package providers

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"routenest/internal/budget"
	"routenest/internal/domain"
	"routenest/internal/platform"
	"routenest/internal/providerstate"
	"routenest/internal/store/syncstate"
)

// localRuntimeStateOwner shares the catalogue's mutex, table and file. Facets
// never retain a separate accounting or observation authority.
type localRuntimeStateOwner struct {
	path     string
	now      func() time.Time
	revision uint64
	health   domain.RuntimeStateHealth
	loadErr  error
}

var providerRuntimeStateOwner *localRuntimeStateOwner

type localBudgetBackend struct{ owner *localRuntimeStateOwner }
type localAvailabilityBackend struct{ owner *localRuntimeStateOwner }

var _ budget.Backend = (*localBudgetBackend)(nil)
var _ providerstate.Backend = (*localAvailabilityBackend)(nil)

// NewLocalRuntimeStateBackend strictly hydrates and purges the shared provider
// document before returning its budget/availability facets and redacted status.
// All facets for the current path share one owner, including separate Stores.
// Corrupt-file backup does not authorize retrying startup against empty state.
func NewLocalRuntimeStateBackend(now func() time.Time) (budget.Backend, providerstate.Backend, func() domain.RuntimeStateHealth, error) {
	provRecheckMu.Lock()
	defer provRecheckMu.Unlock()
	if err := loadProviderRuntimeStateLocked(now); err != nil {
		return nil, nil, nil, err
	}
	owner := providerRuntimeStateOwner
	return &localBudgetBackend{owner}, &localAvailabilityBackend{owner}, owner.status, nil
}

func loadProviderRuntimeStateLocked(now func() time.Time) error {
	ensureSyncPath()
	owner := providerRuntimeStateOwner
	if owner != nil && owner.path == providerSyncFilePath {
		if owner.loadErr != nil {
			return owner.loadErr
		}
		if provRecheckLoaded {
			return nil
		}
	}
	if now == nil {
		now = time.Now
	}
	owner = &localRuntimeStateOwner{path: providerSyncFilePath, now: now,
		health: domain.RuntimeStateHealth{Backend: "local_file", Enforcement: "durable", Reason: "initializing"}}
	providerRuntimeStateOwner = owner
	if _, err := os.Stat(providerSyncFilePath); errors.Is(err, os.ErrNotExist) {
		backupExists, backupErr := corruptRuntimeStateBackupExists(providerSyncFilePath)
		if backupErr != nil {
			return owner.failLoad("state_unreadable")
		}
		if backupExists {
			return owner.failLoad("state_corrupt")
		}
	}
	loaded, err := syncstate.Load(providerSyncFilePath)
	if err != nil {
		var syntax *json.SyntaxError
		var typeError *json.UnmarshalTypeError
		if errors.As(err, &syntax) || errors.As(err, &typeError) || errors.Is(err, syncstate.ErrInvalidDocument) {
			platform.BackupCorruptFile(providerSyncFilePath, err)
			return owner.failLoad("state_corrupt")
		}
		return owner.failLoad("state_unreadable")
	}
	table := make(map[string]*providerRecheckState, len(loaded))
	for id, state := range loaded {
		if state != nil {
			clone := syncstate.CloneState(*state)
			table[id] = &clone
		}
	}
	observations := availabilityFromTable(table)
	owner.revision = observations.Revision
	changed := false
	at := now().UTC()
	for _, state := range table {
		if state.Runtime == nil || state.Runtime.Budget == nil {
			continue
		}
		partition := budgetFromDTO(state.Runtime.Budget)
		before := partition.Clone()
		partition.Purge(at)
		if !reflect.DeepEqual(before, partition) {
			state.Runtime.Budget = budgetToDTO(partition, state.Runtime.Budget)
			changed = true
		}
	}
	observationChanged := false
	for i := range observations.Scopes {
		entry := &observations.Scopes[i]
		kept := make([]domain.AvailabilityObservation, 0, len(entry.Observations))
		for _, observation := range entry.Observations {
			if providerstate.Active(observation, at) {
				kept = append(kept, observation)
			}
		}
		if len(kept) != len(entry.Observations) {
			entry.Observations = kept
			entry.Revision++
			observationChanged = true
		}
	}
	if observationChanged {
		observations.Revision++
		availabilityToTable(table, observations, at)
		owner.revision = observations.Revision
		changed = true
	}
	owner.health.LastSuccessfulRead = at
	if changed {
		if err := owner.saveLocked(table); err != nil {
			owner.loadErr = err
			return err
		}
	}
	providerRecheckStat = table
	provRecheckLoaded = true
	owner.markReadyLocked()
	return nil
}

