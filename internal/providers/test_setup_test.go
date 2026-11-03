package providers

// Package-local test composition. Production installs these seams through
// internal/app; tests install explicit fakes so they never depend on package
// init or on a silent fallback.

import (
	"net/http"

	"routenest/internal/config"
	"routenest/internal/connections"
	"routenest/internal/upstream/openaicompat"
)

func init() {
	ConfigProvider = func() *config.Proxy { return config.DefaultProxy() }
	TransportProvider = func() http.RoundTripper { return http.DefaultTransport }
	BodyOptionsProvider = func() openaicompat.BodyOptions { return openaicompat.BodyOptions{} }
	connections.RegisterProviderRefPruner(PruneConnectionRefs)
}
