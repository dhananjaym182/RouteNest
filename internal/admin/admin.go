package admin

import (
	"cline-go-proxy/internal/routing"
	"cline-go-proxy/internal/zen"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/adminapi"
	"cline-go-proxy/internal/buildinfo"
	"cline-go-proxy/internal/config"
	"cline-go-proxy/internal/connections"
	"cline-go-proxy/internal/egress"
	"cline-go-proxy/internal/httpx"
	"cline-go-proxy/internal/i18n"
	"cline-go-proxy/internal/platform"
	"cline-go-proxy/internal/providers"
	"cline-go-proxy/internal/proxycore"
	"cline-go-proxy/internal/requestlog"
)

type APIResponse = httpx.Response

type apiResponse = APIResponse

func writeAPI(w http.ResponseWriter, status int, resp apiResponse) {
	httpx.WriteAPI(w, status, resp)
}

// 管理后台登录会话（内存态，程序重启后需重新登录）。
var adminSessions = adminapi.NewSessionStore()

const adminSessionCookie = adminapi.SessionCookieName

// adminAuth 连接根接线与 adminapi：密码状态来自账号池，错误文案走 i18n。
var adminAuth = &adminapi.Auth{
	Sessions:    adminSessions,
	PasswordSet: func() bool { return accounts.LoadPool().AdminPasswordHash != "" },
	Unauthorized: func(w http.ResponseWriter, r *http.Request, reason string) {
		writeAPI(w, http.StatusUnauthorized, apiResponse{Error: i18n.TAPI(r, reason)})
	},
}

// adminAuthHandlers connect login/logout/password endpoints to the pool-backed
// password store while adminapi owns the wire behavior.
var adminAuthHandlers = &adminapi.AuthHandlers{
	Sessions:       adminSessions,
	PasswordSet:    func() bool { return accounts.LoadPool().AdminPasswordHash != "" },
	VerifyPassword: verifyAdminPassword,
	SetPassword:    setAdminPassword,
	Translate:      func(r *http.Request, key string) string { return i18n.TAPI(r, key) },
}

// adminKeys/adminMonitoring connect the root composition to adminapi handlers
// without moving pool locking or persistence into the package.
var adminKeys = &adminapi.KeyManager{
	ListKeys: func() []string { return accounts.LoadPool().Keys },
	AddKey: func(key string) {
		p := accounts.LoadPool()
		accounts.PoolMu.Lock()
		p.Keys = append(p.Keys, key)
		accounts.PoolMu.Unlock()
		accounts.SavePool()
	},
	DeleteKey: func(key string) {
		p := accounts.LoadPool()
		accounts.PoolMu.Lock()
		for i, k := range p.Keys {
			if k == key {
				p.Keys = append(p.Keys[:i], p.Keys[i+1:]...)
				break
			}
		}
		accounts.PoolMu.Unlock()
		accounts.SavePool()
	},
	Translate: func(r *http.Request, key string) string { return i18n.TAPI(r, key) },
}

var adminMonitoring = &adminapi.Monitoring{
	Accounts:      func() []*Account { return accounts.LoadPool().Accounts },
	Strategy:      func() string { return GetProxyConfig().Strategy },
	ModelChain:    func() []string { return GetProxyConfig().ModelChain },
	Version:       func() string { return buildinfo.AppVersion },
	StartedAt:     func() time.Time { return buildinfo.ServerStartedAt },
	OpencodeToday: func() any { return requestlog.OpenCodeUsageToday() },
	ListRequestLogs: func(limit int, cursor string) (any, error) {
		return requestlog.List(limit, cursor)
	},
	Translate: func(r *http.Request, key string) string { return i18n.TAPI(r, key) },
}

var adminOpencode = &adminapi.Opencode{
	Get:            zen.GetConfig,
	Set:            zen.SetConfig,
	ValidateProxy:  zen.ValidateProxyList,
	MaskProxy:      zen.MaskProxyURL,
	ProxyCooldowns: zen.ProxyCooldownStatus,
	FailedNow:      zen.FailedNow,
	SyncedModels:   func() int { return len(zen.CurrentModels()) },
	LastSync:       func() any { return zen.LastModelSync() },
	Translate:      func(r *http.Request, key string) string { return i18n.TAPI(r, key) },
}

var adminClineEgress = &adminapi.ClineEgress{
	Get:           egress.GetClineProxyConfig,
	Set:           egress.SetClineProxyConfig,
	PersistErr:    egress.GetClineProxyPersistErr,
	ValidateProxy: zen.ValidateProxyList,
	MaskProxy:     zen.MaskProxyURL,
	Translate:     func(r *http.Request, key string) string { return i18n.TAPI(r, key) },
}