func (owner *localRuntimeStateOwner) failLoad(reason string) error {
	owner.loadErr = &budget.UnavailableError{}
	owner.health.Ready = false
	owner.health.SingleNodeReady = false
	owner.health.Degraded = true
	owner.health.Stale = true
	owner.health.Reason = reason
	return owner.loadErr
}

func corruptRuntimeStateBackupExists(path string) (bool, error) {
	entries, err := os.ReadDir(filepath.Dir(path))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	prefix := filepath.Base(path) + ".corrupt-"
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			return true, nil
		}
	}
	return false, nil
}

func (owner *localRuntimeStateOwner) availableLocked() error {
	if owner != providerRuntimeStateOwner || owner.path != providerSyncFilePath || owner.loadErr != nil || !provRecheckLoaded {
		return &budget.UnavailableError{}
	}
	return nil
}

func (owner *localRuntimeStateOwner) markReadyLocked() {
	owner.health.Ready = true
	owner.health.SingleNodeReady = true
	owner.health.Degraded = false
	owner.health.Stale = false
	owner.health.Reason = ""
}

func (owner *localRuntimeStateOwner) saveLocked(table map[string]*providerRecheckState) error {
	out := make(map[string]providerRecheckState, len(table))
	for id, state := range table {
		if state != nil {
			out[id] = syncstate.CloneState(*state)
		}
	}
	if err := syncstate.Save(owner.path, out); err != nil {
		owner.health.Ready = false
		owner.health.SingleNodeReady = false
		owner.health.Degraded = true
		owner.health.Stale = true
		owner.health.Reason = "persistence_failed"
		return &budget.UnavailableError{}
	}
	owner.health.LastSuccessfulWrite = owner.now().UTC()
	owner.markReadyLocked()
	return nil
}

func (owner *localRuntimeStateOwner) status() domain.RuntimeStateHealth {
	provRecheckMu.Lock()
	defer provRecheckMu.Unlock()
	if owner != providerRuntimeStateOwner || owner.path != providerSyncFilePath {
		health := owner.health
		health.Ready, health.SingleNodeReady, health.Degraded, health.Stale = false, false, true, true
		health.Reason = "backend_not_ready"
		return health
	}
	return owner.health
}

func (b *localBudgetBackend) Health() domain.RuntimeStateHealth       { return b.owner.status() }
func (b *localAvailabilityBackend) Health() domain.RuntimeStateHealth { return b.owner.status() }

func cloneRuntimeTable() map[string]*providerRecheckState {
	table := make(map[string]*providerRecheckState, len(providerRecheckStat))
	for id, state := range providerRecheckStat {
		if state != nil {
			clone := syncstate.CloneState(*state)
			table[id] = &clone
		}
	}
	return table
}

func runtimePartition(table map[string]*providerRecheckState, providerID string) *syncstate.RuntimeState {
	state := table[providerID]
	if state == nil {
		state = &providerRecheckState{}
		table[providerID] = state
	}
	if state.Runtime == nil {
		state.Runtime = &syncstate.RuntimeState{SchemaVersion: syncstate.RuntimeSchemaVersion}
	}
	return state.Runtime
}

