package connections

// ============================================================================
// §8.3 Provider Connections：provider 作用域的登录凭据（与 Cline 账号池并存）。
// 引擎按 flowSpec 数据驱动（对齐 ExtremeRouter 的五种 flowType 中最常用的
// device_code），新增一个 provider 登录 = 往 ProviderDeviceFlows 加一条数据，
// 不新增控制流。Kilo Code 为首个具体移植（§8.3.2，步骤 4）。
// ============================================================================

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"cline-go-proxy/internal/deviceflow"
	"cline-go-proxy/internal/domain"
	"cline-go-proxy/internal/httpx"
	"cline-go-proxy/internal/platform"
	connectionstore "cline-go-proxy/internal/store/connections"
)

// Domain aliases keep the moved connection code terse; they are the same types
// the root package aliases through types.go, so values cross unchanged.
type Connection = domain.Connection
type CustomProvider = domain.CustomProvider
type ModelStat = domain.ModelStat

// HTTPClient is the shared outbound client, injected by the root package
// (http.go) so device-flow auth traffic keeps using the egress proxy pool.
var HTTPClient *http.Client

func httpClientOrDefault() *http.Client {
	if HTTPClient != nil {
		return HTTPClient
	}
	return http.DefaultClient
}

var (
	ConnectionsMu     sync.Mutex
	connections       []*Connection
	connectionsLoaded bool
	FilePath          string
)

func init() {
	FilePath = platform.ResolveDataPath(connectionstore.FileName, nil)
}

func LoadConnections() {
	ConnectionsMu.Lock()
	defer ConnectionsMu.Unlock()
	if connectionsLoaded {
		return
	}
	connectionsLoaded = true
	list, err := connectionstore.Load(FilePath)
	if err != nil {
		platform.BackupCorruptFile(FilePath, err)
		return
	}
	connections = list
}

// saveConnectionsLocked 调用方持 ConnectionsMu。
func saveConnectionsLocked() {
	if err := connectionstore.Save(FilePath, connections); err != nil {
		log.Printf("connections save failed: %v", err)
	}
}

func newConnectionID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "conn_" + fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return "conn_" + hex.EncodeToString(b)
}

// UpsertConnection 按 provider+email 去重（无 email 时按 token 指纹）；替换时保留原 ID/CreatedAt。
func UpsertConnection(c *Connection) *Connection {
	LoadConnections()
	ConnectionsMu.Lock()
	defer ConnectionsMu.Unlock()
	fingerprint := ""
	if c.Email == "" && c.AccessToken != "" {
		sum := sha256.Sum256([]byte(c.AccessToken))
		fingerprint = hex.EncodeToString(sum[:])[:12]
	}
	for i, old := range connections {
		same := old.Provider == c.Provider && ((c.Email != "" && old.Email == c.Email) ||
			(fingerprint != "" && old.ProviderSpecificData["tokenFp"] == fingerprint))
		if same {
			c.ID, c.CreatedAt = old.ID, old.CreatedAt
			// Usage history survives re-login/re-import replacements.
			c.UsageCount, c.PromptTokens, c.CompletionTokens = old.UsageCount, old.PromptTokens, old.CompletionTokens
			c.TotalTokens, c.CachedTokens, c.ModelStats = old.TotalTokens, old.CachedTokens, old.ModelStats
			if c.ProviderSpecificData == nil {
				c.ProviderSpecificData = map[string]string{}
			}
			if fingerprint != "" {
				c.ProviderSpecificData["tokenFp"] = fingerprint
			}
			connections[i] = c
			saveConnectionsLocked()
			return c
		}
	}
	c.ID = newConnectionID()
	c.CreatedAt = time.Now()
	if c.ProviderSpecificData == nil {
		c.ProviderSpecificData = map[string]string{}
	}
	if fingerprint != "" {
		c.ProviderSpecificData["tokenFp"] = fingerprint
	}
	connections = append(connections, c)
	saveConnectionsLocked()
	return c
}

