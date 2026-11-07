package admin

import (
	"log"
	"net/http"
	"routenest/internal/routing"
	"routenest/internal/zen"
	"runtime"

	"routenest/internal/accounts"
	"routenest/internal/adminapi"
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
		accounts.PoolMu.Unlock()
		accounts.SavePool()
	},
	SetListenHost: func(host string) error {
		p := accounts.LoadPool()
		accounts.PoolMu.Lock()
		old := p.ListenHost
		p.ListenHost = host
		accounts.PoolMu.Unlock()
		if err := accounts.SavePoolStrict(); err != nil {
			accounts.PoolMu.Lock()
			p.ListenHost = old
			accounts.PoolMu.Unlock()
			return err
		}
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
	HasPassword:     func() bool { return accounts.LoadPool().AdminPasswordHash != "" },
	RestartListener: proxycore.RestartListener,
	FatalListenerFailure: func(err error) {
		log.Panicf("administrator listener rollback failed; terminating for supervisor recovery: %v", err)
	},
	ListenerOperations: adminListenerOperations,
	Translate:          func(r *http.Request, key string) string { return i18n.TAPI(r, key) },
	Logf:               log.Printf,
}
