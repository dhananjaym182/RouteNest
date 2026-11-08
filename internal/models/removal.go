package models

import (
	"reflect"
	"strings"

	"routenest/internal/accounts"
	"routenest/internal/config"
	"routenest/internal/domain"
)

// ReconcileRemoval deletes only the requested delisted source records and
// repairs that logical ID's provider projection after declarations change.
// All writes are strict; failed persistence leaves the live pool unchanged.
// providerDelisted is evidence checked by the provider owner at mutation time.
func ReconcileRemoval(ref domain.ModelRemovalRef, claims []ProviderClaim, delisted map[string]bool, providerDelisted bool) (removed, protected bool, err error) {
	return reconcileRemoval(ref, claims, delisted, providerDelisted, false, false, nil)
}

// ReconcileExplicitRemoval removes the selected provider's records even when
// active or custom. Builtin removal reservations commit with the catalogue.
// remainingBuiltinOffering preserves a default backed by an offline sibling.
func ReconcileExplicitRemoval(ref domain.ModelRemovalRef, claims []ProviderClaim, delisted map[string]bool, remainingBuiltinOffering bool) (bool, error) {
	removed, _, err := reconcileRemoval(ref, claims, delisted, false, true, remainingBuiltinOffering, config.Live())
	return removed, err
}

func reconcileRemoval(ref domain.ModelRemovalRef, claims []ProviderClaim, delisted map[string]bool, providerDelisted, explicit, preserveGlobalDefault bool, cfg *config.Proxy) (removed, protected bool, err error) {
	accounts.LoadPool()
	accounts.PoolMu.Lock()
	defer accounts.PoolMu.Unlock()
	p := accounts.Pool
	oldModels, oldDefault, oldInitialized := p.Models, p.DefaultModel, p.ModelCatalogInitialized
	oldRemovals := p.RemovedModelOfferings
	wasInitialized := CatalogInitialized(p)
	var claim *ProviderClaim
	for i := range claims {
		if claims[i].ID == ref.ID {
			claim = &claims[i]
			break
		}
	}
	kept := make([]domain.Model, 0, len(p.Models)+1)
	for _, model := range p.Models {
		matches := model.ID == ref.ID && (model.Source == ref.Source || ref.Source == SourceZen && model.Source == SourceSeed || explicit && ref.Source == SourceRemote && model.Source == SourceCustom)
		if matches && ref.Source != SourceProvider {
			if explicit || model.Delisted && !model.Custom {
				removed = true
				continue
			}
			protected = true
		}
		if model.ID == ref.ID && model.Source == SourceProvider && explicit && ref.Source == SourceProvider {
			if claim == nil {
				removed = true
				continue
			}
			model = domain.Model{ID: ref.ID, Source: SourceProvider, Provider: claim.Provider, Cost: claim.Cost,
				Status: "active", TierSignal: claim.TierSignal, Delisted: delisted[ref.ID], Disabled: model.Disabled}
		} else if model.ID == ref.ID && model.Source == SourceProvider && !model.Custom && !explicit {
			if claim == nil {
				if model.Delisted || providerDelisted && model.Provider == ref.Provider {
					continue
				}
				kept = append(kept, model)
				continue
			}
			if model.Provider != claim.Provider && !claim.Shared {
				model.Provider, model.Cost = claim.Provider, claim.Cost
				model.TierSignal = claim.TierSignal
			}
			if state, known := delisted[ref.ID]; known {
				model.Delisted = state
			}
		}
		kept = append(kept, model)
	}
	if protected && !removed {
		return false, true, nil
	}
	if explicit && ReserveOfferingRemoval(p, ref) {
		removed = true
	}
	initialized := oldInitialized || removed && (ref.Source == SourceRemote || ref.Source == SourceZen)
	if explicit {
		initialized = oldInitialized || removed && wasInitialized
	}
	listed := false
	live := false
	defaultDeclared := false
	for _, model := range kept {
		defaultDeclared = defaultDeclared || model.ID == p.DefaultModel
		if model.ID == ref.ID {
			listed = true
			live = live || model.Custom || !model.Delisted
		}
	}
	if !listed && claim != nil {
		kept = append(kept, domain.Model{ID: ref.ID, Source: SourceProvider, Provider: claim.Provider, Cost: claim.Cost,
			Status: "active", TierSignal: claim.TierSignal, Delisted: delisted[ref.ID]})
	}
	live = live || claim != nil && !delisted[ref.ID]
	if len(kept) == 0 && oldModels == nil {
		kept = nil
	}
	nextDefault := p.DefaultModel
	if nextDefault == ref.ID && !live && !preserveGlobalDefault {
		nextDefault = ""
	}
	if explicit && nextDefault != "" && nextDefault != ref.ID && !defaultDeclared {
		modelID, providerID, qualified := cfg.SplitProviderModel(strings.TrimSpace(nextDefault))
		if qualified && modelID == ref.ID && providerID == ref.ProviderID ||
			!qualified && (nextDefault == ref.ID+"@"+ref.ProviderID || ref.Provider != "" && nextDefault == ref.ID+"@"+ref.Provider) {
			nextDefault = ""
		}
	}
	if reflect.DeepEqual(kept, oldModels) && nextDefault == oldDefault && initialized == oldInitialized && reflect.DeepEqual(p.RemovedModelOfferings, oldRemovals) {
		return removed, protected, nil
	}
	p.Models, p.DefaultModel, p.ModelCatalogInitialized = kept, nextDefault, initialized
	if err := accounts.SavePoolStrictLocked(); err != nil {
		p.Models, p.DefaultModel, p.ModelCatalogInitialized = oldModels, oldDefault, oldInitialized
		p.RemovedModelOfferings = oldRemovals
		return false, protected, err
	}
	return removed, protected, nil
}