func RemoveConnection(id string) bool {
	LoadConnections()
	ConnectionsMu.Lock()
	removed := false
	for i, c := range connections {
		if c.ID == id {
			connections = append(connections[:i], connections[i+1:]...)
			saveConnectionsLocked()
			removed = true
			break
		}
	}
	ConnectionsMu.Unlock()
	if removed {
		// Wave-4: dropped connections must not linger in provider pools.
		// Prune outside ConnectionsMu — PruneConnectionRefs takes the provider
		// lock, and candidate resolution holds that lock while taking
		// ConnectionsMu (reverse order would deadlock).
		PruneConnectionRefs(id)
	}
	return removed
}

// MarkConnectionReauth 上游 401/403 且无刷新能力时调用（不锁死，用户重登即恢复）。
func MarkConnectionReauth(id string) {
	LoadConnections()
	ConnectionsMu.Lock()
	defer ConnectionsMu.Unlock()
	for _, c := range connections {
		if c.ID == id && c.Status == "active" {
			c.Status = "reauth_required"
			saveConnectionsLocked()
			return
		}
	}
}

// ---------- device-code 流程引擎 ----------

// deviceFlowSpec aliases the deviceflow package spec so existing call sites and
// tests can keep using the local name while the engine lives in its own package.
type deviceFlowSpec = deviceflow.Spec

// ProviderDeviceFlows is the mutable flow registry. Built-in specs come from
// the deviceflow package; tests may register additional entries.
var ProviderDeviceFlows = deviceflow.Specs()

// 登录会话：start 建会话并后台轮询；status 读取（对齐 Cline WorkOS 会话 UX）。
type providerLoginSession struct {
	ID         string
	Provider   string
	UserCode   string
	AuthURL    string
	CreatedAt  time.Time
	Done       bool
	Success    bool
	Error      string
	Connection *Connection
}

var (
	providerLoginMu       sync.Mutex
	providerLoginSessions = map[string]*providerLoginSession{}
)

// jsonPathString resolves a dotted JSON path; implementation lives in deviceflow.
func jsonPathString(body map[string]any, path string) string {
	return deviceflow.JSONPathString(body, path)
}

// deviceLoginResult 两种轮询模式（kilo / rfc8628）的统一成功形态。
type deviceLoginResult struct {
	token, refreshToken, email, orgID string
	userID                            string // qoder：上游 user_id，用于邮箱缺失时的账号标识
	expiresAtMs                       int64  // 0 = 不过期（Kilo 语义）
	exchangedToken                    string
	exchangedExpMs                    int64
}

func newUUIDv4() (string, error) { return deviceflow.NewUUIDv4() }

func newPKCE() (verifier, challenge string, err error) { return deviceflow.NewPKCE() }

func statePollHeaders(spec deviceFlowSpec, isPoll bool) map[string]string {
	return deviceflow.StatePollHeaders(spec, isPoll)
}

// exchangeJobToken performs the optional second exchange (GitHub Copilot).
func exchangeJobToken(spec deviceFlowSpec, accessToken string) (token string, expMs int64, err error) {
	return deviceflow.ExchangeJobToken(context.Background(), OAuthHTTPClient(15*time.Second), spec, accessToken)
}

// OAuthHTTPClient routes auth traffic through the shared outbound transport
// (SOCKS5 egress proxy pool + custom dialer) like proxied chat traffic; a bare
// &http.Client{} would bypass the configured egress proxy entirely.
func OAuthHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: httpClientOrDefault().Transport}
}

func qoderExpiryMs(data map[string]any) int64 { return deviceflow.QoderExpiryMs(data) }

func epochToMs(n int64) int64 { return deviceflow.EpochToMs(n) }

