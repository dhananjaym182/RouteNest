package proxycore

import (
	"cline-go-proxy/internal/config"
	"cline-go-proxy/internal/domain"
)

type Account = domain.Account
type RequestLog = domain.RequestLog
type tokenUsage = TokenUsage

func defaultProxyConfig() *config.Proxy { return config.DefaultProxy() }

func setProxyConfig(cfg *config.Proxy) {
	ConfigProvider = func() *config.Proxy { return cfg }
}
