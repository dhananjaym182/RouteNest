package providers

import (
	"net/http"
	"strings"
	"sync/atomic"

	"routenest/internal/config"
	"routenest/internal/domain"
	"routenest/internal/upstream/openaicompat"
)

// Domain aliases keep the registry code readable; they are the same types
// internal/domain defines, so values cross packages unchanged.
type CustomProvider = domain.CustomProvider
type Connection = domain.Connection
type Account = domain.Account
type AccountPool = domain.AccountPool
type Model = domain.Model
type ModelStat = domain.ModelStat
type RequestLog = domain.RequestLog

// ProviderIDParam carries the provider ID through the request map for request
// log attribution. It was previously owned by package main.
const ProviderIDParam = "__served_by_id"

// Composition seams are installed by internal/app. Functional defaults keep this
// package independently testable; required seams are nil until wired.
var (
	TransportProvider = func() http.RoundTripper { return http.DefaultTransport }

	BodyOptionsProvider = func() openaicompat.BodyOptions {
		return openaicompat.BodyOptions{}
	}
	// ConfigProvider supplies the live proxy policy, installed by internal/app.
	// Nil until then so a missing composition fails loudly.
	ConfigProvider func() *config.Proxy
)

func liveConfig() *config.Proxy {
	if ConfigProvider == nil {
		panic("providers: app.Configure() was not called (ConfigProvider unset)")
	}
	return ConfigProvider()
}

var freeIDs = func() *atomic.Value {
	v := &atomic.Value{}
	v.Store(map[string]bool{})
	return v
}()

// SetFreeModelIDs installs the root Zen seed IDs and aliases used by provider
// model classification. The copy makes later root mutations irrelevant.
func SetFreeModelIDs(ids []string) {
	values := make(map[string]bool, len(ids))
	for _, id := range ids {
		values[strings.ToLower(id)] = true
	}
	freeIDs.Store(values)
}

func freeModelIDAllowlist() map[string]bool {
	return freeIDs.Load().(map[string]bool)
}
