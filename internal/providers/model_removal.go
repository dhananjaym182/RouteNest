package providers

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"routenest/internal/config"
	"routenest/internal/domain"
	"routenest/internal/modelcatalog"
	"routenest/internal/models"
	providerstore "routenest/internal/store/providers"
)

// Serialize declaration/import/recheck mutation stages, without holding this
// lock during upstream discovery. Catalogue reads and request dispatch remain
// independent of this administration lock.
var modelLifecycleMu sync.Mutex

// The composition root includes offline builtin offerings in this read-only
// catalogue. The local projection keeps this package independently testable.
var ModelRemovalOfferingsProvider = SnapshotModelOfferings

// RemoveDelistedModel removes one verified delisted offering and its stale
// references. Each store write is strict. A failure may leave earlier cleanup
// stages committed, so an absent target remains eligible for idempotent cleanup
// of its remaining references on retry; it is never reported as removed on error.
func RemoveDelistedModel(input domain.ModelRemovalRef) (bool, error) {
	return removeModel(input, false)
}

// RemoveModel explicitly removes one offering selected by stable provider ID,
// including active and administrator-added models. Other providers remain intact.
func RemoveModel(input domain.ModelRemovalRef) (bool, error) {
	return removeModel(input, true)
}

func removeModel(input domain.ModelRemovalRef, explicit bool) (bool, error) {
	ref, err := input.Normalize()
	if err != nil {
		return false, err
	}
	if explicit && ref.ProviderID == "" {
		return false, fmt.Errorf("explicit model removal requires a stable providerId")
	}
	modelLifecycleMu.Lock()
	defer modelLifecycleMu.Unlock()
	Load()
	provRecheckMu.Lock()
	err = loadProviderRuntimeStateLocked(nil)
	provRecheckMu.Unlock()
	if err != nil {
		return false, fmt.Errorf("provider state unavailable: %w", err)
	}
	var beforeOfferings []domain.ModelOffering
	if explicit {
		beforeOfferings, err = ModelRemovalOfferingsProvider()
		if err != nil {
			return false, fmt.Errorf("model offerings unavailable: %w", err)
		}
		if ref.Source != models.SourceProvider {
			known := false
			for _, offering := range beforeOfferings {
				known = known || string(offering.ProviderID) == ref.ProviderID && offering.ModelID == ref.ID
			}
			models.View(func(pool *domain.AccountPool) {
				known = known || models.OfferingRemovalReserved(pool, ref.ProviderID, ref.ID)
			})
			if !known {
				return false, nil
			}
		}
	}
	removed := false
	providerDelisted := false
	var target *CustomProvider
	if ref.Source == models.SourceProvider {
		target, err = resolveRemovalProvider(ref)
		if err != nil {
			return false, err
		}
		if target == nil {
			return false, nil
		}
		ref.ProviderID, ref.Provider = target.ID, target.Name
		state := currentProviderOffering(*target, ref.ID)
		providerDelisted = state.Delisted != nil && *state.Delisted
		if !explicit && state.Custom != nil && *state.Custom {
			return false, nil
		}
		if containsModel(target.ModelIDs, ref.ID) {
			if !explicit && !providerDelisted {
				return false, nil
			}
			if err := removeProviderDeclarationStrict(target.ID, ref.ID); err != nil {
				return false, err
			}
			removed = true
		}
	}
	claims, aggregate := removalClaims(ref.ID)
	configRef := ref
	if ref.Source == models.SourceRemote {
		configRef.Provider = "Cline"
	}
	if ref.Source == models.SourceZen {
		configRef.Provider = "OpenCode Zen"
	}
	var modelRemoved, protected bool
	if explicit {
		claims, aggregate = removalClaimsWithPolicy(ref.ID, true)
		keepDefault := false
		liveProviderNames := make(map[string]string)
		for _, provider := range List() {
			liveProviderNames[provider.ID] = provider.Name
		}
		modelRef := configRef
		for _, offering := range beforeOfferings {
			providerID := string(offering.ProviderID)
			if offering.ModelID == ref.ID && providerID != ref.ProviderID {
				keepDefault = true
				if name := liveProviderNames[providerID]; name != "" && strings.EqualFold(name, configRef.Provider) {
					modelRef.Provider = ""
				}
			}
		}
		modelRemoved, err = models.ReconcileExplicitRemoval(modelRef, claims, aggregate, keepDefault)
	} else {
		modelRemoved, protected, err = models.ReconcileRemoval(ref, claims, aggregate, providerDelisted)
	}
	if err != nil {
		return false, fmt.Errorf("persist model catalogue: %w", err)
	}
	removed = removed || modelRemoved
	if protected && !modelRemoved && ref.Source != models.SourceProvider {
		return false, nil
	}
	if ref.Source == models.SourceProvider {
		stateRemoved, err := removeProviderOfferingStrict(ref.ProviderID, ref.ID)
		if err != nil {
			return false, err
		}
		removed = removed || stateRemoved
		if err := removeProviderCooldownStrict(ref.ProviderID, ref.ID); err != nil {
			return false, err
		}
	}
	// Use current unmerged state after cleanup, including provider offerings that
	// are not represented by their own pool row because another source shares ID.
	globalLive, sourceLive, nameLive := remainingRemovalOfferings(configRef)
	if explicit {
		globalLive, sourceLive, nameLive, err = remainingExplicitRemovalOfferings(configRef)
		if err != nil {
			return false, fmt.Errorf("model offerings unavailable after removal: %w", err)
		}
	}
	if err := config.PruneModelReferences(configRef, !globalLive, !sourceLive, !nameLive && (target != nil || ref.Source != models.SourceProvider)); err != nil {
		return false, fmt.Errorf("persist routing references: %w", err)
	}
	return removed, nil
}