func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/admin/", adminStaticHandler)
	// 无需登录的接口
	mux.HandleFunc("/admin/api/login", proxycore.CorsHandler(handleAdminLogin))
	mux.HandleFunc("/admin/api/logout", proxycore.CorsHandler(handleAdminLogout))
	// 其余 API 全部需要后台鉴权（设置了密码后）
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return requireAdminAuth(proxycore.CorsHandler(h))
	}
	mux.HandleFunc("/admin/api/admin-sessions/clear", auth(handleAdminSessionsClear))
	mux.HandleFunc("/admin/api/accounts", auth(handleAdminAccounts))
	mux.HandleFunc("/admin/api/accounts/add", auth(handleAdminAccountAdd))
	mux.HandleFunc("/admin/api/accounts/delete", auth(handleAdminAccountDelete))
	mux.HandleFunc("/admin/api/accounts/export", auth(handleExportAccounts))
	mux.HandleFunc("/admin/api/oauth/start", auth(handleOAuthStart))
	mux.HandleFunc("/admin/api/oauth/status", auth(handleOAuthStatus))
	mux.HandleFunc("/admin/api/sso/import", auth(handleSSOImport))
	mux.HandleFunc("/admin/api/stats", auth(handleAdminStats))
	mux.HandleFunc("/admin/api/dashboard/overview", auth(handleDashboardOverview))
	mux.HandleFunc("/admin/api/batch-import", auth(handleBatchImport))
	mux.HandleFunc("/admin/api/accounts/refresh-all", auth(handleAdminRefreshAll))
	mux.HandleFunc("/admin/api/accounts/delete-all", auth(handleAdminDeleteAll))
	mux.HandleFunc("/admin/api/accounts/reset", auth(handleAdminAccountReset))
	mux.HandleFunc("/admin/api/accounts/test", auth(handleAdminAccountTest))
	mux.HandleFunc("/admin/api/keys", auth(handleAdminGetKeys))
	mux.HandleFunc("/admin/api/keys/generate", auth(handleAdminGenerateKey))
	mux.HandleFunc("/admin/api/keys/delete", auth(handleAdminDeleteKey))
	mux.HandleFunc("/admin/api/models", auth(handleAdminModels))
	mux.HandleFunc("/admin/api/models/sync", auth(handleAdminModelSync))
	mux.HandleFunc("/admin/api/opencode/config", auth(handleOpenCodeConfig))
	mux.HandleFunc("/admin/api/opencode/config/update", auth(handleOpenCodeConfigUpdate))
	mux.HandleFunc("/admin/api/opencode/models/sync", auth(handleOpenCodeModelSync))
	mux.HandleFunc("/admin/api/cline-proxy/config", auth(handleClineProxyConfig))
	mux.HandleFunc("/admin/api/cline-proxy/config/update", auth(handleClineProxyConfigUpdate))
	mux.HandleFunc("/admin/api/models/add", auth(handleAdminModelAdd))
	mux.HandleFunc("/admin/api/models/delete", auth(handleAdminModelDelete))
	mux.HandleFunc("/admin/api/models/context", auth(handleAdminModelContext))
	mux.HandleFunc("/admin/api/models/capabilities", auth(handleAdminModelCapabilities))
	mux.HandleFunc("/admin/api/models/toggle", auth(HandleAdminModelToggle))
	mux.HandleFunc("/admin/api/models/test", auth(HandleAdminModelTest))
	mux.HandleFunc("/admin/api/config", auth(handleAdminConfig))
	mux.HandleFunc("/admin/api/config/update", auth(handleAdminUpdateConfig))
	mux.HandleFunc("/admin/api/providers", auth(providers.HandleList))
	mux.HandleFunc("/admin/api/providers/save", auth(providers.HandleSave))
	mux.HandleFunc("/admin/api/providers/delete", auth(providers.HandleDelete))
	mux.HandleFunc("/admin/api/providers/test", auth(providers.HandleTest))
	mux.HandleFunc("/admin/api/providers/models", auth(HandleProviderModels))
	mux.HandleFunc("/admin/api/providers/presets", auth(providers.HandlePresets))
	mux.HandleFunc("/admin/api/providers/recheck", auth(adminSync.ProviderRecheck))
	mux.HandleFunc("/admin/api/providers/sync-status", auth(adminSync.ProviderStatus))
	mux.HandleFunc("/admin/api/models/import/preview", auth(providers.HandleImportPreview))
	mux.HandleFunc("/admin/api/models/import/apply", auth(providers.HandleImportApply))
	mux.HandleFunc("/admin/api/models/import/pending", auth(providers.HandleImportPending))
	mux.HandleFunc("/admin/api/models/import/batch", auth(providers.HandleImportBatch))
	mux.HandleFunc("/admin/api/models/import/discard", auth(providers.HandleImportDiscard))
	mux.HandleFunc("/admin/api/models/cost", auth(providers.HandleModelCost))
	mux.HandleFunc("/admin/api/connections", auth(connections.HandleConnectionsList))
	mux.HandleFunc("/admin/api/connections/login/start", auth(connections.HandleConnectionLoginStart))
	mux.HandleFunc("/admin/api/connections/login/status", auth(connections.HandleConnectionLoginStatus))
	mux.HandleFunc("/admin/api/connections/import", auth(connections.HandleConnectionImport))
	mux.HandleFunc("/admin/api/connections/import-batch", auth(connections.HandleConnectionImportBatch))
	mux.HandleFunc("/admin/api/connections/delete", auth(connections.HandleConnectionDelete))
	mux.HandleFunc("/admin/api/connections/test", auth(connections.HandleConnectionTest))
	mux.HandleFunc("/admin/api/combos", auth(handleCombosList))
	mux.HandleFunc("/admin/api/combos/save", auth(handleComboSave))
	mux.HandleFunc("/admin/api/combos/delete", auth(handleComboDelete))
	mux.HandleFunc("/admin/api/combos/toggle", auth(handleComboToggle))
	mux.HandleFunc("/admin/api/combos/clone", auth(handleComboClone))
	mux.HandleFunc("/admin/api/combos/test", auth(HandleComboTest))
	mux.HandleFunc("/admin/api/password", auth(handleAdminPassword))
	mux.HandleFunc("/admin/api/request-logs", auth(handleAdminRequestLogs))
	mux.HandleFunc("/admin/api/request-logs/clear", auth(requestlog.HandleClear))
	mux.HandleFunc("/admin/api/providers/cooldown-reset", auth(providers.HandleCooldownReset))
	mux.HandleFunc("/admin/api/account-health", auth(providers.HandleAccountHealth))
	mux.HandleFunc("/admin/api/version-check", auth(buildinfo.HandleAdminVersionCheck))
	mux.HandleFunc("/admin/api/open-external", auth(handleOpenExternal))
}

