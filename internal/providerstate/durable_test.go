package providerstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"routenest/internal/domain"
)

// sharedAvailabilityBackend serializes every client through one mutex and JSON
// round-trips commits to model a real durable adapter's value representation.
type sharedAvailabilityBackend struct {
	mu       sync.Mutex
	state    BackendSnapshot
	readErr  error
	writeErr error
	commits  int
}

func (b *sharedAvailabilityBackend) Snapshot(ctx context.Context) (BackendSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return BackendSnapshot{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.readErr != nil {
		return BackendSnapshot{}, b.readErr
	}
	return CloneSnapshot(b.state), nil
}

func (b *sharedAvailabilityBackend) CompareAndSwap(ctx context.Context, expected uint64, next BackendSnapshot) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.writeErr != nil {
		return false, b.writeErr
	}
	if b.state.Revision != expected {
		return false, nil
	}
	if next.Revision != expected+1 {
		return false, fmt.Errorf("invalid next revision")
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return false, err
	}
	var stored BackendSnapshot
	if err := json.Unmarshal(raw, &stored); err != nil {
		return false, err
	}
	b.state = stored
	b.commits++
	return true, nil
}

func (b *sharedAvailabilityBackend) Health() domain.RuntimeStateHealth {
	b.mu.Lock()
	defer b.mu.Unlock()
	ready := b.readErr == nil && b.writeErr == nil
	return domain.RuntimeStateHealth{Backend: "shared_test", Ready: ready, SingleNodeReady: ready, MultiReplicaSupported: true, Enforcement: "durable"}
}

func (b *sharedAvailabilityBackend) failReads(err error) { b.mu.Lock(); b.readErr = err; b.mu.Unlock() }
func (b *sharedAvailabilityBackend) failWrites(err error) {
	b.mu.Lock()
	b.writeErr = err
	b.mu.Unlock()
}
func (b *sharedAvailabilityBackend) commitCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.commits
}

