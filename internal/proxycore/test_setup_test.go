package proxycore

import (
	"net/http"

	"cline-go-proxy/internal/config"
	"cline-go-proxy/internal/domain"
	"cline-go-proxy/internal/egress"
	"cline-go-proxy/internal/providers"
	"cline-go-proxy/internal/routing"
)

type Account = domain.Account
type AccountPool = domain.AccountPool
type RequestLog = domain.RequestLog
type tokenUsage = TokenUsage

type freeModelRoundTripper func(*http.Request) (*http.Response, error)

func (f freeModelRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

var testProxyConfig = config.DefaultProxy()

func defaultProxyConfig() *config.Proxy { return config.DefaultProxy() }

func getProxyConfig() *config.Proxy { return testProxyConfig }

func setProxyConfig(cfg *config.Proxy) {
	testProxyConfig = cfg
	provider := func() *config.Proxy { return testProxyConfig }
	ConfigProvider = provider
	routing.ConfigProvider = provider
	egress.ConfigProvider = provider
	providers.ConfigProvider = provider
}

func init() { setProxyConfig(testProxyConfig) }