func (b *localBudgetBackend) Snapshot(ctx context.Context, providerID string) (budget.State, error) {
	if err := ctx.Err(); err != nil {
		return budget.State{}, err
	}
	provRecheckMu.Lock()
	defer provRecheckMu.Unlock()
	if err := b.owner.availableLocked(); err != nil {
		return budget.State{}, err
	}
	if state := providerRecheckStat[providerID]; state != nil && state.Runtime != nil {
		return budgetFromDTO(state.Runtime.Budget), nil
	}
	return budget.State{}, nil
}

func (b *localBudgetBackend) CheckAndRecord(ctx context.Context, command budget.AdmissionCommand) (budget.Admission, error) {
	if err := ctx.Err(); err != nil {
		return budget.Admission{}, err
	}
	provRecheckMu.Lock()
	defer provRecheckMu.Unlock()
	if err := b.owner.availableLocked(); err != nil {
		return budget.Admission{}, err
	}
	table := cloneRuntimeTable()
	runtime := runtimePartition(table, command.ProviderID)
	state := budgetFromDTO(runtime.Budget)
	receipt, result := budget.Admit(&state, command)
	if result != nil && !errors.Is(result, budget.ErrExhausted) {
		return budget.Admission{}, result
	}
	if receipt.Replay {
		return receipt, result
	}
	runtime.Budget = budgetToDTO(state, runtime.Budget)
	if err := ctx.Err(); err != nil {
		return budget.Admission{}, err
	}
	if err := b.owner.saveLocked(table); err != nil {
		return budget.Admission{}, err
	}
	providerRecheckStat = table
	return receipt, result
}

func (b *localBudgetBackend) RecordTokens(ctx context.Context, event budget.TokenEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	provRecheckMu.Lock()
	defer provRecheckMu.Unlock()
	if err := b.owner.availableLocked(); err != nil {
		return err
	}
	table := cloneRuntimeTable()
	runtime := runtimePartition(table, event.ProviderID)
	state := budgetFromDTO(runtime.Budget)
	if err := budget.Account(&state, event); err != nil {
		return err
	}
	if runtime.Budget != nil {
		if _, replay := runtime.Budget.TokenEvents[event.EventID]; replay {
			return nil
		}
	}
	state.Purge(b.owner.now())
	runtime.Budget = budgetToDTO(state, runtime.Budget)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.owner.saveLocked(table); err != nil {
		return err
	}
	providerRecheckStat = table
	return nil
}

func budgetFromDTO(dto *syncstate.BudgetState) budget.State {
	if dto == nil {
		return budget.State{}
	}
	copy := dto.Clone()
	state := budget.State{RPM: copy.RPM, DailyRequests: copy.Daily, Tokens: copy.Tokens}
	if copy.Admissions != nil {
		state.Attempts = make(map[string]budget.Admission, len(copy.Admissions))
		for id, receipt := range copy.Admissions {
			policy := budget.Policy{RPMLimit: int(receipt.Limits.RPM), DailyLimit: int(receipt.Limits.Daily), TokenLimit: receipt.Limits.Tokens, TokenWindow: receipt.Limits.TokenWindow}
			allowed := receipt.Allowed == nil || *receipt.Allowed
			admission := budget.Admission{ProviderID: receipt.ProviderID, ModelID: receipt.ModelID, AttemptID: receipt.AttemptID, At: receipt.At, Day: receipt.Day, Month: receipt.Month, TokenWindow: receipt.TokenWindow, Policy: policy, Allowed: allowed}
			if !allowed {
				limit := receipt.Limits.Tokens
				if receipt.Reason == "rpm" {
					limit = receipt.Limits.RPM
				} else if receipt.Reason == "daily_requests" {
					limit = receipt.Limits.Daily
				}
				admission.Exhaustion = &budget.ExhaustionError{ProviderID: receipt.ProviderID, ModelID: receipt.ModelID, Kind: receipt.Reason, Limit: limit, Usage: receipt.Usage, ResetAt: receipt.ResetAt}
			}
			state.Attempts[id] = admission
		}
	}
	if copy.TokenEvents != nil {
		state.TokenEvents = make(map[string]budget.TokenEvent, len(copy.TokenEvents))
		for id, event := range copy.TokenEvents {
			state.TokenEvents[id] = budget.TokenEvent{ProviderID: event.ProviderID, AttemptID: event.AttemptID, EventID: event.EventID, Tokens: event.Tokens}
		}
	}
	return state
}

