package providers

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"routenest/internal/budget"
	"routenest/internal/domain"
)

type testBudgetBackend struct {
	mu     sync.Mutex
	states map[string]budget.State
}

func newTestBudgetBackend() *testBudgetBackend {
	return &testBudgetBackend{states: map[string]budget.State{}}
}

func (b *testBudgetBackend) Snapshot(ctx context.Context, providerID string) (budget.State, error) {
	if err := ctx.Err(); err != nil {
		return budget.State{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.states[providerID].Clone(), nil
}

func (b *testBudgetBackend) CheckAndRecord(ctx context.Context, command budget.AdmissionCommand) (budget.Admission, error) {
	if err := ctx.Err(); err != nil {
		return budget.Admission{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.states[command.ProviderID].Clone()
	receipt, err := budget.Admit(&state, command)
	if err != nil && !errors.Is(err, budget.ErrExhausted) {
		return budget.Admission{}, err
	}
	b.states[command.ProviderID] = state
	return receipt, err
}

func (b *testBudgetBackend) RecordTokens(ctx context.Context, event budget.TokenEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.states[event.ProviderID].Clone()
	if err := budget.Account(&state, event); err != nil {
		return err
	}
	b.states[event.ProviderID] = state
	return nil
}

func (b *testBudgetBackend) Health() domain.RuntimeStateHealth {
	return domain.RuntimeStateHealth{Backend: "memory", Ready: true, SingleNodeReady: true, Enforcement: "durable"}
}

func installTestBudget(t *testing.T, now func() time.Time) *testBudgetBackend {
	t.Helper()
	if now == nil {
		now = time.Now
	}
	previous := budget.Default()
	backend := newTestBudgetBackend()
	if err := budget.Publish(budget.NewStore(backend, nil, budget.Options{Now: now})); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = budget.Publish(previous) })
	return backend
}

func admitTestUsage(t *testing.T, provider CustomProvider, model, attemptID, eventID string, at time.Time, tokens int64) {
	t.Helper()
	if _, err := AdmitDispatch(context.Background(), provider, model, attemptID, at); err != nil {
		t.Fatal(err)
	}
	if err := RecordTokenUsage(provider.ID, attemptID, eventID, tokens); err != nil {
		t.Fatal(err)
	}
}

func installProviders(t *testing.T, provs ...*CustomProvider) {
	t.Helper()
	tmp := t.TempDir()
	Mu.Lock()
	oldPath, oldCooldownsPath := FilePath, CooldownsPath
	oldProviders, oldLoaded, oldCooldowns := CustomProviders, Loaded, Cooldowns
	FilePath = filepath.Join(tmp, ".routenest-providers.json")
	CooldownsPath = filepath.Join(tmp, ".routenest-providers-cooldowns.json")
	CustomProviders = append([]*CustomProvider(nil), provs...)
	Loaded = true
	Cooldowns = map[string]time.Time{}
	Mu.Unlock()
	provRecheckMu.Lock()
	oldSyncPath, oldSyncState, oldOwner := providerSyncFilePath, providerRecheckStat, providerRuntimeStateOwner
	oldMiss, oldSyncLoaded := provRecheckMiss, provRecheckLoaded
	providerSyncFilePath = filepath.Join(tmp, ".routenest-providers-sync.json")
	providerRecheckStat = map[string]*providerRecheckState{}
	provRecheckMiss = map[string]map[string]int{}
	provRecheckLoaded = true
	providerRuntimeStateOwner = nil
	provRecheckMu.Unlock()
	t.Cleanup(func() {
		Mu.Lock()
		FilePath, CooldownsPath = oldPath, oldCooldownsPath
		CustomProviders, Loaded, Cooldowns = oldProviders, oldLoaded, oldCooldowns
		Mu.Unlock()
		provRecheckMu.Lock()
		providerSyncFilePath, providerRecheckStat, providerRuntimeStateOwner = oldSyncPath, oldSyncState, oldOwner
		provRecheckMiss, provRecheckLoaded = oldMiss, oldSyncLoaded
		provRecheckMu.Unlock()
	})
}
