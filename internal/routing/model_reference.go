package routing

import (
	"fmt"
	"strings"

	"routenest/internal/config"
	"routenest/internal/domain"
	"routenest/internal/models"
	"routenest/internal/providers"
)

// ResolveModelReference separates a client model reference from its upstream
// ID. Compact namespaces carry a stable provider identity; their entire tail
// is an upstream ID, including slashes and literal @ characters.
func ResolveModelReference(raw string) (string, CandidatePin, error) {
	raw = strings.TrimSpace(raw)
	cfg := modelReferenceConfig()
	if modelID, providerID, qualified := cfg.SplitProviderModel(raw); qualified {
		if strings.TrimSpace(modelID) == "" {
			return "", CandidatePin{}, fmt.Errorf("provider model reference %q requires a model ID", raw)
		}
		return modelID, CandidatePin{ProviderID: domain.ProviderInstanceID(providerID)}, nil
	}
	if !strings.Contains(raw, "@") && !strings.HasPrefix(raw, "opencode/") {
		return raw, CandidatePin{}, nil
	}
	instances, err := providers.SnapshotInstances()
	if err != nil {
		return "", CandidatePin{}, err
	}
	offerings, err := ModelOfferings()
	if err != nil {
		return "", CandidatePin{}, err
	}
	modelID, pin, err := resolveModelReference(raw, cfg, instances, offerings)
	if err != nil {
		_, name := providers.SplitPin(raw)
		matches := 0
		for _, instance := range instances {
			if name != "" && strings.EqualFold(strings.TrimSpace(instance.Name), name) {
				matches++
			}
		}
		// Legacy execution supports an explicit display-name redundancy group.
		// The snapshot planner still rejects ambiguity; compact and stable-ID
		// references never acquire display-name group semantics.
		if matches > 1 {
			return modelID, CandidatePin{ProviderName: name}, nil
		}
	}
	return modelID, pin, err
}

func resolveModelReference(raw string, cfg *config.Proxy, instances []domain.ProviderInstance, offerings []domain.ModelOffering) (string, CandidatePin, error) {
	raw = strings.TrimSpace(raw)
	if modelID, providerID, qualified := cfg.SplitProviderModel(raw); qualified {
		if strings.TrimSpace(modelID) == "" {
			return "", CandidatePin{}, fmt.Errorf("provider model reference %q requires a model ID", raw)
		}
		return modelID, CandidatePin{ProviderID: domain.ProviderInstanceID(providerID)}, nil
	}
	// An exact upstream ID wins over the legacy @ syntax. Compact namespaces
	// remain unambiguous even when an upstream ID ends in a provider's name.
	for _, offering := range offerings {
		if offering.ModelID == raw {
			return raw, CandidatePin{}, nil
		}
	}
	if modelID, legacy := strings.CutPrefix(raw, "opencode/"); legacy {
		return modelID, CandidatePin{}, nil
	}
	modelID, rawPin := providers.SplitPin(raw)
	if rawPin == "" {
		return raw, CandidatePin{}, nil
	}
	// Reserved identities stay IDs after deletion, so a new display name
	// cannot capture an old pin.
	for _, reservedID := range cfg.ProviderModelPrefixes {
		if reservedID == rawPin {
			return modelID, CandidatePin{ProviderID: domain.ProviderInstanceID(rawPin)}, nil
		}
	}
	pin, err := resolveCandidatePin(instances, rawPin)
	return modelID, pin, err
}

