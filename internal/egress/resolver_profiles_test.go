package egress

import (
	"strings"
	"testing"
	"time"

	"routenest/internal/providerstate"
)

type staticProfileSource struct {
	id      string
	profile Profile
}

func (s staticProfileSource) ProfileID() string { return s.id }

func (s staticProfileSource) Snapshot() Profile { return s.profile }

func TestResolverProfilesRedactProxyCredentials(t *testing.T) {
	registry, err := NewRegistry("pool", staticProfileSource{
		id: "pool",
		profile: Profile{
			ID:      "pool",
			Name:    "Pool",
			Proxies: []string{"http://user:secret@proxy.example:8080/edge?token=query#fragment"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewResolver(registry, providerstate.NewStore(time.Now))
	if err != nil {
		t.Fatal(err)
	}

	if got := resolver.GlobalID(); got != "pool" {
		t.Fatalf("global id = %q", got)
	}
	profiles := resolver.Profiles()
	if len(profiles) != 1 {
		t.Fatalf("profiles = %+v", profiles)
	}
	proxy := profiles[0].Proxies[0]
	if strings.Contains(proxy, "secret") || strings.Contains(proxy, "token=query") || strings.Contains(proxy, "fragment") {
		t.Fatalf("profile leaked secret material: %q", proxy)
	}
	if !strings.Contains(proxy, "proxy.example:8080") || !strings.Contains(proxy, "/edge") {
		t.Fatalf("profile lost endpoint identity: %q", proxy)
	}
}
