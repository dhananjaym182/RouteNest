package providerreg

import (
	"fmt"
	"net/url"
	"strings"

	"routenest/internal/deviceflow"
)

var builtinKinds = map[string]bool{"": true, "free": true, "freeTier": true, "hasFree": true, "local": true, "other": true}

var knownProtocols = map[string]bool{
	"":              true,
	DefaultProtocol: true,
	"qoder":         true,
	"cline":         true,
	"opencode-zen":  true,
}

var knownAdapters = map[string]bool{
	"openaicompat": true,
	"qoder":        true,
	"cline":        true,
	"opencodezen":  true,
}

var knownCapabilities = map[string]bool{
	CapabilityModelDiscovery:        true,
	CapabilityFreeModelDetection:    true,
	CapabilityRemovedModelDetection: true,
	CapabilityReactiveQuota:         true,
	CapabilityProactiveQuota:        true,
	CapabilityQuotaResetDetection:   true,
	CapabilityCredentialRotation:    true,
	CapabilityEgressPool:            true,
	CapabilityContextCompaction:     true,
}

// builtInCredentialFlows are executable built-in adapter capabilities that do
// not belong to the legacy provider-connection deviceflow registry.
var builtInCredentialFlows = map[string]bool{
	"cline-workos":        true,
	"cline-refresh-token": true,
	"opencode-public":     true,
	"opencode-session":    true,
}

// knownFetcherTypes lists catalogue fetcher implementations that exist in the
// codebase. Providers with a specialized fetcher still speak OpenAI-compatible
// chat; only their catalogue format differs.
var knownFetcherTypes = map[string]bool{
	"":              true,
	"openai":        true,
	"agentrouter":   true,
	"bynara":        true,
	"hcnsec":        true,
	"inxora":        true,
	"mimo-free":     true,
	"opencode-free": true,
	"qoder":         true,
}

func knownAuthFlow(flow string, legacy map[string]deviceflow.Spec) bool {
	if builtInCredentialFlows[flow] {
		return true
	}
	_, ok := legacy[flow]
	return ok
}

// Validate checks every definition and its cross-references. The map key must
// equal the definition ID. Legacy AuthFlow IDs must name a device-flow spec;
// AuthFlows may additionally name built-in adapter credential flows.
func Validate(presets map[string]Preset) error {
	flows := deviceflow.Specs()
	seenIDs := make(map[string]string, len(presets))
	for key, p := range presets {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("preset with empty key")
		}
		if p.ID != "" && p.ID != key {
			return fmt.Errorf("preset %q: id %q does not match key", key, p.ID)
		}
		if prev, dup := seenIDs[p.ID]; dup {
			return fmt.Errorf("preset id %q duplicated (%s and %s)", p.ID, prev, key)
		}
		seenIDs[p.ID] = key
		if strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("preset %q: name is required", key)
		}
		u, err := url.Parse(p.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("preset %q: baseURL %q must be an absolute http(s) URL", key, p.BaseURL)
		}
		if !builtinKinds[p.Kind] {
			return fmt.Errorf("preset %q: unknown kind %q", key, p.Kind)
		}
		if p.Kind == "free" && !p.FreeTier {
			return fmt.Errorf("preset %q: kind free requires freeTier", key)
		}
		if !knownProtocols[p.Protocol] {
			return fmt.Errorf("preset %q: unsupported protocol %q", key, p.Protocol)
		}
		if adapter := effectiveAdapter(p); !knownAdapters[adapter] {
			return fmt.Errorf("preset %q: unsupported adapter %q", key, adapter)
		}
		if p.AuthFlow != "" {
			if _, ok := flows[p.AuthFlow]; !ok {
				return fmt.Errorf("preset %q: authFlow %q has no device-flow spec", key, p.AuthFlow)
			}
		}
		seenFlows := map[string]bool{}
		for _, flow := range p.AuthFlows {
			if strings.TrimSpace(flow) == "" {
				return fmt.Errorf("preset %q: empty auth flow", key)
			}
			if seenFlows[flow] {
				return fmt.Errorf("preset %q: duplicate auth flow %q", key, flow)
			}
			seenFlows[flow] = true
			if !knownAuthFlow(flow, flows) {
				return fmt.Errorf("preset %q: unknown auth flow %q", key, flow)
			}
		}
		if p.LoginFlow != (p.AuthFlow != "") {
			return fmt.Errorf("preset %q: loginFlow must be true exactly when authFlow is set", key)
		}
		if p.Provenance != "" && p.Provenance != ProvenanceManual && p.Provenance != ProvenanceImported {
			return fmt.Errorf("preset %q: unknown provenance %q", key, p.Provenance)
		}
		for capability, state := range p.Capabilities {
			if !knownCapabilities[capability] {
				return fmt.Errorf("preset %q: unknown capability %q", key, capability)
			}
			if !state.Valid() {
				return fmt.Errorf("preset %q: invalid capability state %q for %s", key, state, capability)
			}
		}
		if p.MissingThreshold < 0 || p.MissingThreshold > 100 {
			return fmt.Errorf("preset %q: missingThreshold %d must be between 0 and 100", key, p.MissingThreshold)
		}
		if p.ModelsFetcher != nil {
			fu, err := url.Parse(p.ModelsFetcher.URL)
			if err != nil || (fu.Scheme != "http" && fu.Scheme != "https") || fu.Host == "" {
				return fmt.Errorf("preset %q: modelsFetcher URL %q must be absolute", key, p.ModelsFetcher.URL)
			}
			if !knownFetcherTypes[p.ModelsFetcher.Type] {
				return fmt.Errorf("preset %q: unsupported modelsFetcher type %q", key, p.ModelsFetcher.Type)
			}
		}
		seenModels := make(map[string]bool, len(p.ModelIDs))
		for _, id := range p.ModelIDs {
			if strings.TrimSpace(id) == "" {
				return fmt.Errorf("preset %q: empty model id", key)
			}
			if seenModels[id] {
				return fmt.Errorf("preset %q: duplicate model id %q", key, id)
			}
			seenModels[id] = true
		}
	}
	return nil
}