func budgetToDTO(state budget.State, previous *syncstate.BudgetState) *syncstate.BudgetState {
	state = state.Clone()
	dto := &syncstate.BudgetState{RPM: state.RPM, Daily: state.DailyRequests, Tokens: state.Tokens}
	if state.Attempts != nil {
		dto.Admissions = make(map[string]syncstate.AdmissionReceipt, len(state.Attempts))
		for id, admission := range state.Attempts {
			var receipt syncstate.AdmissionReceipt
			if previous != nil {
				receipt = previous.Admissions[id]
			}
			receipt.ProviderID, receipt.ModelID, receipt.AttemptID = admission.ProviderID, admission.ModelID, admission.AttemptID
			receipt.At, receipt.Day, receipt.Month, receipt.TokenWindow = admission.At, admission.Day, admission.Month, admission.TokenWindow
			receipt.Limits = syncstate.BudgetLimits{RPM: int64(admission.Policy.RPMLimit), Daily: int64(admission.Policy.DailyLimit), Tokens: admission.Policy.TokenLimit, TokenWindow: admission.Policy.TokenWindow}
			allowed := admission.Allowed
			receipt.Allowed = &allowed
			if admission.Exhaustion != nil {
				receipt.Reason, receipt.ResetAt, receipt.Usage = admission.Exhaustion.Kind, admission.Exhaustion.ResetAt, admission.Exhaustion.Usage
			}
			dto.Admissions[id] = receipt
		}
	}
	if state.TokenEvents != nil {
		dto.TokenEvents = make(map[string]syncstate.TokenEventReceipt, len(state.TokenEvents))
		for id, event := range state.TokenEvents {
			var receipt syncstate.TokenEventReceipt
			if previous != nil {
				receipt = previous.TokenEvents[id]
			}
			receipt.ProviderID, receipt.AttemptID, receipt.EventID, receipt.Tokens = event.ProviderID, event.AttemptID, event.EventID, event.Tokens
			dto.TokenEvents[id] = receipt
		}
	}
	return dto
}

func availabilityFromTable(table map[string]*providerRecheckState) providerstate.BackendSnapshot {
	state := providerstate.BackendSnapshot{}
	scopes := map[domain.AvailabilityScope]*providerstate.ScopeState{}
	getScope := func(scope domain.AvailabilityScope) *providerstate.ScopeState {
		if scopes[scope] == nil {
			scopes[scope] = &providerstate.ScopeState{Scope: scope}
		}
		return scopes[scope]
	}
	for _, provider := range table {
		if provider.Runtime == nil {
			continue
		}
		runtime := provider.Runtime
		state.Revision = max(state.Revision, runtime.ObservationRevision)
		for _, observation := range runtime.Observations {
			entry := getScope(observation.Observation.Scope)
			entry.Revision = max(entry.Revision, observation.Revision)
			entry.Observations = append(entry.Observations, observation.Observation)
		}
		for _, tombstone := range runtime.Tombstones {
			entry := getScope(tombstone.Scope)
			entry.Revision = max(entry.Revision, tombstone.Revision)
		}
		for id, receipt := range runtime.ObservationEvents {
			state.Events = append(state.Events, providerstate.EventReceipt{EventID: id, Observation: receipt.Observation})
		}
	}
	for _, scope := range scopes {
		sort.Slice(scope.Observations, func(i, j int) bool { return scope.Observations[i].Kind < scope.Observations[j].Kind })
		state.Scopes = append(state.Scopes, *scope)
	}
	sort.Slice(state.Scopes, func(i, j int) bool {
		a, b := state.Scopes[i].Scope, state.Scopes[j].Scope
		if a.ProviderID != b.ProviderID {
			return a.ProviderID < b.ProviderID
		}
		if a.CredentialID != b.CredentialID {
			return a.CredentialID < b.CredentialID
		}
		if a.ModelID != b.ModelID {
			return a.ModelID < b.ModelID
		}
		return a.EgressID < b.EgressID
	})
	sort.Slice(state.Events, func(i, j int) bool { return state.Events[i].EventID < state.Events[j].EventID })
	return state
}

