package credentials

import (
	"strings"
	"testing"
	"time"

	"routenest/internal/domain"
	"routenest/internal/providerstate"
)

type staticSource struct {
	name        string
	credentials []domain.ProviderCredential
}

func (s staticSource) Name() string                          { return s.name }
func (s staticSource) Snapshot() []domain.ProviderCredential { return s.credentials }

func TestRegistryReturnsDeterministicDeepRedactedSnapshots(t *testing.T) {
	now := time.Now()
	store := providerstate.NewStore(func() time.Time { return now })
	reg, err := NewRegistry(store,
		staticSource{name: "accounts", credentials: []domain.ProviderCredential{{
			ID: "cline:a1", ProviderIDs: []domain.ProviderInstanceID{"cline"}, Kind: domain.CredentialOAuthAccount,
			Subject: "a@example.com", Status: "active", ModelCooldowns: map[string]time.Time{"m": now.Add(time.Hour)},
		}}},
		staticSource{name: "keys", credentials: []domain.ProviderCredential{{
			ID: "api-key:p", ProviderIDs: []domain.ProviderInstanceID{"p"}, Kind: domain.CredentialAPIKey,
			Status: "active", SecretConfigured: true,
		}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	first, err := reg.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].ID != "api-key:p" || first[1].ID != "cline:a1" {
		t.Fatalf("snapshot order = %+v", first)
	}
	first[1].ProviderIDs[0] = "mutated"
	first[1].ModelCooldowns["m"] = time.Time{}
	second, _ := reg.Snapshot()
	if second[1].ProviderIDs[0] != "cline" || second[1].ModelCooldowns["m"].IsZero() {
		t.Fatalf("snapshot aliases source data: %+v", second[1])
	}
}

func TestRegistryRejectsDuplicateSourcesAndCredentials(t *testing.T) {
	store := providerstate.NewStore(time.Now)
	if _, err := NewRegistry(store, staticSource{name: "same"}, staticSource{name: "same"}); err == nil || !strings.Contains(err.Error(), "source") {
		t.Fatalf("duplicate source error = %v", err)
	}
	reg, err := NewRegistry(store,
		staticSource{name: "one", credentials: []domain.ProviderCredential{{ID: "duplicate", Kind: domain.CredentialAPIKey, Status: "active"}}},
		staticSource{name: "two", credentials: []domain.ProviderCredential{{ID: "duplicate", Kind: domain.CredentialAPIKey, Status: "active"}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Snapshot(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate credential error = %v", err)
	}
}

func TestEligibleFiltersCredentialAndProviderState(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	store := providerstate.NewStore(func() time.Time { return now })
	source := staticSource{name: "test", credentials: []domain.ProviderCredential{
		{ID: "active", ProviderIDs: []domain.ProviderInstanceID{"p"}, Kind: domain.CredentialOAuthAccount, Status: "active"},
		{ID: "model-cooling", ProviderIDs: []domain.ProviderInstanceID{"p"}, Kind: domain.CredentialOAuthAccount, Status: "active", ModelCooldowns: map[string]time.Time{"m": now.Add(time.Hour)}},
		{ID: "reauth", ProviderIDs: []domain.ProviderInstanceID{"p"}, Kind: domain.CredentialOAuthAccount, Status: "reauth_required"},
		{ID: "expired", ProviderIDs: []domain.ProviderInstanceID{"p"}, Kind: domain.CredentialOAuthAccount, Status: "active", ExpiresAt: now.Add(-time.Minute)},
		{ID: "refreshable", ProviderIDs: []domain.ProviderInstanceID{"p"}, Kind: domain.CredentialOAuthAccount, Status: "active", ExpiresAt: now.Add(-time.Minute), Refreshable: true},
	}}
	reg, err := NewRegistry(store, source)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Apply(domain.AvailabilityObservation{
		Kind:  domain.ObservationRateLimited,
		Scope: domain.AvailabilityScope{ProviderID: "p", CredentialID: "active", ModelID: "m"},
		Until: now.Add(time.Hour), Source: "test", Confidence: domain.ConfidenceExplicit,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := reg.Eligible("p", "m", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "refreshable" {
		t.Fatalf("eligible = %+v, want refreshable only", got)
	}
}

func TestObserveWritesProviderState(t *testing.T) {
	store := providerstate.NewStore(time.Now)
	reg, err := NewRegistry(store, staticSource{name: "empty"})
	if err != nil {
		t.Fatal(err)
	}
	observation := domain.AvailabilityObservation{
		Kind:   domain.ObservationAuthExpired,
		Scope:  domain.AvailabilityScope{ProviderID: "p", CredentialID: "c"},
		Source: "test", Confidence: domain.ConfidenceExplicit,
	}
	if err := reg.Observe(observation); err != nil {
		t.Fatal(err)
	}
	if got := store.Blocking(observation.Scope); len(got) != 1 || got[0].Kind != domain.ObservationAuthExpired {
		t.Fatalf("state = %+v", got)
	}
}