func durableStore(t *testing.T, b Backend, clock *fakeClock) *Store {
	t.Helper()
	store, err := NewDurableStore(b, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestDurableRestartExpiryAndExactRemoval(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)}
	backend := &sharedAvailabilityBackend{}
	original := durableStore(t, backend, clock)
	provider := domain.AvailabilityScope{ProviderID: "p"}
	credential := domain.AvailabilityScope{ProviderID: "p", CredentialID: "c"}
	if err := original.Apply(stateObservation(domain.ObservationRateLimited, provider, clock.Now().Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
	if err := original.Apply(stateObservation(domain.ObservationAuthExpired, credential, time.Time{})); err != nil {
		t.Fatal(err)
	}
	restarted := durableStore(t, backend, clock)
	if got := restarted.Blocking(credential); len(got) != 2 {
		t.Fatalf("restart lost observations: %+v", got)
	}
	clock.Advance(time.Minute)
	restarted = durableStore(t, backend, clock)
	if got := restarted.Blocking(credential); len(got) != 1 || got[0].Kind != domain.ObservationAuthExpired {
		t.Fatalf("restart expiry: %+v", got)
	}
	state, err := backend.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range state.Scopes {
		if scope.Scope == provider && len(scope.Observations) != 0 {
			t.Fatal("expiry was not persisted")
		}
	}
	if err := restarted.Recover(credential); err != nil {
		t.Fatal(err)
	}
	if got := durableStore(t, backend, clock).Snapshot(""); len(got) != 0 {
		t.Fatalf("removal did not survive restart: %+v", got)
	}
}

func TestDurableEventsReplayAcrossRestartAndRecovery(t *testing.T) {
	// time.Now carries monotonic clock metadata, which JSON deliberately drops.
	clock := &fakeClock{now: time.Now()}
	backend := &sharedAvailabilityBackend{}
	scope := domain.AvailabilityScope{ProviderID: "p", ModelID: "m"}
	observation := stateObservation(domain.ObservationRateLimited, scope, clock.Now().Add(time.Hour))
	store := durableStore(t, backend, clock)
	if err := store.ApplyEvent("attempt-1", observation); err != nil {
		t.Fatal(err)
	}
	firstGuard, err := store.Guard(scope)
	if err != nil {
		t.Fatal(err)
	}
	commits := backend.commitCount()
	restarted := durableStore(t, backend, clock)
	if err := restarted.ApplyEvent("attempt-1", observation); err != nil {
		t.Fatalf("semantic replay after JSON round trip: %v", err)
	}
	if got := backend.commitCount(); got != commits {
		t.Fatalf("replay wrote state: %d -> %d", commits, got)
	}
	replayGuard, err := restarted.Guard(scope)
	if err != nil || replayGuard != firstGuard {
		t.Fatalf("replay changed scope revision: %+v %v", replayGuard, err)
	}
	if err := restarted.RecoverGuard(firstGuard); err != nil {
		t.Fatal(err)
	}
	if err := durableStore(t, backend, clock).ApplyEvent("attempt-1", observation); err != nil {
		t.Fatal(err)
	}
	if got := store.Blocking(scope); len(got) != 0 {
		t.Fatalf("old event resurrected recovered evidence: %+v", got)
	}
	observation.Source = "different"
	if err := store.ApplyEvent("attempt-1", observation); err == nil {
		t.Fatal("event ID collision accepted")
	}
}

func TestTwoDurableStoresSeeAuthoritativeStateAndGuardExactScopes(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)}
	backend := &sharedAvailabilityBackend{}
	a, b := durableStore(t, backend, clock), durableStore(t, backend, clock)
	scope := domain.AvailabilityScope{ProviderID: "p", ModelID: "m"}
	broad := domain.AvailabilityScope{ProviderID: "p"}
	observation := stateObservation(domain.ObservationRateLimited, scope, clock.Now().Add(time.Hour))
	if err := a.Apply(observation); err != nil {
		t.Fatal(err)
	}
	if got := b.Blocking(scope); len(got) != 1 {
		t.Fatalf("second store used old cache: %+v", got)
	}
	guard, err := b.Guard(scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Apply(stateObservation(domain.ObservationAuthExpired, broad, time.Time{})); err != nil {
		t.Fatal(err)
	}
	if err := b.RecoverGuard(guard); err != nil {
		t.Fatalf("broader scope invalidated exact guard: %v", err)
	}
	if got := a.Blocking(scope); len(got) != 1 || got[0].Scope != broad {
		t.Fatalf("shared recovery cleared wrong scope: %+v", got)
	}
	if err := a.Apply(observation); err != nil {
		t.Fatal(err)
	}
	if err := b.RecoverGuard(guard); !errors.Is(err, ErrConflict) {
		t.Fatalf("tombstone revision lost: %v", err)
	}
	if !b.Health().Ready {
		t.Fatal("healthy revision conflict reported backend outage")
	}
}

func TestDurableConcurrentEventsKeepAllScopesAndDeduplicate(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)}
	backend := &sharedAvailabilityBackend{}
	stores := []*Store{durableStore(t, backend, clock), durableStore(t, backend, clock)}
	var wg sync.WaitGroup
	failures := make(chan error, 40)
	for i := 0; i < 20; i++ {
		scope := domain.AvailabilityScope{ProviderID: "p", ModelID: fmt.Sprintf("m%d", i)}
		observation := stateObservation(domain.ObservationRateLimited, scope, clock.Now().Add(time.Hour))
		for _, store := range stores {
			wg.Add(1)
			go func(store *Store, event string, observation domain.AvailabilityObservation) {
				defer wg.Done()
				if err := store.ApplyEvent(event, observation); err != nil {
					failures <- err
				}
			}(store, fmt.Sprintf("event-%d", i), observation)
		}
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	state, err := backend.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Events) != 20 || len(stores[0].Snapshot("p")) != 20 || backend.commitCount() != 20 {
		t.Fatalf("lost or duplicated shared mutations: events=%d scopes=%d commits=%d", len(state.Events), len(state.Scopes), backend.commitCount())
	}
}

