package providers

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"time"

	"routenest/internal/domain"
	"routenest/internal/models"
	"routenest/internal/store/syncstate"
)

var ErrAmbiguousModelOffering = errors.New("providerId is required when multiple providers offer this model")

type offeringDecision struct {
	ID     string
	Cost   string
	Signal string
}

func boolValue(value bool) *bool { return &value }

func intValue(value int) *int { return &value }

func stringValue(value string) *string { return &value }

func stringsValue(values []string) *[]string {
	out := append([]string{}, values...)
	return &out
}

func offeringAccess(cost, signal string) domain.AccessClass {
	if signal == tierSignalProviderFree {
		return domain.AccessUnknown
	}
	if cost == models.CostFree {
		return domain.AccessFree
	}
	if cost == models.CostPass {
		return domain.AccessPaidAPI
	}
	return domain.AccessUnknown
}

func cloneOfferingStates(in map[string]domain.ModelOfferingState) map[string]domain.ModelOfferingState {
	out := make(map[string]domain.ModelOfferingState, len(in))
	for modelID, state := range in {
		out[modelID] = state.Clone()
	}
	return out
}

func legacyOwnershipKey(providerName, modelID string) string {
	return strings.ToLower(strings.TrimSpace(providerName)) + "\x00" + strings.TrimSpace(modelID)
}

func providerLegacyOwnerCounts(providers []CustomProvider) map[string]int {
	counts := map[string]int{}
	for _, provider := range providers {
		seen := map[string]bool{}
		for _, modelID := range provider.ModelIDs {
			modelID = strings.TrimSpace(modelID)
			key := legacyOwnershipKey(provider.Name, modelID)
			if modelID == "" || seen[key] {
				continue
			}
			seen[key] = true
			counts[key]++
		}
	}
	return counts
}

// providerLegacyOwnerCountsLocked reads CustomProviders while Mu is already
// held by routing selection. It must not call List, which would re-lock Mu.
func providerLegacyOwnerCountsLocked() map[string]int {
	providers := make([]CustomProvider, 0, len(CustomProviders))
	for _, provider := range CustomProviders {
		if provider != nil {
			providers = append(providers, *provider)
		}
	}
	return providerLegacyOwnerCounts(providers)
}

func providerMayInheritLegacy(provider CustomProvider, modelID string, owners map[string]int) bool {
	return owners[legacyOwnershipKey(provider.Name, modelID)] == 1
}

func providerOwnsLegacyModel(provider CustomProvider, model domain.Model, allowLegacy bool) bool {
	return allowLegacy && model.Source == models.SourceProvider && strings.EqualFold(strings.TrimSpace(model.Provider), strings.TrimSpace(provider.Name))
}

func seedProviderOffering(provider CustomProvider, modelID string, legacy domain.Model, hasLegacy, allowLegacy bool) domain.ModelOfferingState {
	state := domain.ModelOfferingState{UpstreamModelID: modelID}
	if hasLegacy && providerOwnsLegacyModel(provider, legacy, allowLegacy) {
		cost, signal := legacy.Cost, legacy.TierSignal
		decidedAt := legacy.TierDecidedAt
		if signal == "" {
			if classifiedCost, classifiedSignal := classifyUpstreamModel(upstreamModelEntry{ID: modelID}, provider.Free); classifiedCost == models.CostFree {
				cost, signal, decidedAt = classifiedCost, classifiedSignal, time.Now().Unix()
			}
		}
		state.Cost = cost
		state.Access = offeringAccess(cost, signal)
		state.TierSignal = signal
		state.TierDecidedAt = decidedAt
		if legacy.Status != "" {
			state.Status = stringValue(legacy.Status)
		}
		state.Disabled = boolValue(legacy.Disabled)
		state.Delisted = boolValue(legacy.Delisted)
		state.Context = intValue(legacy.Context)
		state.Output = intValue(legacy.Output)
		state.MetaLocked = boolValue(legacy.MetaLocked)
		if legacy.ThinkingLevels != nil {
			state.ThinkingLevels = stringsValue(legacy.ThinkingLevels)
		}
		if legacy.InputModalities != nil {
			state.InputModalities = stringsValue(legacy.InputModalities)
		}
		if legacy.OutputModalities != nil {
			state.OutputModalities = stringsValue(legacy.OutputModalities)
		}
		if legacy.Custom {
			state.Custom = boolValue(true)
		}
		if legacy.TierSignal == "manual" {
			state.Manual = boolValue(true)
		}
		return state
	}

	cost, signal := classifyUpstreamModel(upstreamModelEntry{ID: modelID}, provider.Free)
	state.Cost = cost
	state.Access = offeringAccess(cost, signal)
	state.TierSignal = signal
	state.Delisted = boolValue(false)
	return state
}