// startProviderDeviceLogin initiates a flow, registers a session, and polls in
// the background. Persistence stays here; the deviceflow package only performs
// the upstream authentication exchange.
func startProviderDeviceLogin(provider string) (*providerLoginSession, error) {
	spec, ok := ProviderDeviceFlows[provider]
	if !ok {
		return nil, fmt.Errorf("no device_code flow registered for provider %q", provider)
	}
	init, err := deviceflow.Initiate(context.Background(), OAuthHTTPClient(15*time.Second), spec)
	if err != nil {
		return nil, err
	}
	sess := &providerLoginSession{
		ID:        fmt.Sprintf("plog_%d_%s", time.Now().UnixNano(), provider),
		Provider:  provider,
		UserCode:  init.UserCode,
		AuthURL:   init.VerificationURL,
		CreatedAt: time.Now(),
	}
	providerLoginMu.Lock()
	for id, old := range providerLoginSessions {
		if len(providerLoginSessions) >= 20 || time.Since(old.CreatedAt) > 15*time.Minute {
			delete(providerLoginSessions, id)
		}
	}
	providerLoginSessions[sess.ID] = sess
	providerLoginMu.Unlock()

	go func() {
		res, pollErr := pollProviderDeviceToken(spec, init.Code, init.ExpiresIn, init.Verifier)
		var conn *Connection
		if pollErr == nil {
			conn = UpsertConnection(&Connection{
				Provider: provider, AuthType: "device_code", Email: res.email,
				AccessToken: res.token, RefreshToken: res.refreshToken,
				ExpiresAt: res.expiresAtMs, Status: "active",
			})
			psd := map[string]string{}
			if res.orgID != "" {
				psd["orgId"] = res.orgID
			}
			if spec.Mode == "qoder" {
				psd["authMethod"] = "device"
				if res.userID != "" {
					psd["userId"] = res.userID
				}
				if init.MachineID != "" {
					psd["machineId"] = init.MachineID
				}
			}
			if res.exchangedToken != "" {
				psd["exchangedToken"] = res.exchangedToken
				psd["exchangedExp"] = fmt.Sprintf("%d", res.exchangedExpMs)
			}
			if len(psd) > 0 {
				ConnectionsMu.Lock()
				for k, v := range psd {
					conn.ProviderSpecificData[k] = v
				}
				saveConnectionsLocked()
				ConnectionsMu.Unlock()
			}
		}
		providerLoginMu.Lock()
		if pollErr != nil {
			sess.Done, sess.Success, sess.Error = true, false, pollErr.Error()
		} else {
			sess.Done, sess.Success, sess.Connection = true, true, conn
		}
		providerLoginMu.Unlock()
	}()
	return sess, nil
}

// pollProviderDeviceToken polls until a token arrives and maps the deviceflow
// package result onto the in-package result shape.
func pollProviderDeviceToken(spec deviceFlowSpec, code string, expiresIn int, verifier string) (*deviceLoginResult, error) {
	res, err := deviceflow.Poll(context.Background(), OAuthHTTPClient(15*time.Second), spec, &deviceflow.Initiation{
		Code: code, ExpiresIn: expiresIn, Verifier: verifier,
	})
	if err != nil {
		return nil, err
	}
	return &deviceLoginResult{
		token: res.Token, refreshToken: res.RefreshToken, email: res.Email,
		orgID: res.OrgID, userID: res.UserID, expiresAtMs: res.ExpiresAtMs,
		exchangedToken: res.ExchangedToken, exchangedExpMs: res.ExchangedExpMs,
	}, nil
}

// updateConnectionPSD 按 id 合并写 ProviderSpecificData（调用方无需持锁）。
func updateConnectionPSD(id string, kv map[string]string) {
	LoadConnections()
	ConnectionsMu.Lock()
	defer ConnectionsMu.Unlock()
	for _, c := range connections {
		if c.ID == id {
			for k, v := range kv {
				c.ProviderSpecificData[k] = v
			}
			saveConnectionsLocked()
			return
		}
	}
}

// connectionBearer 派发/测试用：默认 OAuth access token；带二段兑换的（Copilot）
// 优先用短期作业票，临期自动重兑换并回存。
// refreshRFC8628Connection performs a standard OAuth refresh via deviceflow and
// persists the new tokens. Returns false when the upstream cannot refresh.
func refreshRFC8628Connection(conn *Connection, spec deviceFlowSpec) bool {
	at, rt, expMs, ok := deviceflow.RefreshRFC8628(
		context.Background(), OAuthHTTPClient(15*time.Second), spec, conn.RefreshToken, conn.ExpiresAt)
	if !ok {
		return false
	}
	ConnectionsMu.Lock()
	for _, c := range connections {
		if c.ID == conn.ID {
			c.AccessToken, c.RefreshToken, c.ExpiresAt, c.Status = at, rt, expMs, "active"
			break
		}
	}
	saveConnectionsLocked()
	ConnectionsMu.Unlock()
	return true
}