// ResolveComboEntry converts a qualified reference into the existing raw model
// plus source/provider fields. An explicit, conflicting source is rejected.
// Already pinned raw IDs are never interpreted as legacy @ references.
func ResolveComboEntry(entry domain.ComboEntry) (domain.ComboEntry, error) {
	entry.Model = strings.TrimSpace(entry.Model)
	entry.Source = strings.TrimSpace(entry.Source)
	entry.Provider = strings.TrimSpace(entry.Provider)
	modelID, providerID, qualified := modelReferenceConfig().SplitProviderModel(entry.Model)
	var pin CandidatePin
	if qualified {
		if strings.TrimSpace(modelID) == "" {
			return entry, fmt.Errorf("provider model reference %q requires a model ID", entry.Model)
		}
		pin.ProviderID = domain.ProviderInstanceID(providerID)
	} else if entry.Source == "" {
		var err error
		modelID, pin, err = ResolveModelReference(entry.Model)
		if err != nil {
			return entry, err
		}
	} else {
		return entry, nil
	}
	if !pin.active() {
		return entry, nil
	}
	wantedSource := "provider"
	switch pin.ProviderID {
	case "cline":
		wantedSource = "cline"
	case "opencode":
		wantedSource = "zen"
	}
	if entry.Source != "" && entry.Source != wantedSource {
		return entry, fmt.Errorf("model reference %q conflicts with source %q", entry.Model, entry.Source)
	}
	if wantedSource == "provider" && entry.Provider != "" && entry.Provider != string(pin.ProviderID) {
		instances, err := providers.SnapshotInstances()
		if err != nil {
			return entry, err
		}
		explicit, err := resolveCandidatePin(instances, entry.Provider)
		if err != nil || explicit.ProviderID != pin.ProviderID {
			return entry, fmt.Errorf("model reference %q conflicts with provider %q", entry.Model, entry.Provider)
		}
	}
	entry.Model = modelID
	entry.Source = wantedSource
	entry.Provider = ""
	if wantedSource == "provider" {
		entry.Provider = string(pin.ProviderID)
		if entry.Provider == "" {
			entry.Provider = pin.ProviderName
		}
	}
	return entry, nil
}

// ComboEntryModelReference retains a source pin when resolving policy and
// capability metadata. It is a client reference, never an upstream model ID.
func ComboEntryModelReference(entry domain.ComboEntry) string {
	providerID := entry.Provider
	switch entry.Source {
	case "cline":
		providerID = "cline"
	case "zen":
		providerID = "opencode"
	case "provider":
	default:
		return entry.Model
	}
	if prefix := modelReferenceConfig().ProviderModelPrefix(providerID); prefix != "" {
		return prefix + "/" + entry.Model
	}
	if providerID != "" {
		return entry.Model + "@" + providerID
	}
	return entry.Model
}

// Reference normalization can also be used by independently configured combo
// and compaction modules before the full application composition is installed.
func modelReferenceConfig() *config.Proxy {
	if ConfigProvider != nil {
		return ConfigProvider()
	}
	return config.Live()
}

// ModelForReference resolves capability metadata from the selected offering.
// It never borrows the first same-name model from another provider.
func ModelForReference(raw string) (domain.Model, bool) {
	modelID, pin, err := ResolveModelReference(raw)
	if err != nil {
		return domain.Model{}, false
	}
	if !pin.active() {
		for _, model := range ModelsProvider() {
			if model.ID == modelID {
				return model, true
			}
		}
		return domain.Model{}, false
	}
	offerings, err := ModelOfferings()
	if err != nil {
		return domain.Model{}, false
	}
	for _, offering := range offerings {
		if offering.ModelID == modelID && offering.ProviderID == pin.ProviderID {
			return modelFromOffering(offering), true
		}
	}
	return domain.Model{}, false
}

func modelFromOffering(offering domain.ModelOffering) domain.Model {
	source := models.SourceProvider
	switch offering.ProviderID {
	case "cline":
		source = models.SourceRemote
	case "opencode":
		source = models.SourceZen
	}
	cost := models.CostPass
	if offering.FreeOnlyEligible() {
		cost = models.CostFree
	} else if offering.Access == domain.AccessUnknown {
		cost = ""
	}
	return domain.Model{
		ID: offering.ModelID, Source: source, Cost: cost, Status: offering.Status,
		Disabled: offering.Disabled, Delisted: offering.Delisted, Custom: offering.Custom,
		Context: offering.Context, Output: offering.Output, MetaLocked: offering.MetaLocked,
		TierSignal: offering.TierSignal, TierDecidedAt: offering.TierDecidedAt,
		ThinkingLevels:   append([]string(nil), offering.ThinkingLevels...),
		InputModalities:  append([]string(nil), offering.InputModalities...),
		OutputModalities: append([]string(nil), offering.OutputModalities...),
	}
}