func mergeProviderOfferingState(base, overlay domain.ModelOfferingState) domain.ModelOfferingState {
	overlay = overlay.Clone()
	if overlay.UpstreamModelID != "" {
		base.UpstreamModelID = overlay.UpstreamModelID
	}
	if overlay.Cost != "" {
		base.Cost = overlay.Cost
		base.TierSignal = overlay.TierSignal
		base.TierDecidedAt = overlay.TierDecidedAt
	}
	if overlay.Access != "" {
		base.Access = overlay.Access
	}
	if overlay.Cost == "" {
		if overlay.TierSignal != "" {
			base.TierSignal = overlay.TierSignal
		}
		if overlay.TierDecidedAt != 0 {
			base.TierDecidedAt = overlay.TierDecidedAt
		}
	}
	if overlay.AllowanceVerified != nil {
		base.AllowanceVerified = overlay.AllowanceVerified
	}
	if overlay.Status != nil {
		base.Status = overlay.Status
	}
	if overlay.Disabled != nil {
		base.Disabled = overlay.Disabled
	}
	if overlay.Delisted != nil {
		base.Delisted = overlay.Delisted
	}
	if overlay.Manual != nil {
		base.Manual = overlay.Manual
	}
	if overlay.Custom != nil {
		base.Custom = overlay.Custom
	}
	if overlay.Context != nil {
		base.Context = overlay.Context
	}
	if overlay.Output != nil {
		base.Output = overlay.Output
	}
	if overlay.MetaLocked != nil {
		base.MetaLocked = overlay.MetaLocked
	}
	if overlay.ThinkingLevels != nil {
		base.ThinkingLevels = overlay.ThinkingLevels
	}
	if overlay.InputModalities != nil {
		base.InputModalities = overlay.InputModalities
	}
	if overlay.OutputModalities != nil {
		base.OutputModalities = overlay.OutputModalities
	}
	return base
}

func legacyModelsByID(records []domain.Model) map[string]domain.Model {
	out := make(map[string]domain.Model, len(records))
	for _, model := range records {
		out[model.ID] = model
	}
	return out
}

func stateForModel(provider CustomProvider, modelID string, persisted map[string]domain.ModelOfferingState, legacy map[string]domain.Model, allowLegacy bool) domain.ModelOfferingState {
	model, hasLegacy := legacy[modelID]
	base := seedProviderOffering(provider, modelID, model, hasLegacy, allowLegacy)
	if state, ok := persisted[modelID]; ok && state.Valid() {
		base = mergeProviderOfferingState(base, state)
	}
	// ModelMap is the single source of truth for the upstream ID. A persisted
	// value can only be stale (written before the mapping changed).
	if mapped := strings.TrimSpace(provider.ModelMap[modelID]); mapped != "" {
		base.UpstreamModelID = mapped
	}
	return base
}