func ConnectionBearer(conn *Connection, spec deviceFlowSpec) string {
	if spec.Mode == "rfc8628" && conn.RefreshToken != "" &&
		conn.ExpiresAt > 0 && time.Now().UnixMilli() >= conn.ExpiresAt-60_000 {
		if refreshRFC8628Connection(conn, spec) {
			ConnectionsMu.Lock()
			var fresh string
			for _, c := range connections {
				if c.ID == conn.ID {
					fresh = c.AccessToken
					break
				}
			}
			ConnectionsMu.Unlock()
			if fresh != "" {
				return fresh
			}
		}
	}
	if spec.ExchangeURL == "" {
		return conn.AccessToken
	}
	exp, _ := strconv.ParseInt(conn.ProviderSpecificData["exchangedExp"], 10, 64)
	if t := conn.ProviderSpecificData["exchangedToken"]; t != "" && time.Now().UnixMilli() < exp-30_000 {
		return t
	}
	if t, expMs, err := exchangeJobToken(spec, conn.AccessToken); err == nil {
		updateConnectionPSD(conn.ID, map[string]string{"exchangedToken": t, "exchangedExp": fmt.Sprintf("%d", expMs)})
		return t
	}
	return conn.AccessToken // 兑换失败让上游 401 → reauth 标记兜底
}

// ---------- admin handlers ----------

func HandleConnectionsList(w http.ResponseWriter, r *http.Request) {
	LoadConnections()
	ConnectionsMu.Lock()
	out := make([]Connection, 0, len(connections))
	for _, c := range connections {
		cp := *c
		cp.AccessToken = "" // 列表不外泄 token
		out = append(out, cp)
	}
	ConnectionsMu.Unlock()
	flows := make([]string, 0, len(ProviderDeviceFlows))
	for p := range ProviderDeviceFlows {
		flows = append(flows, p)
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"connections": out, "flows": flows}})
}

// POST /admin/api/connections/login/start  body: {provider}
func HandleConnectionLoginStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "invalid_json"})
		return
	}
	sess, err := startProviderDeviceLogin(strings.TrimSpace(req.Provider))
	if err != nil {
		httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"error": err.Error()}})
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{
		"sessionId": sess.ID, "userCode": sess.UserCode, "authUrl": sess.AuthURL,
	}})
}

// GET /admin/api/connections/login/status?sessionId=x
func HandleConnectionLoginStatus(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("sessionId")
	providerLoginMu.Lock()
	sess, ok := providerLoginSessions[id]
	var out map[string]any
	if ok {
		out = map[string]any{"done": sess.Done, "success": sess.Success, "error": sess.Error, "userCode": sess.UserCode, "authUrl": sess.AuthURL}
		if sess.Connection != nil {
			cp := *sess.Connection
			cp.AccessToken = ""
			out["connection"] = cp
		}
	}
	providerLoginMu.Unlock()
	if !ok {
		httpx.WriteAPI(w, http.StatusNotFound, httpx.Response{Error: "session not found"})
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: out})
}

// ImportConnectionToken 单条粘贴 token 的共享语义（单条导入与批量导入共用）：
// trim 后校验，走 provider+email（无 email 时 token 指纹）去重，落 orgId。
func ImportConnectionToken(provider, accessToken, email, orgID string) (*Connection, error) {
	provider = strings.TrimSpace(provider)
	accessToken = strings.TrimSpace(accessToken)
	email = strings.TrimSpace(email)
	orgID = strings.TrimSpace(orgID)
	if provider == "" || accessToken == "" {
		return nil, fmt.Errorf("provider and accessToken required")
	}
	conn := UpsertConnection(&Connection{
		Provider: provider, AuthType: "import_token", Email: email,
		AccessToken: accessToken, Status: "active",
	})
	if orgID != "" {
		ConnectionsMu.Lock()
		conn.ProviderSpecificData["orgId"] = orgID
		saveConnectionsLocked()
		ConnectionsMu.Unlock()
	}
	return conn, nil
}

