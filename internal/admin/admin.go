package admin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"routenest/internal/zen"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/adminapi"
	"routenest/internal/adminauth"
	"routenest/internal/adminsso"
	"routenest/internal/buildinfo"
	"routenest/internal/config"
	"routenest/internal/egress"
	"routenest/internal/httpx"
	"routenest/internal/i18n"
	"routenest/internal/requestlog"
)

type APIResponse = httpx.Response

type apiResponse = APIResponse

func writeAPI(w http.ResponseWriter, status int, resp apiResponse) {
	httpx.WriteAPI(w, status, resp)
}

// adminKeys/adminMonitoring connect the root composition to adminapi handlers
// without moving pool locking or persistence into the package.
var adminKeys = &adminapi.KeyManager{
	ListKeys:  accounts.ClientKeySnapshot,
	AddKey:    accounts.AddClientKey,
	DeleteKey: accounts.DeleteClientKey,
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
	runtime, err := configuredAdminSecurity()
	if err != nil {
		panic("register admin routes: " + err.Error())
	}
	registerRoutesWithSecurity(mux, runtime)
}

// RouteSecurityOptions configures the canonical request boundary when an
// embedding or test supplies its own durable adminauth service. This does not
// bypass authentication; it installs the same route registry and middleware as
// the production ConfigureSecurity path.
type RouteSecurityOptions struct {
	CanonicalURL      string
	TrustedProxyCIDRs []string
	ProxyMode         bool
	EnableHSTS        bool
}

func RegisterRoutesWithService(mux *http.ServeMux, service *adminauth.Service, opts RouteSecurityOptions) (io.Closer, error) {
	if mux == nil || service == nil {
		return nil, fmt.Errorf("admin mux and security service are required")
	}
	requestOptions, err := requestSecurityOptions(SecurityOptions{
		ListenHost:        "127.0.0.1",
		ListenPort:        1,
		CanonicalURL:      opts.CanonicalURL,
		TrustedProxyCIDRs: opts.TrustedProxyCIDRs,
		ProxyMode:         opts.ProxyMode,
	})
	if err != nil {
		return nil, err
	}
	sso, err := adminsso.NewManager(context.Background(), adminsso.ManagerOptions{Service: service, CanonicalURL: requestOptions.CanonicalURL})
	if err != nil {
		return nil, err
	}
	registerRoutesWithSecurity(mux, &adminSecurityRuntime{service: service, sso: sso, requestOptions: requestOptions, enableHSTS: opts.EnableHSTS})
	return sso, nil
}

func registerRoutesWithSecurity(mux *http.ServeMux, runtime *adminSecurityRuntime) {
	registry := newAdminRouteRegistry()
	registerSessionRoutes(registry)
	registerAccountsRoutes(registry)
	registerKeysRoutes(registry)
	registerModelsRoutes(registry)
	registerUpstreamsRoutes(registry)
	registerConfigRoutes(registry)
	registerProvidersRoutes(registry)
	registerProviderSystemRoutes(registry)
	registerConnectionsRoutes(registry)
	registerCombosRoutes(registry)
	registerObservabilityRoutes(registry)
	registerMiscRoutes(registry)
	mux.Handle("/admin/api/", runtime.handler(registry))
	mux.Handle("/admin/", runtime.staticHandler(http.HandlerFunc(adminStaticHandler)))
}

func registerPublicRoute(registry *adminRouteRegistry, method, path string, csrf csrfMode, handler http.HandlerFunc) {
	registry.MustHandle(adminRouteDefinition{Method: method, Path: path, Public: true, CSRF: csrf, Handler: handler})
}

func registerProtectedRoute(registry *adminRouteRegistry, method, path string, csrf csrfMode, handler http.HandlerFunc, permissions ...adminauth.Permission) {
	registry.MustHandle(adminRouteDefinition{Method: method, Path: path, Permissions: permissions, CSRF: csrf, Handler: handler})
}