func TestDurableDeferAndQuotaResetPersist(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)}
	backend := &sharedAvailabilityBackend{}
	store := durableStore(t, backend, clock)
	scope := domain.AvailabilityScope{ProviderID: "p", ModelID: "m"}
	quota := stateObservation(domain.ObservationQuotaExhausted, scope, clock.Now().Add(2*time.Hour))
	quota.ResetAt = clock.Now().Add(time.Minute)
	if err := store.Apply(quota); err != nil {
		t.Fatal(err)
	}
	if err := store.Apply(stateObservation(domain.ObservationAuthExpired, scope, time.Time{})); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	guard, err := store.Guard(scope)
	if err != nil {
		t.Fatal(err)
	}
	until := clock.Now().Add(time.Hour)
	if err := store.DeferGuard(guard, until); err != nil {
		t.Fatal(err)
	}
	reasons := durableStore(t, backend, clock).Snapshot("p")
	var deferred domain.AvailabilityObservation
	for _, reason := range reasons {
		if reason.Kind == domain.ObservationQuotaExhausted {
			deferred = reason
		}
	}
	if !deferred.ResetAt.IsZero() || !deferred.Until.Equal(until) || deferred.Source != "recovery_probe" {
		t.Fatalf("deferred reset precedence: %+v", deferred)
	}
	if err := store.ApplyEvent("quota-reset", stateObservation(domain.ObservationQuotaReset, scope, time.Time{})); err != nil {
		t.Fatal(err)
	}
	if got := durableStore(t, backend, clock).Blocking(scope); len(got) != 1 || got[0].Kind != domain.ObservationAuthExpired {
		t.Fatalf("quota reset persistence cleared unrelated state: %+v", got)
	}
}

func TestRecoveryRejectsNewerEvidenceAndDoesNotCountFailedPersistence(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)}
	backend := &sharedAvailabilityBackend{}
	a, b := durableStore(t, backend, clock), durableStore(t, backend, clock)
	scope := domain.AvailabilityScope{ProviderID: "p"}
	applyDue := func() {
		t.Helper()
		if err := a.Apply(stateObservation(domain.ObservationRateLimited, scope, clock.Now().Add(time.Minute))); err != nil {
			t.Fatal(err)
		}
		clock.Advance(time.Minute)
	}
	applyDue()
	got, err := RecoverDueWithError(context.Background(), clock.Now(), a, time.Minute, func(context.Context, domain.AvailabilityScope) bool {
		if err := b.Apply(stateObservation(domain.ObservationAuthExpired, scope, time.Time{})); err != nil {
			t.Fatal(err)
		}
		return true
	})
	if got != 0 || !errors.Is(err, ErrConflict) {
		t.Fatalf("slow recovery count=%d err=%v", got, err)
	}
	if got := b.Blocking(scope); len(got) != 1 || got[0].Kind != domain.ObservationAuthExpired {
		t.Fatalf("slow recovery cleared new evidence: %+v", got)
	}
	applyDue()
	failure := errors.New("write failed at secret-proxy-url")
	got, err = RecoverDueWithError(context.Background(), clock.Now(), a, time.Minute, func(context.Context, domain.AvailabilityScope) bool { backend.failWrites(failure); return true })
	if got != 0 || !errors.Is(err, failure) {
		t.Fatalf("failed write reported recovery count=%d err=%v", got, err)
	}
	if !errors.Is(a.LastError(), failure) {
		t.Fatalf("persistence error swallowed: %v", a.LastError())
	}
	health := a.Health()
	raw, _ := json.Marshal(health)
	if health.Ready || !health.Degraded || !health.Stale || strings.Contains(string(raw), "secret") {
		t.Fatalf("unsafe backend health: %s", raw)
	}
	backend.failWrites(nil)
	if got := durableStore(t, backend, clock).Blocking(scope); len(got) != 1 || got[0].Kind != domain.ObservationAuthExpired {
		t.Fatalf("failed recovery write changed durable permanent state: %+v", got)
	}
}

