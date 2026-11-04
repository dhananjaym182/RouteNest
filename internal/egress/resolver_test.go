package egress

import (
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"routenest/internal/domain"
	"routenest/internal/providerstate"
)

func testTarget(t *testing.T, raw string) *url.URL {
	t.Helper()
	target, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func newTestResolver(t *testing.T, store *providerstate.Store, global Profile, extras ...Source) *Resolver {
	t.Helper()
	sources := append([]Source{&mutableSource{id: global.ID, profile: global}}, extras...)
	registry, err := NewRegistry(global.ID, sources...)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewResolver(registry, store)
	if err != nil {
		t.Fatal(err)
	}
	resolver.SetEnvironmentProxy(func(*url.URL) (*url.URL, error) { return nil, nil })
	return resolver
}

func TestResolverPrecedence(t *testing.T) {
	global := Profile{ID: "global", Proxies: []string{"http://global:1"}}
	named := &mutableSource{id: "named", profile: Profile{ID: "named", Proxies: []string{"socks5://named:2"}}}
	resolver := newTestResolver(t, nil, global, named)
	target := testTarget(t, "https://api.example.com/v1")

	decision, err := resolver.Resolve("p", Override{Direct: true, ProfileID: "named", ProxyURL: "http://override:3"}, target)
	if err != nil || decision.Mode != ModeDirect || decision.Source != "provider-direct" {
		t.Fatalf("provider direct = %+v, %v", decision, err)
	}

	decision, err = resolver.Resolve("p", Override{ProxyURL: "http://override:3"}, target)
	if err != nil || decision.Mode != ModeProfile || decision.Source != "provider-override" || decision.Proxy.String() != "http://override:3" {
		t.Fatalf("provider override = %+v, %v", decision, err)
	}

	decision, err = resolver.Resolve("p", Override{ProfileID: "named"}, target)
	if err != nil || decision.ProfileID != "named" || decision.Source != "provider-profile" {
		t.Fatalf("provider profile = %+v, %v", decision, err)
	}

	decision, err = resolver.Resolve("p", Override{}, target)
	if err != nil || decision.ProfileID != "global" || decision.Source != "global-profile" {
		t.Fatalf("global profile = %+v, %v", decision, err)
	}

	if _, err := resolver.Resolve("p", Override{ProfileID: "unknown"}, target); err == nil {
		t.Fatal("unknown profile must error")
	}
	if _, err := resolver.Resolve("p", Override{ProxyURL: "ftp://bad:1"}, target); err == nil {
		t.Fatal("invalid override must error")
	}
}

func TestResolverFallsBackToEnvironmentThenDirect(t *testing.T) {
	empty := Profile{ID: "global"}
	resolver := newTestResolver(t, nil, empty)
	target := testTarget(t, "https://api.example.com")
	envURL, _ := url.Parse("http://env-proxy:8080")
	resolver.SetEnvironmentProxy(func(*url.URL) (*url.URL, error) { return envURL, nil })

	decision, err := resolver.Resolve("p", Override{}, target)
	if err != nil || decision.Mode != ModeEnvironment || decision.Proxy.String() != envURL.String() {
		t.Fatalf("environment = %+v, %v", decision, err)
	}

	resolver.SetEnvironmentProxy(func(*url.URL) (*url.URL, error) { return nil, nil })
	decision, err = resolver.Resolve("p", Override{}, target)
	if err != nil || decision.Mode != ModeDirect || decision.Source != "fallback-direct" {
		t.Fatalf("direct fallback = %+v, %v", decision, err)
	}
}

func TestResolverBypassesPrivateTargets(t *testing.T) {
	global := Profile{ID: "global", Proxies: []string{"http://global:1"}}
	resolver := newTestResolver(t, nil, global)
	for _, target := range []string{
		"http://localhost:11434/v1",
		"http://127.0.0.1:8080",
		"http://192.168.1.5:8000",
		"http://[::1]:9000",
	} {
		decision, err := resolver.Resolve("p", Override{}, testTarget(t, target))
		if err != nil || decision.Mode != ModeDirect || decision.Source != "private-target" {
			t.Fatalf("%s decision = %+v, %v", target, decision, err)
		}
	}
	// An explicit compatibility override is pinned even for local targets.
	decision, err := resolver.Resolve("p", Override{ProxyURL: "http://override:1"}, testTarget(t, "http://127.0.0.1:8080"))
	if err != nil || decision.Mode != ModeProfile || decision.Source != "provider-override" {
		t.Fatalf("explicit override decision = %+v, %v", decision, err)
	}
}

func TestResolverHonorsEndpointCooldown(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	store := providerstate.NewStore(func() time.Time { return now })
	profile := Profile{ID: "global", Proxies: []string{"http://a:1", "http://b:2"}, Strategy: StrategyFill}
	resolver := newTestResolver(t, store, profile)
	target := testTarget(t, "https://api.example.com")

	if err := store.Apply(domain.AvailabilityObservation{
		Kind: domain.ObservationRateLimited,
		Scope: domain.AvailabilityScope{
			ProviderID: "p", EgressID: EndpointID("global", 0),
		},
		Until: now.Add(time.Minute), Source: "test", Confidence: domain.ConfidenceExplicit,
	}); err != nil {
		t.Fatal(err)
	}

	decision, err := resolver.Resolve("p", Override{}, target)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Endpoint != "global:1" || decision.Proxy.String() != "http://b:2" || decision.Cooling {
		t.Fatalf("cooling endpoint selected: %+v", decision)
	}

	if err := store.Apply(domain.AvailabilityObservation{
		Kind: domain.ObservationRateLimited,
		Scope: domain.AvailabilityScope{
			ProviderID: "p", EgressID: EndpointID("global", 1),
		},
		Until: now.Add(time.Minute), Source: "test", Confidence: domain.ConfidenceExplicit,
	}); err != nil {
		t.Fatal(err)
	}
	decision, err = resolver.Resolve("p", Override{}, target)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Cooling || decision.Proxy == nil {
		t.Fatalf("all-cooling decision = %+v", decision)
	}
}

func TestResolverStrategies(t *testing.T) {
	target := testTarget(t, "https://api.example.com")

	fill := newTestResolver(t, nil, Profile{ID: "global", Proxies: []string{"http://a:1", "http://b:2"}, Strategy: StrategyFill})
	for i := 0; i < 3; i++ {
		decision, _ := fill.Resolve("p", Override{}, target)
		if decision.Proxy.String() != "http://a:1" {
			t.Fatalf("fill picked %s", decision.Proxy)
		}
	}

	roundRobin := newTestResolver(t, nil, Profile{ID: "global", Proxies: []string{"http://a:1", "http://b:2"}})
	first, _ := roundRobin.Resolve("p", Override{}, target)
	second, _ := roundRobin.Resolve("p", Override{}, target)
	if first.Proxy.String() == second.Proxy.String() {
		t.Fatalf("round robin did not rotate: %s then %s", first.Proxy, second.Proxy)
	}
}

func TestResolverConcurrentUse(t *testing.T) {
	store := providerstate.NewStore(time.Now)
	profile := Profile{ID: "global", Proxies: []string{"http://a:1", "http://b:2", "socks5://c:3"}}
	resolver := newTestResolver(t, store, profile)
	target := testTarget(t, "https://api.example.com")
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				decision, err := resolver.Resolve("p", Override{}, target)
				if err != nil || decision.Proxy == nil {
					t.Errorf("resolve = %+v, %v", decision, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestDecisionNeverExposesRedactedProxyToTransport(t *testing.T) {
	// Guard the invariant that redaction belongs to snapshots, not routing.
	global := Profile{ID: "global", Proxies: []string{"http://user:secret@proxy:1"}}
	resolver := newTestResolver(t, nil, global)
	decision, err := resolver.Resolve("p", Override{}, testTarget(t, "https://api.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Proxy == nil || !strings.Contains(decision.Proxy.String(), "secret") {
		t.Fatalf("live decision lost credentials needed for dialing: %+v", decision)
	}
	registry, _ := NewRegistry("global", &mutableSource{id: "global", profile: global})
	if snapshot := registry.Profiles()[0].Proxies[0]; strings.Contains(snapshot, "secret") {
		t.Fatalf("snapshot leaked credentials: %s", snapshot)
	}
}