func registerSessionRoutes(registry *adminRouteRegistry) {
	registerPublicRoute(registry, http.MethodGet, "/admin/api/auth/status", csrfSafe, handleAdminAuthStatus)
	registerPublicRoute(registry, http.MethodPost, "/admin/api/auth/bootstrap", csrfLocalEntry, handleAdminBootstrap)
	registerPublicRoute(registry, http.MethodPost, "/admin/api/login", csrfLocalEntry, handleAdminLoginSecure)
	registerPublicRoute(registry, http.MethodPost, "/admin/api/auth/legacy/rotate", csrfLocalEntry, handleAdminLegacyRotation)
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/auth/session", csrfSafe, handleAdminCurrentSession, adminauth.PermissionView)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/logout", csrfSession, handleAdminLogoutSecure, adminauth.PermissionView)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/auth/password", csrfSession, handleAdminPasswordRotation, adminauth.PermissionView)
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/auth/sessions", csrfSafe, handleAdminSessions, adminauth.PermissionSecurityWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/auth/sessions/revoke", csrfSession, handleAdminSessionRevoke, adminauth.PermissionSecurityWrite)
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/auth/users", csrfSafe, handleAdminUsers, adminauth.PermissionSecurityWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/auth/users", csrfSession, handleAdminUserCreate, adminauth.PermissionSecurityWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/auth/users/update", csrfSession, handleAdminUserUpdate, adminauth.PermissionSecurityWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/auth/users/password", csrfSession, handleAdminUserPasswordReset, adminauth.PermissionSecurityWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/auth/users/delete", csrfSession, handleAdminUserDelete, adminauth.PermissionSecurityWrite)
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/auth/audit", csrfSafe, handleAdminAudit, adminauth.PermissionSecurityWrite)
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/auth/sso/providers", csrfSafe, handleAdminSSOProviders, adminauth.PermissionSecurityWrite)
	registerProtectedRoute(registry, http.MethodPut, "/admin/api/auth/sso/providers", csrfSession, handleAdminSSOProviderSave, adminauth.PermissionSecurityWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/auth/sso/providers/delete", csrfSession, handleAdminSSOProviderDelete, adminauth.PermissionSecurityWrite)
	registerPublicRoute(registry, http.MethodPost, "/admin/api/auth/oidc/start", csrfSSOStart, handleAdminOIDCStart)
	registerPublicRoute(registry, http.MethodGet, "/admin/api/auth/oidc/callback", csrfOIDCCallback, handleAdminOIDCCallback)
	registerPublicRoute(registry, http.MethodGet, "/admin/api/auth/saml/metadata", csrfSafe, handleAdminSAMLMetadata)
	registerPublicRoute(registry, http.MethodPost, "/admin/api/auth/saml/start", csrfSSOStart, handleAdminSAMLStart)
	registerPublicRoute(registry, http.MethodPost, "/admin/api/auth/saml/acs", csrfSAMLACS, handleAdminSAMLACS)
}

func registerAccountsRoutes(registry *adminRouteRegistry) {
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/accounts", csrfSafe, handleAdminAccounts, adminauth.PermissionCredentialWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/accounts/add", csrfSession, handleAdminAccountAdd, adminauth.PermissionCredentialWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/accounts/delete", csrfSession, handleAdminAccountDelete, adminauth.PermissionCredentialWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/accounts/export", csrfSession, handleExportAccounts, adminauth.PermissionCredentialExport)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/oauth/start", csrfSession, handleOAuthStart, adminauth.PermissionCredentialWrite)
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/oauth/status", csrfSafe, handleOAuthStatus, adminauth.PermissionCredentialWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/sso/import", csrfSession, handleSSOImport, adminauth.PermissionCredentialWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/batch-import", csrfSession, handleBatchImport, adminauth.PermissionCredentialWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/accounts/refresh-all", csrfSession, handleAdminRefreshAll, adminauth.PermissionCredentialWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/accounts/delete-all", csrfSession, handleAdminDeleteAll, adminauth.PermissionCredentialWrite, adminauth.PermissionDangerousExecute)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/accounts/reset", csrfSession, handleAdminAccountReset, adminauth.PermissionCredentialWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/accounts/test", csrfSession, handleAdminAccountTest, adminauth.PermissionCredentialWrite)
}

