package providers

import (
	"fmt"
	"net/url"
	"sort"

	"routenest/internal/domain"
	"routenest/internal/providerreg"
	"routenest/internal/zen"
)

func redactedEndpoint(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u.String()
}

func definitionCapabilities(p providerreg.Preset) map[string]domain.CapabilityState {
	out := make(map[string]domain.CapabilityState, len(providerreg.CapabilityNames()))
	for _, capability := range providerreg.CapabilityNames() {
		out[capability] = domain.CapabilityUnknown
	}
	for capability, state := range p.Capabilities {
		out[capability] = state
	}
	return out
}

func builtInInstance(reg *providerreg.Registry, id string, enabled bool, baseURL string) (domain.ProviderInstance, error) {
	definition, ok := reg.Get(id)
	if !ok {
		return domain.ProviderInstance{}, fmt.Errorf("built-in provider definition %q is missing", id)
	}
	if baseURL == "" {
		baseURL = definition.BaseURL
	}
	return domain.ProviderInstance{
		ID:             domain.ProviderInstanceID(id),
		DefinitionID:   domain.ProviderDefinitionID(id),
		AdapterID:      reg.AdapterFor(id),
		Name:           definition.Name,
		BaseURL:        redactedEndpoint(baseURL),
		Enabled:        enabled,
		RuntimeBuiltin: true,
		Deletable:      false,
		FreeTier:       definition.FreeTier,
		Kind:           definition.Kind,
		Capabilities:   definitionCapabilities(definition),
	}, nil
}

func definitionForRuntime(reg *providerreg.Registry, baseURL string) (providerreg.Preset, bool) {
	ids := reg.ByBaseURL(baseURL)
	if len(ids) == 0 {
		return providerreg.Preset{}, false
	}
	definition, ok := reg.Get(ids[0])
	return definition, ok
}

func customInstance(reg *providerreg.Registry, p domain.CustomProvider) domain.ProviderInstance {
	definition, known := definitionForRuntime(reg, p.BaseURL)
	adapterID := "openaicompat"
	definitionID := domain.ProviderDefinitionID("")
	capabilities := definitionCapabilities(providerreg.Preset{})
	kind := "other"
	freeTier := p.Free
	if known {
		definitionID = domain.ProviderDefinitionID(definition.ID)
		adapterID = reg.AdapterFor(definition.ID)
		capabilities = definitionCapabilities(definition)
		kind = definition.Kind
		freeTier = definition.FreeTier || p.Free
	}
	return domain.ProviderInstance{
		ID:           domain.ProviderInstanceID(p.ID),
		DefinitionID: definitionID,
		AdapterID:    adapterID,
		Name:         p.Name,
		BaseURL:      redactedEndpoint(p.BaseURL),
		Enabled:      p.Enabled,
		Deletable:    true,
		FreeTier:     freeTier,
		Kind:         kind,
		Priority:     p.Priority,
		Weight:       p.Weight,
		TimeoutSec:   p.TimeoutSec,
		Capabilities: capabilities,
	}
}

// SnapshotInstances returns every routable upstream as a deterministic,
// redacted, independently mutable projection. Existing stores remain unchanged.
func SnapshotInstances() ([]domain.ProviderInstance, error) {
	Load()
	Mu.Lock()
	custom := make([]domain.CustomProvider, 0, len(CustomProviders))
	for _, p := range CustomProviders {
		if p != nil {
			custom = append(custom, *p)
		}
	}
	Mu.Unlock()

	reg := providerreg.Builtin()
	zenConfig := zen.GetConfig()
	cline, err := builtInInstance(reg, "cline", true, "")
	if err != nil {
		return nil, err
	}
	openCode, err := builtInInstance(reg, "opencode", zenConfig.Enabled, zenConfig.BaseURL)
	if err != nil {
		return nil, err
	}

	out := []domain.ProviderInstance{cline, openCode}
	seen := map[domain.ProviderInstanceID]bool{cline.ID: true, openCode.ID: true}
	for _, p := range custom {
		id := domain.ProviderInstanceID(p.ID)
		if seen[id] {
			return nil, fmt.Errorf("custom provider id %q collides with a built-in provider instance", p.ID)
		}
		seen[id] = true
		out = append(out, customInstance(reg, p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
