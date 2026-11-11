package providers

import (
	"fmt"
	"strings"
	"unicode"
)

// modelMapMaxLen bounds one upstream model ID. Real IDs are far shorter; the cap
// only stops a malformed save from persisting an unbounded string.
const modelMapMaxLen = 256

// UpstreamModelFor returns the model ID this provider's upstream expects for a
// client-visible name from ModelIDs. Every dispatch path (legacy/shadow
// providers.Call and the planner's offering projection) goes through this one
// lookup, so they cannot disagree about what is sent upstream.
func UpstreamModelFor(p CustomProvider, name string) string {
	if upstream := strings.TrimSpace(p.ModelMap[name]); upstream != "" {
		return upstream
	}
	return name
}

// SanitizeModelMap validates a saved mapping against the declared models.
// Entries for undeclared names and entries that map a name to itself are
// dropped silently (they carry no information). An upstream ID containing a
// control character, or longer than modelMapMaxLen, is rejected so the
// administrator sees the problem instead of a silently altered mapping.
func SanitizeModelMap(modelIDs []string, in map[string]string) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	declared := make(map[string]bool, len(modelIDs))
	for _, id := range modelIDs {
		declared[id] = true
	}
	out := make(map[string]string, len(in))
	for name, upstream := range in {
		name, upstream = strings.TrimSpace(name), strings.TrimSpace(upstream)
		if upstream == "" || upstream == name || !declared[name] {
			continue
		}
		if len(upstream) > modelMapMaxLen {
			return nil, fmt.Errorf("upstream model ID for %q is too long (max %d characters)", name, modelMapMaxLen)
		}
		for _, r := range upstream {
			if unicode.IsControl(r) {
				return nil, fmt.Errorf("upstream model ID for %q contains a control character", name)
			}
		}
		out[name] = upstream
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// upstreamPresence reports, for every declared name, whether the discovered
// catalogue contains the ID actually sent upstream. recheck compares the
// catalogue with this view so a mapped friendly name is not delisted merely
// because the upstream only knows its mapped ID.
func upstreamPresence(p CustomProvider, discovered map[string]bool) map[string]bool {
	if len(discovered) == 0 {
		// An empty catalogue is non-authoritative; keep it empty so the
		// reconciler still refuses to count misses against it.
		return discovered
	}
	out := make(map[string]bool, len(discovered)+len(p.ModelMap))
	for id, present := range discovered {
		out[id] = present
	}
	for name := range p.ModelMap {
		out[name] = discovered[UpstreamModelFor(p, name)]
	}
	return out
}

// mappedUpstreamIDs lists the upstream IDs a provider reaches through ModelMap,
// so discovery does not offer them again as new models.
func mappedUpstreamIDs(p CustomProvider) map[string]bool {
	out := make(map[string]bool, len(p.ModelMap))
	for name := range p.ModelMap {
		out[UpstreamModelFor(p, name)] = true
	}
	return out
}