// POST /admin/api/connections/import  body: {provider, accessToken, email?, orgId?}
// 粘贴 token 路径（Kilo 这类无 refresh 的上游同样适用），复用去重与状态语义。
func HandleConnectionImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider    string `json:"provider"`
		AccessToken string `json:"accessToken"`
		Email       string `json:"email"`
		OrgID       string `json:"orgId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "provider and accessToken required"})
		return
	}
	conn, err := ImportConnectionToken(req.Provider, req.AccessToken, req.Email, req.OrgID)
	if err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: err.Error()})
		return
	}
	cp := *conn
	cp.AccessToken = ""
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"connection": cp}})
}

// connectionImportBatchItem 批量导入的单项输入（token 只在请求内出现，绝不出现在响应）。
type connectionImportBatchItem struct {
	Provider    string `json:"provider"`
	AccessToken string `json:"accessToken"`
	Email       string `json:"email"`
	OrgID       string `json:"orgId"`
}

// connectionImportBatchResult 单项结果：保留输入顺序与原 index，失败不中断整批。
type connectionImportBatchResult struct {
	Index    int    `json:"index"`
	OK       bool   `json:"ok"`
	ID       string `json:"id,omitempty"`
	Provider string `json:"provider"`
	Email    string `json:"email,omitempty"`
	Error    string `json:"error,omitempty"`
}

// POST /admin/api/connections/import-batch  body: {items:[{provider, accessToken, email?, orgId?}]}
// 管理端「导入 Token」弹窗的批量 tab：逐项复用 ImportConnectionToken，返回逐项结果。
func HandleConnectionImportBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Items []connectionImportBatchItem `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Items) == 0 {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "items required"})
		return
	}
	if len(req.Items) > 500 {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "too many items (max 500)"})
		return
	}
	results := make([]connectionImportBatchResult, 0, len(req.Items))
	imported, failed := 0, 0
	for i, item := range req.Items {
		res := connectionImportBatchResult{Index: i, Provider: strings.TrimSpace(item.Provider)}
		conn, err := ImportConnectionToken(item.Provider, item.AccessToken, item.Email, item.OrgID)
		if err != nil {
			res.Error = err.Error()
			failed++
		} else {
			res.OK, res.ID, res.Provider, res.Email = true, conn.ID, conn.Provider, conn.Email
			imported++
		}
		results = append(results, res)
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{
		"total": len(req.Items), "imported": imported, "failed": failed, "results": results,
	}})
}

// POST /admin/api/connections/delete  body: {id}
func HandleConnectionDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "invalid_json"})
		return
	}
	if !RemoveConnection(req.ID) {
		httpx.WriteAPI(w, http.StatusNotFound, httpx.Response{Error: "connection not found"})
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true})
}

// POST /admin/api/connections/test  body: {id}
// bearer + 动态头探测 ModelsTestURL；同时把 401/403 反馈到 reauth 标记。
func HandleConnectionTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "invalid_json"})
		return
	}
	LoadConnections()
	ConnectionsMu.Lock()
	var conn *Connection
	for _, c := range connections {
		if c.ID == req.ID {
			cp := *c
			conn = &cp
			break
		}
	}
	ConnectionsMu.Unlock()
	if conn == nil {
		httpx.WriteAPI(w, http.StatusNotFound, httpx.Response{Error: "connection not found"})
		return
	}
	spec, ok := ProviderDeviceFlows[conn.Provider]
	if !ok || spec.ModelsTestURL == "" {
		httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"ok": false, "error": "no test endpoint for provider " + conn.Provider}})
		return
	}
	if conn.ExpiresAt > 0 && time.Now().UnixMilli() >= conn.ExpiresAt && conn.RefreshToken == "" {
		MarkConnectionReauth(conn.ID)
		httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"ok": false, "error": "token expired; please re-login"}})
		return
	}
	hreq, _ := http.NewRequest(http.MethodGet, spec.ModelsTestURL, nil)
	hreq.Header.Set("Authorization", "Bearer "+ConnectionBearer(conn, spec))
	if spec.UserAgent != "" {
		hreq.Header.Set("User-Agent", spec.UserAgent)
	}
	if spec.OrgHeader != "" && conn.ProviderSpecificData["orgId"] != "" {
		hreq.Header.Set(spec.OrgHeader, conn.ProviderSpecificData["orgId"])
	}
	resp, err := OAuthHTTPClient(20 * time.Second).Do(hreq)
	if err != nil {
		httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"ok": false, "error": err.Error()}})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		MarkConnectionReauth(conn.ID)
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{
		"ok": resp.StatusCode == http.StatusOK, "status": resp.StatusCode,
		"body": platform.Truncate(strings.TrimSpace(string(body)), 240),
	}})
}