func availabilityToTable(table map[string]*providerRecheckState, next providerstate.BackendSnapshot, at time.Time) {
	previous := make(map[string]syncstate.RuntimeState, len(table))
	for id, provider := range table {
		if provider.Runtime != nil {
			previous[id] = provider.Runtime.Clone()
			provider.Runtime.ObservationRevision = next.Revision
			provider.Runtime.Observations = nil
			provider.Runtime.Tombstones = nil
			provider.Runtime.ObservationEvents = nil
		}
	}
	for _, entry := range next.Scopes {
		providerID := string(entry.Scope.ProviderID)
		runtime := runtimePartition(table, providerID)
		runtime.ObservationRevision = next.Revision
		for _, observation := range entry.Observations {
			versioned := syncstate.VersionedObservation{Observation: observation, Revision: entry.Revision}
			for _, old := range previous[providerID].Observations {
				if old.Observation == observation {
					versioned = old
					break
				}
			}
			runtime.Observations = append(runtime.Observations, versioned)
		}
		tombstone := syncstate.ObservationTombstone{Scope: entry.Scope, Revision: entry.Revision, At: at}
		for _, old := range previous[providerID].Tombstones {
			if old.Scope != entry.Scope {
				continue
			}
			if old.Kind != "" {
				runtime.Tombstones = append(runtime.Tombstones, old)
			} else if old.Revision == entry.Revision {
				tombstone = old
			}
		}
		runtime.Tombstones = append(runtime.Tombstones, tombstone)
	}
	for _, event := range next.Events {
		providerID := string(event.Observation.Scope.ProviderID)
		runtime := runtimePartition(table, providerID)
		runtime.ObservationRevision = next.Revision
		if runtime.ObservationEvents == nil {
			runtime.ObservationEvents = map[string]syncstate.ObservationEventReceipt{}
		}
		receipt := previous[providerID].ObservationEvents[event.EventID]
		if receipt.EventID == "" {
			receipt = syncstate.ObservationEventReceipt{EventID: event.EventID, Scope: event.Observation.Scope, Kind: event.Observation.Kind, Revision: next.Revision, At: at}
		}
		receipt.Observation = event.Observation
		runtime.ObservationEvents[event.EventID] = receipt
	}
}

func (b *localAvailabilityBackend) Snapshot(ctx context.Context) (providerstate.BackendSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return providerstate.BackendSnapshot{}, err
	}
	provRecheckMu.Lock()
	defer provRecheckMu.Unlock()
	if err := b.owner.availableLocked(); err != nil {
		return providerstate.BackendSnapshot{}, err
	}
	state := availabilityFromTable(providerRecheckStat)
	state.Revision = b.owner.revision
	return state, nil
}

func (b *localAvailabilityBackend) CompareAndSwap(ctx context.Context, expected uint64, next providerstate.BackendSnapshot) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	provRecheckMu.Lock()
	defer provRecheckMu.Unlock()
	if err := b.owner.availableLocked(); err != nil {
		return false, err
	}
	if b.owner.revision != expected {
		return false, nil
	}
	if next.Revision != expected+1 || next.Revision == 0 {
		return false, providerstate.ErrConflict
	}
	table := cloneRuntimeTable()
	availabilityToTable(table, providerstate.CloneSnapshot(next), b.owner.now().UTC())
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := b.owner.saveLocked(table); err != nil {
		return false, err
	}
	providerRecheckStat = table
	b.owner.revision = next.Revision
	return true, nil
}
