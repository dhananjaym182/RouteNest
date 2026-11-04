package proxycore

import (
	"net/http"

	"routenest/internal/config"
	"routenest/internal/domain"
	"routenest/internal/egress"
	"routenest/internal/provideradapters"
	clineadapter "routenest/internal/provideradapters/cline"
	"routenest/internal/providers"
	"routenest/internal/routing"
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

func init() {
	setProxyConfig(testProxyConfig)
	// Usage attribution is a composed hook in production; tests keep a no-op.
	RecordUsage = func(*domain.Account, *http.Response, *domain.RequestLog, string, TokenUsage) {}
	// The real composition root installs the adapter registry; this package
	// cannot import app (app imports proxycore), so install the Cline bundle
	// the dispatch path needs.
	adapter := clineadapter.New()
	registry, err := provideradapters.NewRegistry(provideradapters.Bundle{
		ID: "cline", Invoker: adapter, FailureClassifier: adapter,
	})
	if err != nil {
		panic(err)
	}
	if err := provideradapters.Publish(registry); err != nil {
		panic(err)
	}
}
