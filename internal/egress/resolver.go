package egress

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/http/httpproxy"

	"routenest/internal/domain"
	"routenest/internal/providerstate"
	"routenest/internal/transport"
)

// Source projects a legacy owner's pool into a profile without taking over its
// persisted file. Snapshot is read on demand so configuration changes are seen.
type Source interface {
	ProfileID() string
	Snapshot() Profile
}

// Registry holds immutable profile definitions. Sources may be live views, but
// every value returned by the registry is an independent copy.
type Registry struct {
	mu       sync.RWMutex
	globalID string
	sources  []Source
}

func NewRegistry(globalID string, sources ...Source) (*Registry, error) {
	globalID = strings.TrimSpace(globalID)
	if globalID == "" {
		return nil, fmt.Errorf("egress: global profile id is required")
	}
	seen := map[string]bool{}
	for _, source := range sources {
		if source == nil {
			return nil, fmt.Errorf("egress: profile source is nil")
		}
		id := strings.TrimSpace(source.ProfileID())
		if id == "" {
			return nil, fmt.Errorf("egress: profile source id is required")
		}
		if seen[id] {
			return nil, fmt.Errorf("egress: duplicate profile %q", id)
		}
		seen[id] = true
	}
	if !seen[globalID] {
		return nil, fmt.Errorf("egress: global profile %q has no source", globalID)
	}
	return &Registry{globalID: globalID, sources: append([]Source(nil), sources...)}, nil
}

func (r *Registry) source(id string) (Source, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, source := range r.sources {
		if source.ProfileID() == id {
			return source, true
		}
	}
	return nil, false
}

// Get returns a normalized, independent profile or false when it is unknown or
// invalid. An invalid source is skipped rather than poisoning other profiles.
func (r *Registry) Get(id string) (Profile, bool) {
	source, ok := r.source(id)
	if !ok {
		return Profile{}, false
	}
	profile, err := source.Snapshot().Normalize()
	if err != nil || profile.ID != id {
		return Profile{}, false
	}
	return profile, true
}

func (r *Registry) Global() (Profile, bool) {
	if r == nil {
		return Profile{}, false
	}
	return r.Get(r.globalID)
}

func (r *Registry) GlobalID() string {
	if r == nil {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.globalID
}

// Profiles returns normalized, redacted, deterministically ordered copies.
func (r *Registry) Profiles() []Profile {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	sources := append([]Source(nil), r.sources...)
	r.mu.RUnlock()
	out := make([]Profile, 0, len(sources))
	for _, source := range sources {
		profile, err := source.Snapshot().Normalize()
		if err != nil {
			continue
		}
		out = append(out, profile.Redacted())
	}
	sortProfiles(out)
	return out
}

// Mode is the transport decision for one request.
type Mode string

const (
	ModeDirect      Mode = "direct"
	ModeProfile     Mode = "profile"
	ModeEnvironment Mode = "environment"
)

// Override is the provider-instance compatibility input, projected by the
// composition root from legacy fields.
type Override struct {
	Direct    bool
	ProfileID string
	ProxyURL  string
}

// Decision is a resolved egress instruction. Proxy is nil unless ModeProfile or
// ModeEnvironment selected an endpoint.
type Decision struct {
	Mode      Mode
	Source    string
	ProfileID string
	Proxy     *url.URL
	Endpoint  string
	Cooling   bool
}

// Resolver applies provider override -> global profile -> environment -> direct.
type Resolver struct {
	registry *Registry
	store    *providerstate.Store
	envProxy func(*url.URL) (*url.URL, error)

	mu       sync.Mutex
	counters map[string]*atomic.Uint64
}

func NewResolver(registry *Registry, store *providerstate.Store) (*Resolver, error) {
	if registry == nil {
		return nil, fmt.Errorf("egress: registry is required")
	}
	return &Resolver{
		registry: registry,
		store:    store,
		envProxy: httpproxy.FromEnvironment().ProxyFunc(),
		counters: map[string]*atomic.Uint64{},
	}, nil
}

// SetEnvironmentProxy replaces the environment lookup seam (tests, platform).
func (r *Resolver) SetEnvironmentProxy(fn func(*url.URL) (*url.URL, error)) {
	if fn == nil {
		fn = httpproxy.FromEnvironment().ProxyFunc()
	}
	r.envProxy = fn
}

func (r *Resolver) counter(profileID string) *atomic.Uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	counter := r.counters[profileID]
	if counter == nil {
		counter = &atomic.Uint64{}
		r.counters[profileID] = counter
	}
	return counter
}

// DirectTarget reports whether an address must bypass proxies: loopback,
// private, and link-local hosts are local services, not public upstreams.
func DirectTarget(host string) bool {
	host = strings.TrimSpace(host)
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
	}
	return false
}

