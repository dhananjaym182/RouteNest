package routing

import (
	"sort"
	"time"

	"routenest/internal/domain"
	"routenest/internal/models"
	"routenest/internal/providers"
	"routenest/internal/zen"
)

// ModelOfferings includes the same offline built-ins as AllModels, without
// writing seed models into the persisted catalog or merging provider identities.
func ModelOfferings() ([]domain.ModelOffering, error) {
	offerings, err := providers.SnapshotModelOfferings()
	if err != nil {
		return nil, err
	}
	instances, err := providers.SnapshotInstances()
	if err != nil {
		return nil, err
	}
	byProvider := make(map[domain.ProviderInstanceID]domain.ProviderInstance, len(instances))
	for _, instance := range instances {
		byProvider[instance.ID] = instance
	}
	byKey := make(map[domain.OfferingKey]domain.ModelOffering, len(offerings))
	for _, offering := range offerings {
		byKey[offering.Key()] = offering
	}
	for _, model := range ModelsProvider() {
		if model.Source == models.SourceProvider {
			continue
		}
		providerID := domain.ProviderInstanceID("cline")
		if zen.IsSource(model) {
			providerID = "opencode"
			model.Source = models.SourceZen
		} else {
			model.Source = models.SourceRemote
		}
		key := domain.OfferingKey{ProviderID: providerID, ModelID: model.ID}
		if _, exists := byKey[key]; exists {
			continue
		}
		projected := models.ProjectOfferings([]domain.Model{model}, []models.OfferingSource{{Instance: byProvider[providerID]}})
		if len(projected) > 0 {
			byKey[key] = projected[0]
		}
	}
	out := make([]domain.ModelOffering, 0, len(byKey))
	for _, offering := range byKey {
		out = append(out, offering)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProviderID != out[j].ProviderID {
			return out[i].ProviderID < out[j].ProviderID
		}
		return out[i].ModelID < out[j].ModelID
	})
	return out, nil
}

// ProviderModelListingEntries exposes each offering under its provider's
// reserved namespace. Bare logical names remain available for automatic routing.
func ProviderModelListingEntries(onlyFree bool) []map[string]any {
	offerings, err := ModelOfferings()
	if err != nil {
		return nil
	}
	instances, err := providers.SnapshotInstances()
	if err != nil {
		return nil
	}
	byID := make(map[domain.ProviderInstanceID]domain.ProviderInstance, len(instances))
	for _, instance := range instances {
		byID[instance.ID] = instance
	}
	cfg := liveConfig()
	out := make([]map[string]any, 0, len(offerings))
	for _, offering := range offerings {
		instance := byID[offering.ProviderID]
		prefix := cfg.ProviderModelPrefix(string(offering.ProviderID))
		if prefix == "" || !instance.Enabled || offering.Disabled || offering.Delisted || onlyFree && !offering.FreeOnlyEligible() {
			continue
		}
		id := prefix + "/" + offering.ModelID
		if cfg.ExcludesModel(id) || cfg.ExcludesModel(offering.ModelID) {
			continue
		}
		ownedBy := instance.Name
		source := "provider"
		switch offering.ProviderID {
		case "cline":
			ownedBy, source = "cline", "cline"
		case "opencode":
			ownedBy, source = "opencode", "zen"
		}
		entry := map[string]any{
			"id": id, "object": "model", "created": time.Now().UnixMilli(),
			"owned_by": ownedBy, "alias_of": offering.ModelID,
			"providerId": string(offering.ProviderID), "providerName": instance.Name, "source": source,
		}
		if offering.Context > 0 {
			entry["context_length"] = offering.Context
		}
		if offering.Output > 0 {
			entry["max_output_tokens"] = offering.Output
		}
		if len(offering.ThinkingLevels) > 0 {
			entry["thinking_levels"] = append([]string(nil), offering.ThinkingLevels...)
		}
		if len(offering.InputModalities) > 0 {
			entry["input_modalities"] = append([]string(nil), offering.InputModalities...)
		}
		if len(offering.OutputModalities) > 0 {
			entry["output_modalities"] = append([]string(nil), offering.OutputModalities...)
		}
		out = append(out, entry)
	}
	return out
}