// requireAdminAuth 后台访问鉴权中间件：未设置密码直接放行，否则校验会话 cookie。
func requireAdminAuth(next http.HandlerFunc) http.HandlerFunc {
	return adminAuth.Middleware(next)
}

// hashAdminPassword 生成加盐密码哈希：hex(sha256(salt+password))。
func hashAdminPassword(saltHex, password string) string {
	return adminapi.HashPassword(saltHex, password)
}

// setAdminPassword 设置/修改/清除后台密码（空 = 清除），并清空所有会话强制重新登录。
func setAdminPassword(password string) {
	p := accounts.LoadPool()
	accounts.PoolMu.Lock()
	if password == "" {
		p.AdminPasswordHash = ""
		p.AdminPasswordSalt = ""
	} else {
		salt, err := adminapi.NewSalt()
		if err != nil {
			salt = time.Now().Format("20060102150405")
		}
		p.AdminPasswordSalt = salt
		p.AdminPasswordHash = hashAdminPassword(p.AdminPasswordSalt, password)
	}
	accounts.PoolMu.Unlock()
	accounts.SavePool()
	adminSessions.Clear()
}

// verifyAdminPassword 校验后台密码（未设置密码时返回 false）。
func verifyAdminPassword(password string) bool {
	p := accounts.LoadPool()
	accounts.PoolMu.Lock()
	defer accounts.PoolMu.Unlock()
	return adminapi.VerifyPassword(p.AdminPasswordSalt, p.AdminPasswordHash, password)
}

// POST /admin/api/login  body: {password}
func handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	adminAuthHandlers.Login(w, r)
}

// POST /admin/api/logout
func handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	adminAuthHandlers.Logout(w, r)
}

// POST /admin/api/admin-sessions/clear 清空全部后台登录会话（含当前浏览器），
// 用于共享机器上强制所有人重新登录。
func handleAdminSessionsClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: i18n.TAPI(r, "method_not_allowed")})
		return
	}
	adminSessions.Clear()
	log.Printf("admin: cleared all admin login sessions")
	writeAPI(w, http.StatusOK, apiResponse{Success: true})
}

// POST /admin/api/password  body: {password}（空 = 清除密码，恢复无密码访问）
func handleAdminPassword(w http.ResponseWriter, r *http.Request) {
	adminAuthHandlers.Password(w, r)
}

func adminStaticHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/admin/" || r.URL.Path == "/admin" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// 管理页随二进制整版发布：禁止浏览器缓存，避免升级后停在旧版 UI
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(adminHTML))
		return
	}
	http.NotFound(w, r)
}

// GET /admin/api/accounts
func handleAdminAccounts(w http.ResponseWriter, r *http.Request) {
	adminAccounts.List(w, r)
}

// POST /admin/api/accounts/add  body: { refreshToken, email }
func handleAdminAccountAdd(w http.ResponseWriter, r *http.Request) {
	adminAccounts.Add(w, r)
}

// POST /admin/api/accounts/delete  body: { accountId }
func handleAdminAccountDelete(w http.ResponseWriter, r *http.Request) {
	adminAccounts.Delete(w, r)
}

// POST /admin/api/oauth/start  -- Start OAuth device login, returns URL
func handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	adminOAuth.Start(w, r)
}

// GET /admin/api/oauth/status?sessionId=xxx
func handleOAuthStatus(w http.ResponseWriter, r *http.Request) {
	adminOAuth.Status(w, r)
}

// POST /admin/api/sso/import  body: { ssoCookies: string, email?: string }
func handleSSOImport(w http.ResponseWriter, r *http.Request) {
	adminImports.SSOImport(w, r)
}

// POST /admin/api/batch-import  body: { tokens: [{ refreshToken, email }] }
func handleBatchImport(w http.ResponseWriter, r *http.Request) {
	adminImports.BatchImport(w, r)
}

// GET /admin/api/accounts/export — 导出账号为批量导入兼容格式
// POST body: { ids: [accountId...] } — 仅导出勾选的账号（管理页勾选导出）
func handleExportAccounts(w http.ResponseWriter, r *http.Request) {
	adminAccountOps.Export(w, r)
}

// GET /admin/api/open-external?url=... — 用系统默认浏览器打开外部链接
func handleOpenExternal(w http.ResponseWriter, r *http.Request) {
	adminAccountOps.OpenExternal(w, r)
}

// POST /admin/api/accounts/refresh-all
func handleAdminRefreshAll(w http.ResponseWriter, r *http.Request) {
	adminAccountOps.RefreshAll(w, r)
}

// POST /admin/api/accounts/delete-all
func handleAdminDeleteAll(w http.ResponseWriter, r *http.Request) {
	adminAccountOps.DeleteAll(w, r)
}

// POST /admin/api/accounts/reset  body: { accountId }
func handleAdminAccountReset(w http.ResponseWriter, r *http.Request) {
	adminAccountOps.Reset(w, r)
}

// POST /admin/api/accounts/test  body: { accountId?: "" }
func handleAdminAccountTest(w http.ResponseWriter, r *http.Request) {
	adminAccountOps.Test(w, r)
}

// Global proxy config (mutable via API, persisted to .routenest-config.json)
var (
	Config   = LoadProxyConfigFromDisk()
	ConfigMu sync.Mutex
)

type ProxyConfig = config.Proxy

// init 注入 accounts 包需要的根状态：代理配置读取器与 Cline 错误分类器
// （具体错误类型仍定义在 egress，accounts 只消费分类结果）。
func init() {
	accounts.SetProxyConfigProvider(GetProxyConfig)
	accounts.SetAffinityErrorClassifier(classifyAffinityError)
}

// classifyAffinityError 把 egress 的上游错误翻译成 accounts 亲和层需要的两个事实：
// HTTP 状态码（401/429 需解绑）与「账号不可用」标记（直接解绑）。
func classifyAffinityError(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	var apiErr *egress.APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode, false
	}
	var accountErr *routing.ClineAccountUnavailableError
	if errors.As(err, &accountErr) {
		return 0, true
	}
	return 0, false
}

func DefaultProxyConfig() *ProxyConfig {
	return config.DefaultProxy()
}

const proxyConfigPath = config.ProxyPath

// LoadProxyConfigFromDisk 启动时加载持久化的代理配置（轮询策略/请求头），
// 文件不存在或损坏时回退默认值（损坏文件改名备份，不静默覆盖）。
func LoadProxyConfigFromDisk() *ProxyConfig {
	path := platform.ResolveDataPath(proxyConfigPath, nil)
	cfg, err := config.LoadProxy(path)
	if err != nil {
		platform.BackupCorruptFile(path, err)
	}
	return cfg
}

