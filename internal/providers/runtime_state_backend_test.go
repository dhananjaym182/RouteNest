package providers

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"routenest/internal/budget"
	"routenest/internal/domain"
	"routenest/internal/providerstate"
	"routenest/internal/store/syncstate"
)

func isolateLocalRuntimeState(t *testing.T) (string, *time.Time) {
	t.Helper()
	provRecheckMu.Lock()
	oldPath, oldState, oldLoaded, oldOwner := providerSyncFilePath, providerRecheckStat, provRecheckLoaded, providerRuntimeStateOwner
	path := filepath.Join(t.TempDir(), syncstate.FileName)
	providerSyncFilePath, providerRecheckStat, provRecheckLoaded, providerRuntimeStateOwner = path, map[string]*providerRecheckState{}, false, nil
	provRecheckMu.Unlock()
	t.Cleanup(func() {
		provRecheckMu.Lock()
		providerSyncFilePath, providerRecheckStat, provRecheckLoaded, providerRuntimeStateOwner = oldPath, oldState, oldLoaded, oldOwner
		provRecheckMu.Unlock()
	})
	at := time.Date(2026, 11, 7, 7, 0, 0, 0, time.UTC)
	return path, &at
}

func localRuntimeFacets(t *testing.T, at *time.Time) (budget.Backend, providerstate.Backend, func() domain.RuntimeStateHealth) {
	t.Helper()
	b, a, health, err := NewLocalRuntimeStateBackend(func() time.Time { return *at })
	if err != nil {
		t.Fatal(err)
	}
	return b, a, health
}

func restartLocalRuntimeState() {
	provRecheckMu.Lock()
	providerRecheckStat, provRecheckLoaded, providerRuntimeStateOwner = map[string]*providerRecheckState{}, false, nil
	provRecheckMu.Unlock()
}

func runtimeObservation(scope domain.AvailabilityScope, kind domain.ObservationKind, until time.Time) domain.AvailabilityObservation {
	return domain.AvailabilityObservation{Scope: scope, Kind: kind, Until: until, Source: "upstream", Confidence: domain.ConfidenceExplicit}
}

func readRuntimeDocument(t *testing.T, path string) map[string]*syncstate.State {
	t.Helper()
	states, err := syncstate.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return states
}