// syncProviderOfferingDeclarations makes each provider's declared ModelIDs the
// authoritative key set for its persisted offering overlays. Existing fields are
// preserved; new declarations are seeded from the matching legacy owner or from
// provider-local classification.
func syncProviderOfferingDeclarations(providers []CustomProvider, records []domain.Model) bool {
	LoadRecheckState()
	legacy := legacyModelsByID(records)
	legacyOwners := providerLegacyOwnerCounts(providers)
	changed := false
	provRecheckMu.Lock()
	for _, provider := range providers {
		state := providerRecheckStat[provider.ID]
		if state == nil {
			state = &providerRecheckState{}
			providerRecheckStat[provider.ID] = state
		}
		desired := make(map[string]domain.ModelOfferingState, len(provider.ModelIDs))
		for _, modelID := range provider.ModelIDs {
			modelID = strings.TrimSpace(modelID)
			if modelID == "" {
				continue
			}
			if existing, ok := state.Offerings[modelID]; ok && existing.Valid() {
				// Sparse overlays inherit at read time. Declaration sync must not
				// materialize fallback fields and rewrite an unrelated provider.
				desired[modelID] = existing.Clone()
				continue
			}
			model, hasLegacy := legacy[modelID]
			desired[modelID] = seedProviderOffering(provider, modelID, model, hasLegacy, providerMayInheritLegacy(provider, modelID, legacyOwners))
		}
		if !reflect.DeepEqual(state.Offerings, desired) {
			state.Offerings = desired
			changed = true
		}
	}
	if changed {
		saveProviderRecheckStateLocked()
	}
	provRecheckMu.Unlock()
	return changed
}

// providerOfferingStates returns an independent snapshot for every declared
// model. Missing persisted entries use migration-time legacy seeding in memory.
func providerOfferingStates(provider CustomProvider, records []domain.Model) map[string]domain.ModelOfferingState {
	LoadRecheckState()
	legacy := legacyModelsByID(records)
	legacyOwners := providerLegacyOwnerCounts(providersIncluding(provider))
	provRecheckMu.Lock()
	var persisted map[string]domain.ModelOfferingState
	if state := providerRecheckStat[provider.ID]; state != nil {
		persisted = cloneOfferingStates(state.Offerings)
	}
	provRecheckMu.Unlock()
	out := make(map[string]domain.ModelOfferingState, len(provider.ModelIDs))
	for _, modelID := range provider.ModelIDs {
		modelID = strings.TrimSpace(modelID)
		if modelID != "" {
			out[modelID] = stateForModel(provider, modelID, persisted, legacy, providerMayInheritLegacy(provider, modelID, legacyOwners))
		}
	}
	return out
}

func currentProviderOfferingScoped(provider CustomProvider, modelID string, allowLegacy bool) domain.ModelOfferingState {
	legacy := legacyModelsByID(models.Snapshot())
	LoadRecheckState()
	provRecheckMu.Lock()
	var persisted map[string]domain.ModelOfferingState
	if state := providerRecheckStat[provider.ID]; state != nil {
		persisted = cloneOfferingStates(state.Offerings)
	}
	provRecheckMu.Unlock()
	return stateForModel(provider, modelID, persisted, legacy, allowLegacy)
}

func currentProviderOffering(provider CustomProvider, modelID string) domain.ModelOfferingState {
	owners := providerLegacyOwnerCounts(providersIncluding(provider))
	return currentProviderOfferingScoped(provider, modelID, providerMayInheritLegacy(provider, modelID, owners))
}

func providerOfferingEligibleScoped(provider CustomProvider, modelID string, allowLegacy bool) bool {
	state := currentProviderOfferingScoped(provider, modelID, allowLegacy)
	if state.Delisted != nil && *state.Delisted {
		return false
	}
	if state.Disabled != nil && *state.Disabled {
		return false
	}
	return state.Status == nil || *state.Status == "" || *state.Status == "active"
}

func providerOfferingEligible(provider CustomProvider, modelID string) bool {
	owners := providerLegacyOwnerCounts(providersIncluding(provider))
	return providerOfferingEligibleScoped(provider, modelID, providerMayInheritLegacy(provider, modelID, owners))
}

func providerClaimCounts(providers []CustomProvider) map[string]int {
	counts := map[string]int{}
	for _, provider := range providers {
		seen := map[string]bool{}
		for _, modelID := range provider.ModelIDs {
			if modelID = strings.TrimSpace(modelID); modelID != "" && !seen[modelID] {
				seen[modelID] = true
				counts[modelID]++
			}
		}
	}
	return counts
}

