package admin

import (
	"log"
	"net/http"
	"routenest/internal/routing"
	"routenest/internal/zen"
	"runtime"

	"routenest/internal/accounts"
	"routenest/internal/adminapi"
	"routenest/internal/adminauth"
	"routenest/internal/buildinfo"
	"routenest/internal/i18n"
	"routenest/internal/proxycore"
)

var adminListenerOperations = adminapi.NewListenerOperationStore()

var adminConfig = &adminapi.Config{
	Proxy: SnapshotProxyConfig,
	SetProxy: func(cfg *ProxyConfig) {
		SetProxyConfig(cfg)
	},
	Models:       routing.AllModels,
	DefaultModel: routing.DefaultModel,
	SetDefaultModel: func(model string) {
		p := accounts.LoadPool()
		accounts.PoolMu.Lock()
		p.DefaultModel = model
		accounts.SavePoolLocked()
		accounts.PoolMu.Unlock()
	},
	SetListenHost: func(host string) error {
		p := accounts.LoadPool()
		accounts.PoolMu.Lock()
		old := p.ListenHost
		p.ListenHost = host
		if err := accounts.SavePoolStrictLocked(); err != nil {
			p.ListenHost = old
			accounts.PoolMu.Unlock()
			return err
		}
		accounts.PoolMu.Unlock()
		return nil
	},
	LocalIPs:           proxycore.DetectLocalIPs,
	ListenHost:         func() string { return proxycore.ListenHost },
	ListenPort:         func() int { return proxycore.ListenPort },
	EffectiveAdminHost: proxycore.EffectiveAdminHost,
	Version:            buildinfo.AppVersion,
	Build: func() map[string]any {
		m := map[string]any{"goVersion": runtime.Version()}
		if buildinfo.AppGitCommit != "" {
			m["commit"] = buildinfo.AppGitCommit
		}
		if buildinfo.AppBuildDate != "" {
			m["date"] = buildinfo.AppBuildDate
		}
		return m
	},
	ZenHeaders:      func() map[string]string { return zen.GetConfig().ZenHeaders },
	PoolPath:        accounts.PoolPath,
	HasPassword:     hasAdminPassword,
	RestartListener: proxycore.RestartListener,
	FatalListenerFailure: func(err error) {
		log.Panicf("administrator listener rollback failed; terminating for supervisor recovery: %v", err)
	},
	AuditListenerChange: func(r *http.Request, _, newHost string) error {
		runtime, ok := adminSecurityRuntimeFromRequest(r)
		if !ok {
			return adminauth.ErrUnauthorized
		}
		principal, ok := adminPrincipal(r)
		if !ok {
			return adminauth.ErrUnauthorized
		}
		return runtime.service.Mutate(r.Context(), principal, adminauth.AuditListenerChange, newHost, func(*adminauth.State) error {
			return nil
		})
	},
	ListenerOperations: adminListenerOperations,
	Translate:          func(r *http.Request, key string) string { return i18n.TAPI(r, key) },
	Logf:               log.Printf,
}
