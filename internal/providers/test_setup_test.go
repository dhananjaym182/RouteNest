package providers

// Package-local test composition. Production installs these seams through
// internal/app; tests install explicit fakes so they never depend on package
// init or on a silent fallback.

import (
	"net/http"
	"time"

	"routenest/internal/budget"
	"routenest/internal/config"
	"routenest/internal/connections"
	"routenest/internal/egress"
)

func init() {
	_ = budget.Publish(budget.NewStore(newTestBudgetBackend(), nil, budget.Options{Now: time.Now}))
	ConfigProvider = func() *config.Proxy { return config.DefaultProxy() }
	egress.ConfigProvider = func() *config.Proxy { return config.DefaultProxy() }
	TransportProvider = func() http.RoundTripper { return http.DefaultTransport }
	connections.RegisterProviderRefPruner(PruneConnectionRefs)
}
