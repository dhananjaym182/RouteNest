package models

import (
	"sort"
	"strings"

	"routenest/internal/domain"
)

// OfferingSource supplies one provider instance and the model IDs it declares.
// Built-in Cline/Zen ownership is still projected from persisted Source values;
// custom providers use ModelIDs because the legacy model store collapses equal
// IDs across providers.
type OfferingSource struct {
	Instance domain.ProviderInstance
	ModelIDs []string
	States   map[string]domain.ModelOfferingState
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

func makeOffering(model domain.Model, instance domain.ProviderInstance, state domain.ModelOfferingState, hasState bool) domain.ModelOffering {
	offering := domain.ModelOffering{
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
	if !hasState || !state.Valid() {
		return offering
	}
	if state.UpstreamModelID != "" {
		offering.UpstreamModelID = state.UpstreamModelID
	}
	if state.Access.Valid() {
		offering.Access = state.Access
	} else if state.Cost != "" {
		offering.Access = offeringAccess(domain.Model{Cost: state.Cost, TierSignal: state.TierSignal}, instance.ID)
	}
	if state.Cost != "" {
		offering.TierSignal = state.TierSignal
		offering.TierDecidedAt = state.TierDecidedAt
	}
	if state.AllowanceVerified != nil {
		offering.AllowanceVerified = *state.AllowanceVerified
	}
	if state.Status != nil {
		offering.Status = *state.Status
	}
	if state.Disabled != nil {
		offering.Disabled = *state.Disabled
	}
	if state.Delisted != nil {
		offering.Delisted = *state.Delisted
	}
	if state.Manual != nil {
		offering.Manual = *state.Manual
	}
	if state.Custom != nil {
		offering.Custom = *state.Custom
	}
	if state.Context != nil {
		offering.Context = *state.Context
	}
	if state.Output != nil {
		offering.Output = *state.Output
	}
	if state.MetaLocked != nil {
		offering.MetaLocked = *state.MetaLocked
	}
	if state.ThinkingLevels != nil {
		offering.ThinkingLevels = append([]string(nil), (*state.ThinkingLevels)...)
	}
	if state.InputModalities != nil {
		offering.InputModalities = append([]string(nil), (*state.InputModalities)...)
	}
	if state.OutputModalities != nil {
		offering.OutputModalities = append([]string(nil), (*state.OutputModalities)...)
	}
	return offering
}

func addOffering(out map[domain.OfferingKey]domain.ModelOffering, model domain.Model, instance domain.ProviderInstance, state domain.ModelOfferingState, hasState bool) {
	if model.ID == "" || !instance.ID.Valid() {
		return
	}
	offering := makeOffering(model, instance, state, hasState)
	out[offering.Key()] = offering
}

// ProjectOfferings builds a deterministic provider-scoped read model without
// mutating the persisted catalogue or provider records.
func ProjectOfferings(records []domain.Model, sources []OfferingSource) []domain.ModelOffering {
	instances := make(map[domain.ProviderInstanceID]domain.ProviderInstance, len(sources))
	recordByID := make(map[string]domain.Model, len(records))
	nameOwners := make(map[string]int, len(sources))
	for _, source := range sources {
		instances[source.Instance.ID] = source.Instance
		if source.Instance.ID != "cline" && source.Instance.ID != "opencode" {
			name := strings.ToLower(strings.TrimSpace(source.Instance.Name))
			for _, modelID := range source.ModelIDs {
				nameOwners[name+"\x00"+modelID]++
			}
		}
	}
	for _, model := range records {
		recordByID[model.ID] = model
	}

	projected := make(map[domain.OfferingKey]domain.ModelOffering)
	for _, model := range records {
		switch model.Source {
		case SourceRemote:
			addOffering(projected, model, instances["cline"], domain.ModelOfferingState{}, false)
		case SourceZen, SourceSeed:
			addOffering(projected, model, instances["opencode"], domain.ModelOfferingState{}, false)
		case SourceCustom:
			// Legacy manual models route through the Cline path when no custom
			// provider claims them; preserve that behavior in the read model.
			addOffering(projected, model, instances["cline"], domain.ModelOfferingState{}, false)
		}
	}

	for _, source := range sources {
		if source.Instance.ID == "cline" || source.Instance.ID == "opencode" {
			continue
		}
		for _, modelID := range source.ModelIDs {
			model, ok := recordByID[modelID]
			if !ok {
				model = domain.Model{ID: modelID, Source: SourceProvider, Status: "active"}
			}
			state, hasState := source.States[modelID]
			// Runtime provider overlays are seeded with their own evidence. A
			// sparse overlay may inherit only an unambiguous legacy owner;
			// another provider's limits and status are not defaults for it.
			name := strings.ToLower(strings.TrimSpace(source.Instance.Name))
			if hasState && state.Valid() && (model.Source != SourceProvider ||
				!strings.EqualFold(strings.TrimSpace(model.Provider), strings.TrimSpace(source.Instance.Name)) || nameOwners[name+"\x00"+modelID] != 1) {
				model = domain.Model{ID: modelID, Source: SourceProvider, Status: "active"}
			}
			addOffering(projected, model, source.Instance, state, hasState)
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
