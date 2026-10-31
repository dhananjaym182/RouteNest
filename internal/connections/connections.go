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
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"routenest/internal/deviceflow"
	"routenest/internal/domain"
	"routenest/internal/platform"
	connectionstore "routenest/internal/store/connections"
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