func registerKeysRoutes(registry *adminRouteRegistry) {
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/keys", csrfSafe, handleAdminGetKeys, adminauth.PermissionCredentialWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/keys/generate", csrfSession, handleAdminGenerateKey, adminauth.PermissionCredentialWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/keys/delete", csrfSession, handleAdminDeleteKey, adminauth.PermissionCredentialWrite)
}

func registerModelsRoutes(registry *adminRouteRegistry) {
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/models", csrfSafe, handleAdminModels, adminauth.PermissionView)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/models/sync", csrfSession, handleAdminModelSync, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/models/add", csrfSession, handleAdminModelAdd, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/models/delete", csrfSession, handleAdminModelDelete, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/models/delisted/remove", csrfSession, handleDelistedModelRemoval, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/models/remove", csrfSession, handleModelRemoval, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/models/context", csrfSession, handleAdminModelContext, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/models/capabilities", csrfSession, handleAdminModelCapabilities, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/models/toggle", csrfSession, HandleAdminModelToggle, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/models/test", csrfSession, HandleAdminModelTest, adminauth.PermissionProviderWrite)
}

func registerUpstreamsRoutes(registry *adminRouteRegistry) {
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/opencode/config", csrfSafe, handleOpenCodeConfig, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/opencode/config/update", csrfSession, handleOpenCodeConfigUpdate, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/opencode/models/sync", csrfSession, handleOpenCodeModelSync, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/cline-proxy/config", csrfSafe, handleClineProxyConfig, adminauth.PermissionView)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/cline-proxy/config/update", csrfSession, handleClineProxyConfigUpdate, adminauth.PermissionProviderWrite)
}

func registerConfigRoutes(registry *adminRouteRegistry) {
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/config", csrfSafe, handleAdminConfig, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/config/update", csrfSession, handleAdminUpdateConfig, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/config/listener", csrfSession, handleAdminUpdateListener, adminauth.PermissionSecurityWrite)
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/listener-operation", csrfSafe, handleAdminListenerOperation, adminauth.PermissionSecurityWrite)
}

func registerProvidersRoutes(registry *adminRouteRegistry) {
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/providers", csrfSafe, handleProvidersList, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/providers/save", csrfSession, handleProviderSave, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/providers/delete", csrfSession, handleProviderDelete, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/providers/test", csrfSession, handleProviderTest, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/providers/models", csrfSession, HandleProviderModels, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/providers/presets", csrfSafe, handleProviderPresets, adminauth.PermissionView)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/providers/recheck", csrfSession, adminSync.ProviderRecheck, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/providers/sync-status", csrfSafe, adminSync.ProviderStatus, adminauth.PermissionView)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/models/import/preview", csrfSession, handleModelImportPreview, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/models/import/apply", csrfSession, handleModelImportApply, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/models/import/pending", csrfSafe, handleModelImportPending, adminauth.PermissionView)
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/models/import/batch", csrfSafe, handleModelImportBatch, adminauth.PermissionView)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/models/import/discard", csrfSession, handleModelImportDiscard, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/models/cost", csrfSession, handleModelCost, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/providers/cooldown-reset", csrfSession, handleProviderCooldownReset, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/account-health", csrfSafe, handleAccountHealth, adminauth.PermissionCredentialWrite)
}

