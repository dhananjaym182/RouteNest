package models

import (
	"sort"

	"routenest/internal/domain"
)

// OfferingSource supplies one provider instance and the model IDs it declares.
// Built-in Cline/Zen ownership is still projected from persisted Source values;
// custom providers use ModelIDs because the legacy model store collapses equal
// IDs across providers.
type OfferingSource struct {
	Instance domain.ProviderInstance
	ModelIDs []string
}

func offeringAccess(model domain.Model, providerID domain.ProviderInstanceID) domain.AccessClass {
	if model.TierSignal == "provider-free" {
		return domain.AccessUnknown
	}
	switch model.Cost {
	case CostFree:
		return domain.AccessFree
	case CostPass:
		if providerID == "cline" && model.Source == SourceRemote {
			return domain.AccessSubscription
		}
		return domain.AccessPaidAPI
	default:
		return domain.AccessUnknown
	}
}

func makeOffering(model domain.Model, instance domain.ProviderInstance) domain.ModelOffering {
	return domain.ModelOffering{
		ProviderID:       instance.ID,
		ModelID:          model.ID,
		UpstreamModelID:  model.ID,
		Access:           offeringAccess(model, instance.ID),
		TierSignal:       model.TierSignal,
		TierDecidedAt:    model.TierDecidedAt,
		ProviderEnabled:  instance.Enabled,
		Status:           model.Status,
		Disabled:         model.Disabled,
		Delisted:         model.Delisted,
		Manual:           model.TierSignal == "manual" || model.Custom,
		Custom:           model.Custom,
		Context:          model.Context,
		Output:           model.Output,
		MetaLocked:       model.MetaLocked,
		ThinkingLevels:   append([]string(nil), model.ThinkingLevels...),
		InputModalities:  append([]string(nil), model.InputModalities...),
		OutputModalities: append([]string(nil), model.OutputModalities...),
	}
}

func addOffering(out map[domain.OfferingKey]domain.ModelOffering, model domain.Model, instance domain.ProviderInstance) {
	if model.ID == "" || !instance.ID.Valid() {
		return
	}
	offering := makeOffering(model, instance)
	out[offering.Key()] = offering
}

// ProjectOfferings builds a deterministic provider-scoped read model without
// mutating the persisted catalogue or provider records.
func ProjectOfferings(records []domain.Model, sources []OfferingSource) []domain.ModelOffering {
	instances := make(map[domain.ProviderInstanceID]domain.ProviderInstance, len(sources))
	byName := make(map[string]domain.ProviderInstance, len(sources))
	recordByID := make(map[string]domain.Model, len(records))
	for _, source := range sources {
		instances[source.Instance.ID] = source.Instance
		if source.Instance.Name != "" {
			byName[source.Instance.Name] = source.Instance
		}
	}
	for _, model := range records {
		recordByID[model.ID] = model
	}

	projected := make(map[domain.OfferingKey]domain.ModelOffering)
	for _, model := range records {
		switch model.Source {
		case SourceRemote:
			addOffering(projected, model, instances["cline"])
		case SourceZen, SourceSeed:
			addOffering(projected, model, instances["opencode"])
		case SourceProvider:
			// Normal provider ownership comes from ModelIDs below. A name match
			// preserves orphaned legacy projections until their provider is saved.
			if instance, ok := byName[model.Provider]; ok {
				addOffering(projected, model, instance)
			}
		case SourceCustom:
			// Legacy manual models route through the Cline path when no custom
			// provider claims them; preserve that behavior in the read model.
			addOffering(projected, model, instances["cline"])
		}
	}

	for _, source := range sources {
		if source.Instance.ID == "cline" || source.Instance.ID == "opencode" {
			continue
		}
		for _, modelID := range source.ModelIDs {
			model, ok := recordByID[modelID]
			if !ok {
				continue
			}
			addOffering(projected, model, source.Instance)
		}
	}

	out := make([]domain.ModelOffering, 0, len(projected))
	for _, offering := range projected {
		out = append(out, offering)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProviderID != out[j].ProviderID {
			return out[i].ProviderID < out[j].ProviderID
		}
		if out[i].ModelID != out[j].ModelID {
			return out[i].ModelID < out[j].ModelID
		}
		return out[i].UpstreamModelID < out[j].UpstreamModelID
	})
	return out
}