func TestLocalRuntimeRestartPreservesAccountingAndCatalogue(t *testing.T) {
	path, at := isolateLocalRuntimeState(t)
	seed := syncstate.State{LastCheckedMs: 12, BatchID: "batch", DelistedModels: []string{}, Offerings: map[string]domain.ModelOfferingState{"m": {UpstreamModelID: "upstream-m", Cost: "free"}}}
	if err := syncstate.Save(path, map[string]syncstate.State{"p": seed}); err != nil {
		t.Fatal(err)
	}
	b, a, _ := localRuntimeFacets(t, at)
	command := budget.AdmissionCommand{ProviderID: "p", ModelID: "m", AttemptID: "admitted", At: *at, Policy: budget.Policy{DailyLimit: 1, TokenWindow: "daily"}}
	if _, err := b.CheckAndRecord(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if err := b.RecordTokens(context.Background(), budget.TokenEvent{ProviderID: "p", AttemptID: "admitted", EventID: "usage", Tokens: 9}); err != nil {
		t.Fatal(err)
	}
	denied := command
	denied.AttemptID = "denied"
	denial, err := b.CheckAndRecord(context.Background(), denied)
	if !errors.Is(err, budget.ErrExhausted) || denial.Allowed {
		t.Fatalf("denial = %+v, %v", denial, err)
	}
	store, err := providerstate.NewDurableStore(a, func() time.Time { return *at })
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.AvailabilityScope{ProviderID: "p", ModelID: "m"}
	if err := store.ApplyEvent("blocked", runtimeObservation(scope, domain.ObservationRateLimited, at.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	before, _ := b.Snapshot(context.Background(), "p")
	beforeAvailability, _ := a.Snapshot(context.Background())
	setProviderRecheckState("p", providerRecheckState{LastCheckedMs: 24, DelistedModels: []string{}})
	document := readRuntimeDocument(t, path)
	if document["p"].LastCheckedMs != 24 || !reflect.DeepEqual(document["p"].Offerings, seed.Offerings) {
		t.Fatalf("runtime write damaged catalogue: %+v", document["p"])
	}
	if got := budgetFromDTO(document["p"].Runtime.Budget); !reflect.DeepEqual(got, before) {
		t.Fatalf("catalogue write damaged budget: %+v", got)
	}
	restartLocalRuntimeState()
	b, a, _ = localRuntimeFacets(t, at)
	after, _ := b.Snapshot(context.Background(), "p")
	afterAvailability, _ := a.Snapshot(context.Background())
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(beforeAvailability, afterAvailability) {
		t.Fatalf("restart mismatch: budget=%+v, availability=%+v", after, afterAvailability)
	}
	if err := b.RecordTokens(context.Background(), budget.TokenEvent{ProviderID: "p", AttemptID: "admitted", EventID: "usage", Tokens: 9}); err != nil {
		t.Fatal(err)
	}
	if err := b.RecordTokens(context.Background(), budget.TokenEvent{ProviderID: "p", AttemptID: "admitted", EventID: "usage", Tokens: 10}); !errors.Is(err, budget.ErrIdempotency) {
		t.Fatalf("changed usage = %v", err)
	}
	got, _ := b.Snapshot(context.Background(), "p")
	if got.Tokens["2026-11-07"] != 9 {
		t.Fatalf("token replay double-counted: %+v", got.Tokens)
	}
	*at = at.Add(48 * time.Hour)
	restartLocalRuntimeState()
	b, _, _ = localRuntimeFacets(t, at)
	replay, err := b.CheckAndRecord(context.Background(), denied)
	if !errors.Is(err, budget.ErrExhausted) || !replay.Replay || !reflect.DeepEqual(replay.Exhaustion, denial.Exhaustion) {
		t.Fatalf("denied receipt lost after rollover: %+v, %v", replay, err)
	}
}

func TestLocalRuntimeFailedSaveRollsBackBothFacets(t *testing.T) {
	path, at := isolateLocalRuntimeState(t)
	b, a, health := localRuntimeFacets(t, at)
	command := budget.AdmissionCommand{ProviderID: "p", ModelID: "m", AttemptID: "first", At: *at}
	if _, err := b.CheckAndRecord(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	store, err := providerstate.NewDurableStore(a, func() time.Time { return *at })
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.AvailabilityScope{ProviderID: "p"}
	if err := store.ApplyEvent("first-block", runtimeObservation(scope, domain.ObservationAuthExpired, time.Time{})); err != nil {
		t.Fatal(err)
	}
	before, _ := b.Snapshot(context.Background(), "p")
	beforeAvailability, _ := a.Snapshot(context.Background())
	beforeFile, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	command.AttemptID = "failed-attempt"
	if _, err := b.CheckAndRecord(context.Background(), command); !errors.Is(err, budget.ErrUnavailable) {
		t.Fatalf("admission error = %v", err)
	}
	if err := b.RecordTokens(context.Background(), budget.TokenEvent{ProviderID: "p", AttemptID: "first", EventID: "failed-usage", Tokens: 4}); !errors.Is(err, budget.ErrUnavailable) {
		t.Fatalf("token write error = %v", err)
	}
	if err := store.Recover(scope); !errors.Is(err, budget.ErrUnavailable) {
		t.Fatalf("observation write error = %v", err)
	}
	setProviderRecheckState("p", providerRecheckState{LastCheckedMs: 999})
	after, _ := b.Snapshot(context.Background(), "p")
	afterAvailability, _ := a.Snapshot(context.Background())
	afterFile, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(beforeAvailability, afterAvailability) || string(beforeFile) != string(afterFile) {
		t.Fatalf("failed write published state: budget=%+v availability=%+v", after, afterAvailability)
	}
	if getProviderRecheckStateForTest("p").LastCheckedMs == 999 {
		t.Fatal("failed catalogue save published replacement")
	}
	if got := health(); got.Ready || got.SingleNodeReady || !got.Degraded || got.Reason != "persistence_failed" {
		t.Fatalf("failed-write health = %+v", got)
	}
	if err := os.Remove(path + ".tmp"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.CheckAndRecord(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if got := health(); !got.Ready || !got.SingleNodeReady || got.Degraded {
		t.Fatalf("recovered health = %+v", got)
	}
}

func TestLocalRuntimeTwoStoresTombstoneAndEventReplay(t *testing.T) {
	path, at := isolateLocalRuntimeState(t)
	_, a, _ := localRuntimeFacets(t, at)
	_, b, _ := localRuntimeFacets(t, at)
	first, err := providerstate.NewDurableStore(a, func() time.Time { return *at })
	if err != nil {
		t.Fatal(err)
	}
	second, err := providerstate.NewDurableStore(b, func() time.Time { return *at })
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.AvailabilityScope{ProviderID: "p", CredentialID: "credential", ModelID: "m"}
	observation := runtimeObservation(scope, domain.ObservationAuthExpired, time.Time{})
	if err := first.ApplyEvent("evidence", observation); err != nil {
		t.Fatal(err)
	}
	guard, err := second.Guard(scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Recover(scope); err != nil {
		t.Fatal(err)
	}
	if err := second.RecoverGuard(guard); !errors.Is(err, providerstate.ErrConflict) {
		t.Fatalf("stale guard = %v", err)
	}
	before, _ := a.Snapshot(context.Background())
	if len(before.Scopes) != 1 || len(before.Scopes[0].Observations) != 0 || before.Scopes[0].Revision <= guard.Revision {
		t.Fatalf("missing tombstone: %+v", before)
	}
	if len(readRuntimeDocument(t, path)["p"].Runtime.Tombstones) == 0 {
		t.Fatal("scope tombstone was not persisted")
	}
	restartLocalRuntimeState()
	_, a, _ = localRuntimeFacets(t, at)
	restarted, err := providerstate.NewDurableStore(a, func() time.Time { return *at })
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.ApplyEvent("evidence", observation); err != nil {
		t.Fatal(err)
	}
	if got := restarted.Blocking(scope); len(got) != 0 {
		t.Fatalf("replayed evidence resurrected blocker: %+v", got)
	}
	after, _ := a.Snapshot(context.Background())
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("replay changed tombstones/receipts: %+v", after)
	}
	observation.Source = "different"
	if err := restarted.ApplyEvent("evidence", observation); err == nil {
		t.Fatal("conflicting replay was accepted")
	}
}

func TestLocalRuntimeStartupPurgesBeforePublication(t *testing.T) {
	path, at := isolateLocalRuntimeState(t)
	scope := domain.AvailabilityScope{ProviderID: "p", ModelID: "expired"}
	observation := runtimeObservation(scope, domain.ObservationRateLimited, at.Add(-time.Minute))
	state := syncstate.State{Runtime: &syncstate.RuntimeState{SchemaVersion: 1, ObservationRevision: 12,
		Budget:            &syncstate.BudgetState{RPM: map[string][]time.Time{"m": {at.Add(-time.Minute), *at}}, Daily: map[string]int64{"2026-11-06": 99, "2026-11-07": 2}, Tokens: map[string]int64{"2026-10": 100, "2026-11": 4}},
		Observations:      []syncstate.VersionedObservation{{Observation: observation, Revision: 7}},
		ObservationEvents: map[string]syncstate.ObservationEventReceipt{"expired-evidence": {EventID: "expired-evidence", Scope: scope, Kind: observation.Kind, Observation: observation}},
	}}
	if err := syncstate.Save(path, map[string]syncstate.State{"p": state}); err != nil {
		t.Fatal(err)
	}
	b, a, health := localRuntimeFacets(t, at)
	accounting, _ := b.Snapshot(context.Background(), "p")
	availability, _ := a.Snapshot(context.Background())
	if len(accounting.RPM["m"]) != 1 || len(accounting.DailyRequests) != 1 || len(accounting.Tokens) != 1 {
		t.Fatalf("startup retained expired accounting: %+v", accounting)
	}
	if availability.Revision != 13 || len(availability.Scopes[0].Observations) != 0 || availability.Scopes[0].Revision != 8 || len(availability.Events) != 1 {
		t.Fatalf("startup lost revision or replay receipt: %+v", availability)
	}
	persisted := readRuntimeDocument(t, path)["p"].Runtime
	if len(persisted.Observations) != 0 || persisted.Tombstones[0].Revision != 8 || len(persisted.Budget.Daily) != 1 {
		t.Fatalf("purge was not durable: %+v", persisted)
	}
	if got := health(); !got.SingleNodeReady || got.LastSuccessfulWrite.IsZero() {
		t.Fatalf("startup health = %+v", got)
	}
}

func TestLocalRuntimeStartupStrictReadAndCorruptBackup(t *testing.T) {
	for _, mode := range []string{"corrupt", "null", "unreadable"} {
		t.Run(mode, func(t *testing.T) {
			path, at := isolateLocalRuntimeState(t)
			switch mode {
			case "unreadable":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "null":
				if err := os.WriteFile(path, []byte("null"), 0600); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 2; i++ {
				b, a, health, err := NewLocalRuntimeStateBackend(func() time.Time { return *at })
				if !errors.Is(err, budget.ErrUnavailable) || b != nil || a != nil || health != nil {
					t.Fatalf("unsafe startup %d: budget=%v availability=%v err=%v", i, b, a, err)
				}
				restartLocalRuntimeState()
			}
			LoadRecheckState()
			if provRecheckLoaded || len(providerRecheckStat) != 0 {
				t.Fatal("failed load published empty runtime state")
			}
			if mode == "unreadable" {
				if info, err := os.Stat(path); err != nil || !info.IsDir() {
					t.Fatalf("unreadable path was renamed: %v", err)
				}
			} else {
				backups, err := filepath.Glob(path + ".corrupt-*")
				if err != nil || len(backups) != 1 {
					t.Fatalf("corrupt backup = %v, %v", backups, err)
				}
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("corrupt path still present: %v", err)
				}
			}
		})
	}
}

func TestLocalRuntimeHealthIsRedactedAndMultiReplicaUnsupported(t *testing.T) {
	_, at := isolateLocalRuntimeState(t)
	b, a, status := localRuntimeFacets(t, at)
	for _, health := range []domain.RuntimeStateHealth{b.Health(), a.Health(), status()} {
		if health.Backend != "local_file" || !health.Ready || !health.SingleNodeReady || health.MultiReplicaSupported || health.LastSuccessfulRead.IsZero() {
			t.Fatalf("local health = %+v", health)
		}
		raw, err := json.Marshal(health)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), providerSyncFilePath) || strings.Contains(string(raw), ".json") {
			t.Fatalf("health leaked backend path: %s", raw)
		}
	}
}

func TestLocalRuntimeProviderDeletionRemovesWholePartition(t *testing.T) {
	path, at := isolateLocalRuntimeState(t)
	b, a, _ := localRuntimeFacets(t, at)
	if _, err := b.CheckAndRecord(context.Background(), budget.AdmissionCommand{ProviderID: "p", ModelID: "m", AttemptID: "attempt", At: *at}); err != nil {
		t.Fatal(err)
	}
	store, err := providerstate.NewDurableStore(a, func() time.Time { return *at })
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyEvent("block", runtimeObservation(domain.AvailabilityScope{ProviderID: "p"}, domain.ObservationAuthExpired, time.Time{})); err != nil {
		t.Fatal(err)
	}
	clearProviderRecheck("p")
	if _, exists := readRuntimeDocument(t, path)["p"]; exists {
		t.Fatal("deleted provider retained runtime partition")
	}
	if got, _ := b.Snapshot(context.Background(), "p"); len(got.Attempts) != 0 {
		t.Fatalf("deleted provider retained accounting: %+v", got)
	}
	if got, _ := a.Snapshot(context.Background()); len(got.Scopes) != 0 || len(got.Events) != 0 {
		t.Fatalf("deleted provider retained observations: %+v", got)
	}
}

func TestLocalRuntimeAtomicAdmissionAcrossFacets(t *testing.T) {
	_, at := isolateLocalRuntimeState(t)
	a, _, _ := localRuntimeFacets(t, at)
	b, _, _ := localRuntimeFacets(t, at)
	backends := []budget.Backend{a, b}
	var wg sync.WaitGroup
	results := make(chan error, 16)
	for i := 0; i < cap(results); i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := backends[i%2].CheckAndRecord(context.Background(), budget.AdmissionCommand{ProviderID: "p", ModelID: "m", AttemptID: string(rune('a' + i)), At: *at, Policy: budget.Policy{DailyLimit: 4}})
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	admitted, denied := 0, 0
	for err := range results {
		if err == nil {
			admitted++
		} else if errors.Is(err, budget.ErrExhausted) {
			denied++
		} else {
			t.Fatal(err)
		}
	}
	state, err := a.Snapshot(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	if admitted != 4 || denied != 12 || state.DailyRequests["2026-11-07"] != 4 || len(state.Attempts) != 16 {
		t.Fatalf("atomic admission admitted=%d denied=%d state=%+v", admitted, denied, state)
	}
}

func TestLocalRuntimeAvailabilityWritePreservesReceiptAndTombstoneMetadata(t *testing.T) {
	path, at := isolateLocalRuntimeState(t)
	scope := domain.AvailabilityScope{ProviderID: "p", ModelID: "m"}
	observation := runtimeObservation(scope, domain.ObservationAuthExpired, time.Time{})
	receipt := syncstate.ObservationEventReceipt{EventID: "old-event", Observation: observation, Scope: scope, Kind: observation.Kind, Revision: 4, At: at.Add(-time.Hour), Fingerprint: "opaque-receipt"}
	tombstone := syncstate.ObservationTombstone{Scope: scope, Kind: domain.ObservationRateLimited, Revision: 3, At: at.Add(-time.Hour)}
	seed := syncstate.State{LastCheckedMs: 123, Runtime: &syncstate.RuntimeState{SchemaVersion: 1, ObservationRevision: 4,
		Observations: []syncstate.VersionedObservation{{Observation: observation, Revision: 2}}, Tombstones: []syncstate.ObservationTombstone{tombstone, {Scope: scope, Revision: 4, At: at.Add(-time.Minute)}},
		ObservationEvents: map[string]syncstate.ObservationEventReceipt{"old-event": receipt}}}
	if err := syncstate.Save(path, map[string]syncstate.State{"p": seed}); err != nil {
		t.Fatal(err)
	}
	b, a, _ := localRuntimeFacets(t, at)
	if _, err := b.CheckAndRecord(context.Background(), budget.AdmissionCommand{ProviderID: "p", ModelID: "m", AttemptID: "attempt", At: *at}); err != nil {
		t.Fatal(err)
	}
	before := readRuntimeDocument(t, path)["p"].Runtime.Budget
	store, err := providerstate.NewDurableStore(a, func() time.Time { return *at })
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyEvent("new-event", runtimeObservation(domain.AvailabilityScope{ProviderID: "p", ModelID: "other"}, domain.ObservationTransientFailure, at.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	got := readRuntimeDocument(t, path)["p"]
	if !reflect.DeepEqual(got.Runtime.Budget, before) || got.LastCheckedMs != 123 {
		t.Fatalf("availability write damaged budget/catalogue: %+v", got)
	}
	if !reflect.DeepEqual(got.Runtime.ObservationEvents["old-event"], receipt) {
		t.Fatalf("receipt metadata changed: %+v", got.Runtime.ObservationEvents["old-event"])
	}
	if got.Runtime.Observations[0].Revision != 2 {
		t.Fatalf("unchanged evidence revision changed: %+v", got.Runtime.Observations)
	}
	found := false
	for _, candidate := range got.Runtime.Tombstones {
		if reflect.DeepEqual(candidate, tombstone) {
			found = true
		}
	}
	if !found {
		t.Fatalf("kind tombstone metadata lost: %+v", got.Runtime.Tombstones)
	}
}

func TestLegacyCooldownResetPersistsNormalizedRecovery(t *testing.T) {
	_, at := isolateLocalRuntimeState(t)
	*at = time.Now().UTC()
	_, availability, _ := localRuntimeFacets(t, at)
	store, err := providerstate.NewDurableStore(availability, func() time.Time { return *at })
	if err != nil {
		t.Fatal(err)
	}
	if err := providerstate.Publish(store); err != nil {
		t.Fatal(err)
	}

	Mu.Lock()
	oldProviders, oldLoaded, oldCooldowns, oldPath := CustomProviders, Loaded, Cooldowns, CooldownsPath
	CustomProviders = []*CustomProvider{{ID: "p", Name: "P"}}
	Loaded = true
	Cooldowns = map[string]time.Time{}
	CooldownsPath = filepath.Join(t.TempDir(), ".routenest-providers-cooldowns.json")
	Mu.Unlock()
	t.Cleanup(func() {
		Mu.Lock()
		CustomProviders, Loaded, Cooldowns, CooldownsPath = oldProviders, oldLoaded, oldCooldowns, oldPath
		Mu.Unlock()
	})

	scope := domain.AvailabilityScope{ProviderID: "p", ModelID: "m"}
	SetCooldown("p", "m", (*at).Add(time.Hour))
	if got := store.Blocking(scope); len(got) != 1 || got[0].Source != "legacy_provider_cooldown" {
		t.Fatalf("legacy cooldown was not normalized: %+v", got)
	}
	if removed := ResetCooldowns("p", "m"); removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}

	restartLocalRuntimeState()
	_, availability, _ = localRuntimeFacets(t, at)
	restarted, err := providerstate.NewDurableStore(availability, func() time.Time { return *at })
	if err != nil {
		t.Fatal(err)
	}
	if got := restarted.Blocking(scope); len(got) != 0 {
		t.Fatalf("reset observation resurrected after restart: %+v", got)
	}
}
