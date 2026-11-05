package providerstate

import (
	"context"
	"testing"
	"time"

	"routenest/internal/domain"
)

func TestRunRecoveryRetriesFailedProbeThenRecovers(t *testing.T) {
	start := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: start}
	store := NewStore(clock.Now)
	scope := domain.AvailabilityScope{ProviderID: "cline", CredentialID: "cline:a1"}
	if err := store.Apply(stateObservation(domain.ObservationRateLimited, scope, start.Add(time.Minute))); err != nil {
		t.Fatal(err)
	}

	attempts := 0
	probe := func(context.Context, domain.AvailabilityScope) bool {
		attempts++
		return attempts == 2
	}

	clock.Advance(time.Minute)
	if got := RecoverDue(context.Background(), clock.Now(), store, 30*time.Second, probe); got != 0 {
		t.Fatalf("recoveries after failed probe = %d", got)
	}
	if reasons := store.Blocking(scope); len(reasons) != 1 {
		t.Fatalf("failed probe did not defer state: %+v", reasons)
	}

	clock.Advance(31 * time.Second)
	if got := RecoverDue(context.Background(), clock.Now(), store, 30*time.Second, probe); got != 1 {
		t.Fatalf("recoveries after successful probe = %d", got)
	}
	if reasons := store.Blocking(scope); len(reasons) != 0 {
		t.Fatalf("successful probe did not recover state: %+v", reasons)
	}
}

func TestDueScopesAreUniqueAndDeterministic(t *testing.T) {
	now := time.Now()
	store := NewStore(func() time.Time { return now })
	a := domain.AvailabilityScope{ProviderID: "a", ModelID: "m"}
	z := domain.AvailabilityScope{ProviderID: "z"}
	store.Apply(stateObservation(domain.ObservationRateLimited, z, now.Add(time.Minute)))
	store.Apply(stateObservation(domain.ObservationQuotaExhausted, a, now.Add(time.Minute)))
	store.Apply(stateObservation(domain.ObservationTransientFailure, a, now.Add(time.Minute)))

	due := store.DueScopes(now.Add(time.Minute))
	if len(due) != 2 || due[0] != a || due[1] != z {
		t.Fatalf("due scopes = %+v", due)
	}
}
