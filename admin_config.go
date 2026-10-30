package main

import (
	"cline-go-proxy/internal/zen"
	"log"
	"net/http"
	"runtime"

	"cline-go-proxy/internal/accounts"
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
		p := accounts.LoadPool()
		accounts.PoolMu.Lock()
		p.DefaultModel = model
		accounts.PoolMu.Unlock()
		accounts.SavePool()
	},
	SetListenHost: func(host string) {
		p := accounts.LoadPool()
		accounts.PoolMu.Lock()
		p.ListenHost = host
		accounts.PoolMu.Unlock()
		accounts.SavePool()
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
	ZenHeaders:      func() map[string]string { return zen.GetConfig().ZenHeaders },
	PoolPath:        accounts.PoolPath,
	HasPassword:     func() bool { return accounts.LoadPool().AdminPasswordHash != "" },
	RestartListener: restartListener,
	Translate:       func(r *http.Request, key string) string { return i18n.TAPI(r, key) },
	Logf:            log.Printf,
}
