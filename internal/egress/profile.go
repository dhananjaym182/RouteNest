package egress

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"routenest/internal/transport"
)

// Strategy is the endpoint selection policy for a profile.
type Strategy string

const (
	StrategyRoundRobin Strategy = "round_robin"
	StrategyRandom     Strategy = "random"
	StrategyFill       Strategy = "fill"
)

// NormalizeStrategy restores an unknown or empty policy to round_robin.
func NormalizeStrategy(strategy string) Strategy {
	switch Strategy(strings.TrimSpace(strategy)) {
	case StrategyRandom:
		return StrategyRandom
	case StrategyFill:
		return StrategyFill
	default:
		return StrategyRoundRobin
	}
}

// Profile is a named proxy pool. Proxies never contain credentials in their
// redacted projection; the live values remain available to transports.
type Profile struct {
	ID       string   `json:"id"`
	Name     string   `json:"name,omitempty"`
	Proxies  []string `json:"proxies,omitempty"`
	Strategy Strategy `json:"strategy"`
}

// Normalize validates the profile and returns an independent copy.
func (p Profile) Normalize() (Profile, error) {
	p.ID = strings.TrimSpace(p.ID)
	if p.ID == "" {
		return Profile{}, fmt.Errorf("egress: profile id is required")
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		p.Name = p.ID
	}
	p.Strategy = NormalizeStrategy(string(p.Strategy))

	proxies := make([]string, 0, len(p.Proxies))
	seen := map[string]bool{}
	for _, raw := range p.Proxies {
		line := strings.TrimSpace(raw)
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		proxies = append(proxies, line)
	}
	if err := transport.ValidateProxyList(proxies); err != nil {
		return Profile{}, fmt.Errorf("egress: profile %q: %w", p.ID, err)
	}
	p.Proxies = proxies
	return p, nil
}

// Clone returns a copy whose slice cannot alias the original.
func (p Profile) Clone() Profile {
	out := p
	out.Proxies = append([]string(nil), p.Proxies...)
	return out
}

// Redacted replaces proxy credentials, queries, and fragments with safe text.
func (p Profile) Redacted() Profile {
	out := p.Clone()
	for i, raw := range out.Proxies {
		out.Proxies[i] = RedactProxyURL(raw)
	}
	return out
}

// RedactProxyURL keeps the endpoint identity (scheme, host, port, path) while
// removing user info, query, and fragment so snapshots cannot leak secrets.
func RedactProxyURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "[invalid proxy]"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String()
}

// EndpointID is a stable, non-secret key for one endpoint in a profile.
func EndpointID(profileID string, index int) string {
	return fmt.Sprintf("%s:%d", profileID, index)
}

func sortProfiles(profiles []Profile) {
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].ID < profiles[j].ID })
}
