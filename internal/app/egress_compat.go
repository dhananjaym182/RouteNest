package app

import (
	"routenest/internal/egress"
	"routenest/internal/providerstate"
	"routenest/internal/zen"
)

// clineGlobalSource projects the legacy global Cline pool as the default egress
// profile. The persisted file stays owned by internal/egress.
type clineGlobalSource struct{}

func (clineGlobalSource) ProfileID() string { return "global" }

func (clineGlobalSource) Snapshot() egress.Profile {
	proxies, strategy := egress.GetClineProxyConfig().Snapshot()
	return egress.Profile{ID: "global", Name: "Global", Proxies: proxies, Strategy: egress.Strategy(strategy)}
}

// zenSource projects the OpenCode Zen pool as a named profile.
type zenSource struct{}

func (zenSource) ProfileID() string { return "zen" }

func (zenSource) Snapshot() egress.Profile {
	cfg := zen.GetConfig()
	return egress.Profile{ID: "zen", Name: "OpenCode Zen", Proxies: cfg.Proxies, Strategy: egress.Strategy(cfg.ProxyStrategy)}
}

func configureEgress() error {
	registry, err := egress.NewRegistry("global", clineGlobalSource{}, zenSource{})
	if err != nil {
		return err
	}
	resolver, err := egress.NewResolver(registry, providerstate.Default())
	if err != nil {
		return err
	}
	return egress.Publish(resolver)
}