func applyProviderOfferingDecisions(provider CustomProvider, decisions []offeringDecision, now int64) (added []string, changed int) {
	if len(decisions) == 0 {
		return nil, 0
	}
	records := models.Snapshot()
	legacy := legacyModelsByID(records)
	legacyOwners := providerLegacyOwnerCounts(providersIncluding(provider))
	declared := map[string]bool{}
	for _, modelID := range provider.ModelIDs {
		declared[modelID] = true
	}

	LoadRecheckState()
	provRecheckMu.Lock()
	state := providerRecheckStat[provider.ID]
	if state == nil {
		state = &providerRecheckState{}
		providerRecheckStat[provider.ID] = state
	}
	if state.Offerings == nil {
		state.Offerings = map[string]domain.ModelOfferingState{}
	}
	for _, decision := range decisions {
		current := stateForModel(provider, decision.ID, state.Offerings, legacy, providerMayInheritLegacy(provider, decision.ID, legacyOwners))
		isAdded := !declared[decision.ID]
		if isAdded {
			added = append(added, decision.ID)
		}
		access := offeringAccess(decision.Cost, decision.Signal)
		delisted := current.Delisted != nil && *current.Delisted
		if !isAdded && (current.Cost != decision.Cost || current.Access != access || current.TierSignal != decision.Signal || current.TierDecidedAt == 0 || delisted) {
			changed++
		}
		current.UpstreamModelID = UpstreamModelFor(provider, decision.ID)
		current.Cost = decision.Cost
		current.Access = access
		current.TierSignal = decision.Signal
		current.TierDecidedAt = now
		current.Delisted = boolValue(false)
		manual := decision.Signal == "manual"
		current.Manual = &manual
		state.Offerings[decision.ID] = current
	}
	saveProviderRecheckStateLocked()
	provRecheckMu.Unlock()
	return added, changed
}

func setProviderOfferingCost(provider CustomProvider, modelID, cost string, now int64) bool {
	records := models.Snapshot()
	legacy := legacyModelsByID(records)
	legacyOwners := providerLegacyOwnerCounts(providersIncluding(provider))
	LoadRecheckState()
	provRecheckMu.Lock()
	state := providerRecheckStat[provider.ID]
	if state == nil {
		state = &providerRecheckState{}
		providerRecheckStat[provider.ID] = state
	}
	if state.Offerings == nil {
		state.Offerings = map[string]domain.ModelOfferingState{}
	}
	offering := stateForModel(provider, modelID, state.Offerings, legacy, providerMayInheritLegacy(provider, modelID, legacyOwners))
	if cost == "" {
		offering.TierSignal = ""
		offering.TierDecidedAt = 0
		offering.Manual = boolValue(false)
	} else {
		offering.Cost = cost
		offering.Access = offeringAccess(cost, "manual")
		offering.TierSignal = "manual"
		offering.TierDecidedAt = now
		offering.Manual = boolValue(true)
	}
	state.Offerings[modelID] = offering
	saveProviderRecheckStateLocked()
	provRecheckMu.Unlock()
	return true
}

func applyProviderOfferingDelisting(provider CustomProvider, toMark []string, upstream map[string]bool) (marked, total int) {
	records := models.Snapshot()
	legacy := legacyModelsByID(records)
	legacyOwners := providerLegacyOwnerCounts(providersIncluding(provider))
	mark := map[string]bool{}
	for _, modelID := range toMark {
		mark[modelID] = true
	}
	declared := map[string]bool{}
	for _, modelID := range provider.ModelIDs {
		declared[modelID] = true
	}

	LoadRecheckState()
	provRecheckMu.Lock()
	state := providerRecheckStat[provider.ID]
	if state == nil {
		state = &providerRecheckState{}
		providerRecheckStat[provider.ID] = state
	}
	if state.Offerings == nil {
		state.Offerings = map[string]domain.ModelOfferingState{}
	}
	for modelID := range declared {
		offering := stateForModel(provider, modelID, state.Offerings, legacy, providerMayInheritLegacy(provider, modelID, legacyOwners))
		wasDelisted := offering.Delisted != nil && *offering.Delisted
		wantDelisted := wasDelisted
		if mark[modelID] {
			wantDelisted = true
		} else if upstream[modelID] {
			wantDelisted = false
		}
		if wantDelisted && !wasDelisted {
			marked++
		}
		offering.Delisted = boolValue(wantDelisted)
		state.Offerings[modelID] = offering
		if wantDelisted {
			total++
		}
	}
	state.DelistedMarked = total
	state.DelistedModels = delistedOfferingIDs(state.Offerings, declared)
	saveProviderRecheckStateLocked()
	provRecheckMu.Unlock()
	return marked, total
}