func TestFailedProbeCannotDeferNewerEvidence(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)}
	backend := &sharedAvailabilityBackend{}
	a, b := durableStore(t, backend, clock), durableStore(t, backend, clock)
	scope := domain.AvailabilityScope{ProviderID: "p"}
	if err := a.Apply(stateObservation(domain.ObservationQuotaExhausted, scope, clock.Now().Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	replacement := stateObservation(domain.ObservationQuotaExhausted, scope, clock.Now().Add(time.Hour))
	got, err := RecoverDueWithError(context.Background(), clock.Now(), a, time.Minute, func(context.Context, domain.AvailabilityScope) bool {
		if err := b.Apply(replacement); err != nil {
			t.Fatal(err)
		}
		return false
	})
	if got != 0 || !errors.Is(err, ErrConflict) {
		t.Fatalf("slow defer count=%d err=%v", got, err)
	}
	if got := a.Blocking(scope); len(got) != 1 || !sameObservation(got[0], replacement) {
		t.Fatalf("failed probe rewrote newer evidence: %+v", got)
	}
}

func TestDurableConstructorAndFailedWritesDoNotPublishEmptyState(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)}
	backend := &sharedAvailabilityBackend{}
	failure := errors.New("backend unavailable")
	backend.failReads(failure)
	if store, err := NewDurableStore(backend, clock.Now); store != nil || !errors.Is(err, failure) {
		t.Fatalf("failed load created store: %v %v", store, err)
	}
	backend.failReads(nil)
	store := durableStore(t, backend, clock)
	scope := domain.AvailabilityScope{ProviderID: "p"}
	backend.failWrites(failure)
	if err := store.ApplyEvent("not-committed", stateObservation(domain.ObservationAuthExpired, scope, time.Time{})); !errors.Is(err, failure) {
		t.Fatalf("failed write = %v", err)
	}
	backend.failWrites(nil)
	if got := store.Snapshot("p"); len(got) != 0 {
		t.Fatalf("failed write poisoned cache: %+v", got)
	}
	if err := store.ApplyEvent("not-committed", stateObservation(domain.ObservationAuthExpired, scope, time.Time{})); err != nil {
		t.Fatalf("failed event incorrectly received receipt: %v", err)
	}
	if err := store.Apply(stateObservation(domain.ObservationRateLimited, scope, clock.Now().Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	backend.failWrites(failure)
	if reopened, err := NewDurableStore(backend, clock.Now); reopened != nil || !errors.Is(err, failure) {
		t.Fatalf("failed startup expiry created store: %v %v", reopened, err)
	}
	backend.failWrites(nil)
	if got := durableStore(t, backend, clock).Snapshot("p"); len(got) != 1 || got[0].Kind != domain.ObservationAuthExpired {
		t.Fatalf("failed expiry damaged durable state: %+v", got)
	}
}

func TestLegacyRecoveryCountSurfacesWriteFailure(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)}
	backend := &sharedAvailabilityBackend{}
	store := durableStore(t, backend, clock)
	scope := domain.AvailabilityScope{ProviderID: "p"}
	if err := store.Apply(stateObservation(domain.ObservationRateLimited, scope, clock.Now().Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	failure := errors.New("write unavailable")
	got := RecoverDue(context.Background(), clock.Now(), store, time.Minute, func(context.Context, domain.AvailabilityScope) bool { backend.failWrites(failure); return true })
	if got != 0 || !errors.Is(store.LastError(), failure) {
		t.Fatalf("legacy recovery swallowed failure: count=%d error=%v", got, store.LastError())
	}
	state, err := backend.Snapshot(context.Background())
	if err != nil || len(state.Scopes[0].Observations) != 1 {
		t.Fatalf("failed recovery changed backend: %+v %v", state, err)
	}
}

// laggingAvailabilityBackend deterministically returns one old snapshot, forcing
// a real CAS conflict after another Store has committed to the shared backend.
type laggingAvailabilityBackend struct {
	Backend
	mu    sync.Mutex
	stale *BackendSnapshot
}

func (b *laggingAvailabilityBackend) Snapshot(ctx context.Context) (BackendSnapshot, error) {
	b.mu.Lock()
	if b.stale != nil {
		state := CloneSnapshot(*b.stale)
		b.stale = nil
		b.mu.Unlock()
		return state, nil
	}
	b.mu.Unlock()
	return b.Backend.Snapshot(ctx)
}

func TestCASRetryPreservesConcurrentEvidenceAndOneEventReceipt(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)}
	shared := &sharedAvailabilityBackend{}
	lagging := &laggingAvailabilityBackend{Backend: shared}
	a, b := durableStore(t, lagging, clock), durableStore(t, shared, clock)
	before, err := shared.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	other := domain.AvailabilityScope{ProviderID: "p", ModelID: "other"}
	if err := b.ApplyEvent("other", stateObservation(domain.ObservationAuthExpired, other, time.Time{})); err != nil {
		t.Fatal(err)
	}
	lagging.mu.Lock()
	lagging.stale = &before
	lagging.mu.Unlock()
	scope := domain.AvailabilityScope{ProviderID: "p", ModelID: "mine"}
	if err := a.ApplyEvent("mine", stateObservation(domain.ObservationRateLimited, scope, clock.Now().Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	state, err := shared.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Events) != 2 || shared.commitCount() != 2 || len(a.Snapshot("p")) != 2 {
		t.Fatalf("CAS retry lost evidence or duplicated receipt: %+v", state)
	}
}

