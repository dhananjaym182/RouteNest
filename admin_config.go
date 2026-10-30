package main

import (
	"log"
	"net/http"
	"runtime"

	"cline-go-proxy/internal/adminapi"
	"cline-go-proxy/internal/buildinfo"
	"cline-go-proxy/internal/i18n"
)

var adminConfig = &adminapi.Config{
	Proxy: getProxyConfig,
	SetProxy: func(cfg *proxyConfigData) {
		setProxyConfig(cfg)
	},
	Models:       getAllModels,
	DefaultModel: getDefaultModel,
	SetDefaultModel: func(model string) {
		p := loadPool()
		poolMu.Lock()
		p.DefaultModel = model
		poolMu.Unlock()
		savePool()
	},
	SetListenHost: func(host string) {
		p := loadPool()
		poolMu.Lock()
		p.ListenHost = host
		poolMu.Unlock()
		savePool()
	},
	LocalIPs:           detectLocalIPs,
	ListenHost:         func() string { return listenHost },
	ListenPort:         func() int { return listenPort },
	EffectiveAdminHost: effectiveAdminHost,
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
	ZenHeaders:      func() map[string]string { return getZenConfig().ZenHeaders },
	PoolPath:        poolPath,
	HasPassword:     func() bool { return loadPool().AdminPasswordHash != "" },
	RestartListener: restartListener,
	Translate:       func(r *http.Request, key string) string { return i18n.TAPI(r, key) },
	Logf:            log.Printf,
}