func delistedOfferingIDs(offerings map[string]domain.ModelOfferingState, declared map[string]bool) []string {
	ids := make([]string, 0)
	for modelID, offering := range offerings {
		if declared[modelID] && offering.Delisted != nil && *offering.Delisted {
			ids = append(ids, modelID)
		}
	}
	sort.Strings(ids)
	return ids
}

func providerDelistedSnapshot(providerID string, modelIDs []string) (int, []string) {
	declared := make(map[string]bool, len(modelIDs))
	for _, modelID := range modelIDs {
		if modelID = strings.TrimSpace(modelID); modelID != "" {
			declared[modelID] = true
		}
	}
	LoadRecheckState()
	provRecheckMu.Lock()
	defer provRecheckMu.Unlock()
	state := providerRecheckStat[providerID]
	if state == nil {
		return 0, nil
	}
	ids := delistedOfferingIDs(state.Offerings, declared)
	return len(ids), ids
}

func syncProviderDelistedProjection(providers []CustomProvider) {
	aggregate := map[string]bool{}
	seen := map[string]bool{}
	legacyOwners := providerLegacyOwnerCounts(providers)
	for _, provider := range providers {
		if !provider.Enabled {
			continue
		}
		for _, modelID := range provider.ModelIDs {
			modelID = strings.TrimSpace(modelID)
			if modelID == "" {
				continue
			}
			delisted := false
			state := currentProviderOfferingScoped(provider, modelID, providerMayInheritLegacy(provider, modelID, legacyOwners))
			if state.Delisted != nil {
				delisted = *state.Delisted
			}
			if !seen[modelID] {
				aggregate[modelID] = delisted
				seen[modelID] = true
			} else {
				aggregate[modelID] = aggregate[modelID] && delisted
			}
		}
	}
	models.ApplyProviderDelistedProjection(aggregate)
}

func providersIncluding(provider CustomProvider) []CustomProvider {
	providers := List()
	for _, existing := range providers {
		if existing.ID == provider.ID {
			return providers
		}
	}
	return append(providers, provider)
}

func resolveModelCostProvider(providerID, modelID string) (*CustomProvider, error) {
	providers := List()
	var matches []CustomProvider
	for _, provider := range providers {
		if providerID != "" && provider.ID != providerID {
			continue
		}
		for _, declared := range provider.ModelIDs {
			if declared == modelID {
				matches = append(matches, provider)
				break
			}
		}
	}
	if providerID != "" {
		if len(matches) == 0 {
			return nil, ErrModelNotFound
		}
		return &matches[0], nil
	}
	if len(matches) > 1 {
		return nil, ErrAmbiguousModelOffering
	}
	if len(matches) == 1 {
		return &matches[0], nil
	}
	return nil, nil
}

func mirrorOfferingDecisions(provider CustomProvider, decisions []offeringDecision, now int64) {
	providers := List()
	counts := providerClaimCounts(providers)
	exclusive := make(map[string]bool, len(decisions))
	legacy := make([]models.Decision, 0, len(decisions))
	for _, decision := range decisions {
		exclusive[decision.ID] = counts[decision.ID] == 1
		legacy = append(legacy, models.Decision{ID: decision.ID, Cost: decision.Cost, Signal: decision.Signal})
	}
	models.ApplyProviderProjection(legacy, provider.Name, exclusive, now)
}

func updateProviderOfferingCost(provider CustomProvider, modelID, cost string) error {
	now := time.Now().Unix()
	if !setProviderOfferingCost(provider, modelID, cost, now) {
		return ErrModelNotFound
	}
	state := currentProviderOffering(provider, modelID)
	mirrorOfferingDecisions(provider, []offeringDecision{{ID: modelID, Cost: state.Cost, Signal: state.TierSignal}}, now)
	return nil
}

// cloneProviderRecheckState returns a persistence-safe copy for callers that
// must retain provider offering overlays while replacing display status fields.
func cloneProviderRecheckState(state providerRecheckState) providerRecheckState {
	return syncstate.CloneState(state)
}
