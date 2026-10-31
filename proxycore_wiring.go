package main

import (
	"cline-go-proxy/internal/admin"
	"cline-go-proxy/internal/proxycore"
)

// init wires the proxycore package to root-owned live state: proxy config,
// admin route registration, and connection-scoped usage attribution.
func init() {
	proxycore.ConfigProvider = admin.GetProxyConfig
	proxycore.RegisterAdminRoutes = admin.RegisterRoutes
	proxycore.RecordUsage = recordUsage
}