func (r *Resolver) endpointCooling(providerID domain.ProviderInstanceID, endpoint string) (bool, time.Time) {
	if r.store == nil || providerID == "" {
		return false, time.Time{}
	}
	scope := domain.AvailabilityScope{ProviderID: providerID, EgressID: endpoint}
	for _, observation := range r.store.Blocking(scope) {
		if until := observation.Until; !until.IsZero() {
			return true, until
		}
		if reset := observation.ResetAt; !reset.IsZero() {
			return true, reset
		}
		return true, time.Time{}
	}
	return false, time.Time{}
}

// selectEndpoint picks a deterministic endpoint, preferring one whose cooldown
// has expired. When every endpoint is cooling, the policy's first candidate is
// returned and marked Cooling, preserving legacy fail-and-retry semantics.
func (r *Resolver) selectEndpoint(providerID domain.ProviderInstanceID, profile Profile) (*url.URL, string, bool) {
	n := len(profile.Proxies)
	if n == 0 {
		return nil, "", false
	}
	start := 0
	switch profile.Strategy {
	case StrategyFill:
		start = 0
	case StrategyRandom:
		start = int(randomIntn(n))
	default:
		start = int(r.counter(profile.ID).Add(1)-1) % n
	}

	fallback := -1
	for offset := 0; offset < n; offset++ {
		index := (start + offset) % n
		raw := profile.Proxies[index]
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			continue
		}
		cooling, _ := r.endpointCooling(providerID, EndpointID(profile.ID, index))
		if cooling {
			if fallback < 0 {
				fallback = index
			}
			continue
		}
		return parsed, EndpointID(profile.ID, index), false
	}
	if fallback >= 0 {
		parsed, _ := url.Parse(profile.Proxies[fallback])
		return parsed, EndpointID(profile.ID, fallback), true
	}
	return nil, "", false
}

func (r *Resolver) fromProfile(providerID domain.ProviderInstanceID, profile Profile, source string, target *url.URL) (Decision, bool) {
	if len(profile.Proxies) == 0 {
		return Decision{}, false
	}
	if target != nil && DirectTarget(target.Hostname()) {
		return Decision{Mode: ModeDirect, Source: "private-target"}, true
	}
	proxy, endpoint, cooling := r.selectEndpoint(providerID, profile)
	if proxy == nil {
		return Decision{}, false
	}
	return Decision{
		Mode: ModeProfile, Source: source, ProfileID: profile.ID,
		Proxy: proxy, Endpoint: endpoint, Cooling: cooling,
	}, true
}

// Resolve returns the egress decision for one request. It performs no I/O.
func (r *Resolver) Resolve(providerID domain.ProviderInstanceID, override Override, target *url.URL) (Decision, error) {
	if r == nil || r.registry == nil {
		return Decision{}, fmt.Errorf("egress: resolver is not configured")
	}
	if override.Direct {
		return Decision{Mode: ModeDirect, Source: "provider-direct"}, nil
	}
	if strings.TrimSpace(override.ProxyURL) != "" {
		spec, err := transport.NormalizeProxySpec(override.ProxyURL)
		if err != nil {
			return Decision{}, fmt.Errorf("egress: provider %q override: %w", providerID, err)
		}
		if spec == "direct" {
			return Decision{Mode: ModeDirect, Source: "provider-direct"}, nil
		}
		parsed, err := url.Parse(spec)
		if err != nil {
			return Decision{}, fmt.Errorf("egress: provider %q override: %w", providerID, err)
		}
		// An explicit compatibility override is pinned exactly as configured,
		// including loopback targets; only pools apply the private-target rule.
		return Decision{Mode: ModeProfile, Source: "provider-override", Proxy: parsed, Endpoint: "override"}, nil
	}
	if id := strings.TrimSpace(override.ProfileID); id != "" {
		profile, ok := r.registry.Get(id)
		if !ok {
			return Decision{}, fmt.Errorf("egress: provider %q references unknown profile %q", providerID, id)
		}
		if decision, ok := r.fromProfile(providerID, profile, "provider-profile", target); ok {
			return decision, nil
		}
	}
	if global, ok := r.registry.Global(); ok {
		if decision, ok := r.fromProfile(providerID, global, "global-profile", target); ok {
			return decision, nil
		}
	}
	if r.envProxy != nil && target != nil {
		if proxy, err := r.envProxy(target); err == nil && proxy != nil {
			return Decision{Mode: ModeEnvironment, Source: "environment", Proxy: proxy}, nil
		}
	}
	return Decision{Mode: ModeDirect, Source: "fallback-direct"}, nil
}

// Publish installs the process-wide resolver used by compatibility adapters.
var defaultResolver atomic.Pointer[Resolver]

func Publish(resolver *Resolver) error {
	if resolver == nil {
		return fmt.Errorf("egress: default resolver is nil")
	}
	defaultResolver.Store(resolver)
	return nil
}

func Default() *Resolver {
	resolver := defaultResolver.Load()
	if resolver == nil {
		panic("egress: app.Configure() was not called (resolver unset)")
	}
	return resolver
}

func HasDefault() bool { return defaultResolver.Load() != nil }