func TestDurableReadFailureExposesLastGoodCacheAndRejectsMutation(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)}
	backend := &sharedAvailabilityBackend{}
	store := durableStore(t, backend, clock)
	scope := domain.AvailabilityScope{ProviderID: "p"}
	if err := store.Apply(stateObservation(domain.ObservationAuthExpired, scope, time.Time{})); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("backend read unavailable")
	backend.failReads(failure)
	cached, err := store.SnapshotWithError(context.Background(), "p")
	if len(cached) != 1 || !errors.Is(err, failure) || !store.Health().Stale {
		t.Fatalf("read failure cache=%+v err=%v health=%+v", cached, err, store.Health())
	}
	if err := store.Recover(scope); !errors.Is(err, failure) {
		t.Fatalf("cache authorized mutation: %v", err)
	}
	backend.failReads(nil)
	if got := store.Blocking(scope); len(got) != 1 || !store.Health().Ready || store.LastError() != nil {
		t.Fatalf("read restoration lost evidence: %+v health=%+v err=%v", got, store.Health(), store.LastError())
	}
}

func TestRecoveryGuardsCapturedBeforeEarlierProbeChangesLaterScope(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)}
	backend := &sharedAvailabilityBackend{}
	a, b := durableStore(t, backend, clock), durableStore(t, backend, clock)
	first := domain.AvailabilityScope{ProviderID: "a"}
	later := domain.AvailabilityScope{ProviderID: "z"}
	for _, scope := range []domain.AvailabilityScope{first, later} {
		if err := a.Apply(stateObservation(domain.ObservationRateLimited, scope, clock.Now().Add(time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(time.Minute)
	count, err := RecoverDueWithError(context.Background(), clock.Now(), a, time.Minute, func(_ context.Context, scope domain.AvailabilityScope) bool {
		if scope == first {
			if err := b.Apply(stateObservation(domain.ObservationAuthExpired, later, time.Time{})); err != nil {
				t.Fatal(err)
			}
		}
		return true
	})
	// Applying new evidence also purges first's expired observation, so even its
	// initial guard conflicts. Neither probe can claim a durable recovery.
	if count != 0 || !errors.Is(err, ErrConflict) {
		t.Fatalf("due selection recaptured later evidence: count=%d err=%v", count, err)
	}
	if got := a.Blocking(later); len(got) != 1 || got[0].Kind != domain.ObservationAuthExpired {
		t.Fatalf("later probe cleared newly selected evidence: %+v", got)
	}
}