func registerProviderSystemRoutes(registry *adminRouteRegistry) {
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/provider-instances", csrfSafe, handleProviderInstances, adminauth.PermissionView)
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/credentials", csrfSafe, handleProviderCredentials, adminauth.PermissionCredentialWrite)
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/model-offerings", csrfSafe, handleModelOfferings, adminauth.PermissionView)
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/provider-state", csrfSafe, handleProviderState, adminauth.PermissionView)
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/egress-profiles", csrfSafe, handleEgressProfiles, adminauth.PermissionView)
}

func registerConnectionsRoutes(registry *adminRouteRegistry) {
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/connections", csrfSafe, handleConnectionsList, adminauth.PermissionCredentialWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/connections/login/start", csrfSession, handleConnectionLoginStart, adminauth.PermissionCredentialWrite)
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/connections/login/status", csrfSafe, handleConnectionLoginStatus, adminauth.PermissionCredentialWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/connections/import", csrfSession, handleConnectionImport, adminauth.PermissionCredentialWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/connections/import-batch", csrfSession, handleConnectionImportBatch, adminauth.PermissionCredentialWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/connections/delete", csrfSession, handleConnectionDelete, adminauth.PermissionCredentialWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/connections/test", csrfSession, handleConnectionTest, adminauth.PermissionCredentialWrite)
}

func registerCombosRoutes(registry *adminRouteRegistry) {
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/combos", csrfSafe, handleCombosList, adminauth.PermissionView)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/combos/save", csrfSession, handleComboSave, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/combos/delete", csrfSession, handleComboDelete, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/combos/toggle", csrfSession, handleComboToggle, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/combos/clone", csrfSession, handleComboClone, adminauth.PermissionProviderWrite)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/combos/test", csrfSession, HandleComboTest, adminauth.PermissionProviderWrite)
}

func registerObservabilityRoutes(registry *adminRouteRegistry) {
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/stats", csrfSafe, handleAdminStats, adminauth.PermissionView)
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/dashboard/overview", csrfSafe, handleDashboardOverview, adminauth.PermissionView)
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/request-logs", csrfSafe, handleAdminRequestLogs, adminauth.PermissionView)
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/request-logs/clear", csrfSession, handleRequestLogsClear, adminauth.PermissionSecurityWrite, adminauth.PermissionDangerousExecute)
	registerProtectedRoute(registry, http.MethodGet, "/admin/api/version-check", csrfSafe, handleAdminVersionCheck, adminauth.PermissionView)
}

func registerMiscRoutes(registry *adminRouteRegistry) {
	registerProtectedRoute(registry, http.MethodPost, "/admin/api/open-external", csrfSession, handleOpenExternal, adminauth.PermissionView)
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
type ProxyConfig = config.Proxy

func DefaultProxyConfig() *ProxyConfig {
	return config.DefaultProxy()
}

// LoadProxyConfigFromDisk loads the persisted proxy configuration at startup.
func LoadProxyConfigFromDisk() *ProxyConfig {
	return config.LoadLiveFromDisk()
}

// SaveProxyConfigLocked persists the live configuration. The caller holds config.Mu.
func SaveProxyConfigLocked() {
	config.SaveLocked()
}

// GetProxyConfig returns the live configuration. See config.Live.
func GetProxyConfig() *ProxyConfig {
	return config.Live()
}

// SnapshotProxyConfig returns a private copy of the live configuration.
func SnapshotProxyConfig() *ProxyConfig {
	return config.Snapshot()
}

// SetProxyConfig publishes c as the live configuration and persists it.
func SetProxyConfig(c *ProxyConfig) {
	config.Publish(c)
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

// POST /admin/api/config/update  body: provider and routing configuration patch
func handleAdminUpdateConfig(w http.ResponseWriter, r *http.Request) {
	adminConfig.Update(w, r)
}

// POST /admin/api/config/listener  body: { host }
func handleAdminUpdateListener(w http.ResponseWriter, r *http.Request) {
	adminConfig.UpdateListener(w, r)
}

func handleAdminListenerOperation(w http.ResponseWriter, r *http.Request) {
	adminConfig.ListenerOperation(w, r)
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
