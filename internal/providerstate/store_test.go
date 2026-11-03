package providerstate

import (
	"sync"
	"testing"
	"time"

	"routenest/internal/domain"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
}

func stateObservation(kind domain.ObservationKind, scope domain.AvailabilityScope, until time.Time) domain.AvailabilityObservation {
	return domain.AvailabilityObservation{
		Kind: kind, Scope: scope, Until: until,
		Source: "test", Confidence: domain.ConfidenceExplicit,
	}
}

func TestStoreKeepsAvailabilityScopesIndependent(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: now}
	store := NewStore(clock.Now)
	provider := domain.AvailabilityScope{ProviderID: "p"}
	credential := domain.AvailabilityScope{ProviderID: "p", CredentialID: "c"}
	offering := domain.AvailabilityScope{ProviderID: "p", ModelID: "m"}
	egress := domain.AvailabilityScope{ProviderID: "p", EgressID: "proxy-1"}
	for _, scope := range []domain.AvailabilityScope{provider, credential, offering, egress} {
		if err := store.Apply(stateObservation(domain.ObservationRateLimited, scope, now.Add(time.Hour))); err != nil {
			t.Fatal(err)
		}
	}

	candidate := domain.AvailabilityScope{ProviderID: "p", CredentialID: "c", ModelID: "m", EgressID: "proxy-1"}
	reasons := store.Blocking(candidate)
	if len(reasons) != 4 {
		t.Fatalf("blocking reasons = %d, want 4: %+v", len(reasons), reasons)
	}

	if err := store.Recover(credential); err != nil {
		t.Fatal(err)
	}
	reasons = store.Blocking(candidate)
	if len(reasons) != 3 {
		t.Fatalf("credential recovery cleared unrelated scopes: %+v", reasons)
	}
	for _, reason := range reasons {
		if reason.Scope == credential {
			t.Fatalf("credential scope still blocked: %+v", reasons)
		}
	}
}

func TestStoreExpiresCooldownsLazilyButKeepsPermanentState(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: now}
	store := NewStore(clock.Now)
	scope := domain.AvailabilityScope{ProviderID: "p", CredentialID: "c"}
	if err := store.Apply(stateObservation(domain.ObservationRateLimited, scope, now.Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
	if err := store.Apply(stateObservation(domain.ObservationAuthExpired, scope, time.Time{})); err != nil {
		t.Fatal(err)
	}
	clock.Advance(2 * time.Minute)

	reasons := store.Blocking(scope)
	if len(reasons) != 1 || reasons[0].Kind != domain.ObservationAuthExpired {
		t.Fatalf("lazy expiry result = %+v", reasons)
	}
}

func TestStoreUsesConfirmedQuotaResetAsExpiry(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: now}
	store := NewStore(clock.Now)
	scope := domain.AvailabilityScope{ProviderID: "p", ModelID: "m"}
	obs := stateObservation(domain.ObservationQuotaExhausted, scope, now.Add(2*time.Hour))
	obs.ResetAt = now.Add(30 * time.Minute)
	if err := store.Apply(obs); err != nil {
		t.Fatal(err)
	}
	clock.Advance(31 * time.Minute)
	if reasons := store.Blocking(scope); len(reasons) != 0 {
		t.Fatalf("confirmed quota reset did not expire state: %+v", reasons)
	}
}

func TestStoreRecoveryObservationClearsOnlyExactScope(t *testing.T) {
	now := time.Now()
	store := NewStore(func() time.Time { return now })
	provider := domain.AvailabilityScope{ProviderID: "p"}
	model := domain.AvailabilityScope{ProviderID: "p", ModelID: "m"}
	store.Apply(stateObservation(domain.ObservationTransientFailure, provider, now.Add(time.Hour)))
	store.Apply(stateObservation(domain.ObservationQuotaExhausted, model, now.Add(time.Hour)))

	recovered := stateObservation(domain.ObservationRecovered, model, time.Time{})
	if err := store.Apply(recovered); err != nil {
		t.Fatal(err)
	}
	if got := store.Blocking(model); len(got) != 1 || got[0].Scope != provider {
		t.Fatalf("recovery cleared wrong states: %+v", got)
	}
}

func TestQuotaResetClearsQuotaButKeepsOtherStateAtSameScope(t *testing.T) {
	now := time.Now()
	store := NewStore(func() time.Time { return now })
	scope := domain.AvailabilityScope{ProviderID: "p", ModelID: "m"}
	store.Apply(stateObservation(domain.ObservationRateLimited, scope, now.Add(time.Hour)))
	store.Apply(stateObservation(domain.ObservationQuotaExhausted, scope, now.Add(time.Hour)))
	reset := stateObservation(domain.ObservationQuotaReset, scope, time.Time{})
	if err := store.Apply(reset); err != nil {
		t.Fatal(err)
	}
	reasons := store.Blocking(scope)
	if len(reasons) != 1 || reasons[0].Kind != domain.ObservationRateLimited {
		t.Fatalf("quota reset cleared unrelated state: %+v", reasons)
	}
}

func TestStoreSnapshotIsDeterministicAndIndependent(t *testing.T) {
	now := time.Now()
	store := NewStore(func() time.Time { return now })
	store.Apply(stateObservation(domain.ObservationRateLimited, domain.AvailabilityScope{ProviderID: "z"}, now.Add(time.Hour)))
	store.Apply(stateObservation(domain.ObservationRateLimited, domain.AvailabilityScope{ProviderID: "a", ModelID: "m"}, now.Add(time.Hour)))

	first := store.Snapshot("")
	if len(first) != 2 || first[0].Scope.ProviderID != "a" || first[1].Scope.ProviderID != "z" {
		t.Fatalf("snapshot order = %+v", first)
	}
	first[0].Source = "mutated"
	if got := store.Snapshot("")[0].Source; got != "test" {
		t.Fatalf("snapshot aliases store: source=%q", got)
	}
}

func TestStoreConcurrentApplyAndSnapshot(t *testing.T) {
	store := NewStore(time.Now)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = store.Apply(stateObservation(domain.ObservationRateLimited, domain.AvailabilityScope{ProviderID: "p", ModelID: "m"}, time.Now().Add(time.Minute)))
		}()
		go func() {
			defer wg.Done()
			_ = store.Snapshot("p")
		}()
	}
	wg.Wait()
}

func TestPublishAndDefaultStore(t *testing.T) {
	store := NewStore(time.Now)
	if err := Publish(store); err != nil {
		t.Fatal(err)
	}
	if !HasDefault() || Default() != store {
		t.Fatal("published store is not the default")
	}
	if err := Publish(nil); err == nil {
		t.Fatal("publishing nil store must fail")
	}
}