func resolveRemovalProvider(ref domain.ModelRemovalRef) (*CustomProvider, error) {
	if ref.ProviderID != "" {
		return Find(ref.ProviderID), nil
	}
	var found *CustomProvider
	for _, provider := range List() {
		if !strings.EqualFold(strings.TrimSpace(provider.Name), ref.Provider) {
			continue
		}
		if found != nil {
			return nil, ErrAmbiguousModelOffering
		}
		copy := provider
		found = &copy
	}
	if found == nil {
		return nil, ErrProviderNotFound
	}
	return found, nil
}

func containsModel(ids []string, target string) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func removeProviderDeclarationStrict(providerID, modelID string) error {
	Mu.Lock()
	defer Mu.Unlock()
	next := make([]*CustomProvider, 0, len(CustomProviders))
	for _, provider := range CustomProviders {
		copy := cloneProvider(provider)
		if copy.ID == providerID {
			ids := make([]string, 0, len(copy.ModelIDs))
			for _, id := range copy.ModelIDs {
				if id != modelID {
					ids = append(ids, id)
				}
			}
			copy.ModelIDs = ids
			delete(copy.ModelMap, modelID)
			if len(copy.ModelMap) == 0 {
				copy.ModelMap = nil
			}
		}
		next = append(next, copy)
	}
	if err := providerstore.Save(FilePath, next); err != nil {
		return fmt.Errorf("persist provider declaration: %w", err)
	}
	CustomProviders = next
	return nil
}

func removalClaims(modelID string) ([]models.ProviderClaim, map[string]bool) {
	return removalClaimsWithPolicy(modelID, false)
}

func removalClaimsWithPolicy(modelID string, includeDisabled bool) ([]models.ProviderClaim, map[string]bool) {
	records := models.Snapshot()
	var claims []models.ProviderClaim
	aggregate := map[string]bool{}
	for _, provider := range List() {
		if !includeDisabled && !provider.Enabled || !containsModel(provider.ModelIDs, modelID) {
			continue
		}
		state := providerOfferingStates(provider, records)[modelID]
		delisted := state.Delisted != nil && *state.Delisted
		if len(claims) == 0 {
			claims = append(claims, models.ProviderClaim{ID: modelID, Provider: provider.Name, Cost: state.Cost, TierSignal: state.TierSignal})
			aggregate[modelID] = delisted
		} else {
			claims[0].Shared = true
			aggregate[modelID] = aggregate[modelID] && delisted
		}
	}
	return claims, aggregate
}