// SaveProxyConfigLocked 落盘当前配置（调用方需持有 ConfigMu）。
func SaveProxyConfigLocked() {
	if err := config.SaveProxy(platform.ResolveDataPath(proxyConfigPath, nil), Config); err != nil {
		log.Printf("proxy config save failed: %v", err)
	}
}

func GetProxyConfig() *ProxyConfig {
	ConfigMu.Lock()
	defer ConfigMu.Unlock()
	return Config
}

func SetProxyConfig(c *ProxyConfig) {
	ConfigMu.Lock()
	defer ConfigMu.Unlock()
	Config = c
	SaveProxyConfigLocked()
}

// GET /admin/api/keys
func handleAdminGetKeys(w http.ResponseWriter, r *http.Request) {
	adminKeys.Get(w, r)
}

// POST /admin/api/keys/generate
func handleAdminGenerateKey(w http.ResponseWriter, r *http.Request) {
	adminKeys.Generate(w, r)
}

// POST /admin/api/keys/delete  body: { key }
func handleAdminDeleteKey(w http.ResponseWriter, r *http.Request) {
	adminKeys.Delete(w, r)
}

// GET /admin/api/config
func handleAdminConfig(w http.ResponseWriter, r *http.Request) {
	adminConfig.Get(w, r)
}

// POST /admin/api/config  body: { strategy?, headers?, defaultModel?, host? }
func handleAdminUpdateConfig(w http.ResponseWriter, r *http.Request) {
	adminConfig.Update(w, r)
}

// GET /admin/api/models
func handleAdminModels(w http.ResponseWriter, r *http.Request) {
	adminModels.List(w, r)
}

// POST /admin/api/models/add  body: { id, provider?, cost? }
func handleAdminModelAdd(w http.ResponseWriter, r *http.Request) {
	adminModels.Add(w, r)
}

// POST /admin/api/models/delete  body: { id }
func handleAdminModelDelete(w http.ResponseWriter, r *http.Request) {
	adminModels.Delete(w, r)
}

// POST /admin/api/models/context  body: { id, context, output }
// 手动设置模型的上下文窗口 / 最大输出 token（压缩阈值与 maybeCompact 按此计算）。
// 0 = 清除为未知（zen 同步会回填默认值）。设置后 zen 模型同步保留该值不再覆盖。
func handleAdminModelContext(w http.ResponseWriter, r *http.Request) {
	adminModels.UpdateContext(w, r)
}

// POST /admin/api/models/capabilities  body: { id, thinkingLevels?, inputModalities?, outputModalities? }
// 声明模型的能力元数据（推理档位/输入输出模态）。未提供的字段保持不变，
// 显式空数组清除该字段。推理请求带不支持的档位时在入口被丢弃。
func handleAdminModelCapabilities(w http.ResponseWriter, r *http.Request) {
	adminModels.UpdateCapabilities(w, r)
}

// POST /admin/api/models/toggle  body: { ids: [...], disabled }
// 启用/停用模型（单个或批量）：停用后不进客户端模型列表、直接请求被拒绝、
// 默认模型与回退链自动跳过；同步保留该标记。停用当前默认模型时清空默认。
func HandleAdminModelToggle(w http.ResponseWriter, r *http.Request) {
	adminModels.Toggle(w, r)
}

// GET /admin/api/stats
func handleAdminStats(w http.ResponseWriter, r *http.Request) {
	adminMonitoring.Stats(w, r)
}

// GET /admin/api/request-logs?limit=50&cursor=...
func handleAdminRequestLogs(w http.ResponseWriter, r *http.Request) {
	adminMonitoring.Logs(w, r)
}

// GET /admin/api/opencode/config — opencode zen 配置 + 运行状态
func handleOpenCodeConfig(w http.ResponseWriter, r *http.Request) {
	adminOpencode.Config(w, r)
}

// POST /admin/api/opencode/config/update — 更新 opencode zen 配置（指针式补丁）
func handleOpenCodeConfigUpdate(w http.ResponseWriter, r *http.Request) {
	adminOpencode.Update(w, r)
}

// GET /admin/api/cline-proxy/config — Cline 出口代理配置（代理地址脱敏返回）
func handleClineProxyConfig(w http.ResponseWriter, r *http.Request) {
	adminClineEgress.Config(w, r)
}

// POST /admin/api/cline-proxy/config/update — 更新 Cline 出口代理配置
func handleClineProxyConfigUpdate(w http.ResponseWriter, r *http.Request) {
	adminClineEgress.Update(w, r)
}

// POST /admin/api/opencode/models/sync — 手动触发一次 opencode 模型同步
func handleOpenCodeModelSync(w http.ResponseWriter, r *http.Request) {
	adminSync.ZenModels(w, r)
}