func remainingExplicitRemovalOfferings(ref domain.ModelRemovalRef) (global, source, name bool, err error) {
	offerings, err := ModelRemovalOfferingsProvider()
	if err != nil {
		return false, false, false, err
	}
	names := make(map[string]string)
	for _, provider := range List() {
		names[provider.ID] = provider.Name
	}
	for _, offering := range offerings {
		if offering.ModelID != ref.ID {
			continue
		}
		global = true
		providerID := string(offering.ProviderID)
		source = source || providerID == ref.ProviderID
		name = name || ref.Provider != "" && strings.EqualFold(strings.TrimSpace(names[providerID]), ref.Provider)
	}
	return
}

func removeProviderOfferingStrict(providerID, modelID string) (bool, error) {
	// Pending previews are ephemeral, but prune them before calculating the
	// persisted queue counters. Applying a stale batch cannot re-add this model.
	importsMu.Lock()
	for id, batch := range pendingImports {
		if batch.ProviderID != providerID {
			continue
		}
		copy := *batch
		copy.Items = make([]importItem, 0, len(batch.Items))
		for _, item := range batch.Items {
			if item.ModelID != modelID {
				copy.Items = append(copy.Items, item)
			}
		}
		if len(copy.Items) == 0 {
			delete(pendingImports, id)
		} else {
			copy.Stats = modelcatalog.ComputeBatchStats(copy.Items)
			pendingImports[id] = &copy
		}
	}
	importsMu.Unlock()
	provRecheckMu.Lock()
	defer provRecheckMu.Unlock()
	previous := providerRecheckStat[providerID]
	if previous == nil {
		return false, nil
	}
	next := cloneProviderRecheckState(*previous)
	_, existed := next.Offerings[modelID]
	delete(next.Offerings, modelID)
	ids := make([]string, 0, len(next.DelistedModels))
	for _, id := range next.DelistedModels {
		if id != modelID {
			ids = append(ids, id)
		}
	}
	next.DelistedModels, next.DelistedMarked = ids, len(ids)
	importsMu.Lock()
	batch := pendingImports[next.BatchID]
	if batch == nil {
		next.BatchID, next.NewQueued = "", 0
	} else {
		next.NewQueued = batch.Stats.Add
	}
	importsMu.Unlock()
	miss := provRecheckMiss[providerID]
	next.MissCounting = 0
	for id, count := range miss {
		if id != modelID && count > 0 && count < providerRecheckMissLimit {
			next.MissCounting++
		}
	}
	providerRecheckStat[providerID] = &next
	if err := saveProviderRecheckStateLocked(); err != nil {
		providerRecheckStat[providerID] = previous
		return false, fmt.Errorf("persist provider offering state: %w", err)
	}
	delete(miss, modelID)
	return existed, nil
}

func removeProviderCooldownStrict(providerID, modelID string) error {
	Mu.Lock()
	defer Mu.Unlock()
	key := providerID + ":" + modelID
	if _, exists := Cooldowns[key]; !exists {
		return nil
	}
	next := make(map[string]time.Time, len(Cooldowns))
	for id, until := range Cooldowns {
		if id != key {
			next[id] = until
		}
	}
	if err := providerstore.SaveCooldowns(CooldownsPath, next); err != nil {
		return fmt.Errorf("persist model cooldown cleanup: %w", err)
	}
	Cooldowns = next
	return nil
}

func remainingRemovalOfferings(ref domain.ModelRemovalRef) (global, source, name bool) {
	for _, model := range models.Snapshot() {
		if model.ID != ref.ID || model.Delisted && !model.Custom {
			continue
		}
		global = true
		if ref.Source != models.SourceProvider && (model.Source == ref.Source || ref.Source == models.SourceZen && model.Source == models.SourceSeed) {
			source = true
		}
	}
	for _, provider := range List() {
		if !provider.Enabled || !containsModel(provider.ModelIDs, ref.ID) {
			continue
		}
		state := currentProviderOffering(provider, ref.ID)
		if state.Delisted != nil && *state.Delisted {
			continue
		}
		global = true
		if provider.ID == ref.ProviderID {
			source = true
		}
		if strings.EqualFold(strings.TrimSpace(provider.Name), ref.Provider) {
			name = true
		}
	}
	return
}
