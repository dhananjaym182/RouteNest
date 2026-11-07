package routing

import (
	"reflect"
	"strings"
	"testing"

	"routenest/internal/domain"
	"routenest/internal/egress"
	"routenest/internal/providers"
)

func TestResolveCandidatePinPrefersStableIDAndRejectsAmbiguousNames(t *testing.T) {
	instances := []domain.ProviderInstance{
		{ID: "alpha", Name: "Shared"},
		{ID: "beta", Name: "Shared"},
	}
	pin, err := resolveCandidatePin(instances, "beta")
	if err != nil || pin.ProviderID != "beta" {
		t.Fatalf("stable ID pin = %+v, %v", pin, err)
	}
	if _, err := resolveCandidatePin(instances, "shared"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous name error = %v", err)
	}
}

func TestDispatchAttemptsPreservesGroupsAndExpandsCredentials(t *testing.T) {
	plan := CandidatePlan{Candidates: []Candidate{
		{
			ProviderID: "a", ProviderName: "A", AdapterID: "x", ModelID: "m1", UpstreamModelID: "u1",
			Group: 0, Credentials: []domain.CredentialID{"c1", "c2"},
			Egress: egress.Decision{Mode: egress.ModeDirect, Source: "fallback-direct"},
		},
		{ProviderID: "b", ProviderName: "B", AdapterID: "y", ModelID: "m2", UpstreamModelID: "u2", Group: 1},
	}}
	attempts := DispatchAttempts(plan)
	if len(attempts) != 3 {
		t.Fatalf("attempts = %+v", attempts)
	}
	got := []string{
		string(attempts[0].ProviderID) + ":" + string(attempts[0].CredentialID),
		string(attempts[1].ProviderID) + ":" + string(attempts[1].CredentialID),
		string(attempts[2].ProviderID) + ":" + string(attempts[2].CredentialID),
	}
	if !reflect.DeepEqual(got, []string{"a:c1", "a:c2", "b:"}) {
		t.Fatalf("expanded order = %v", got)
	}
	if attempts[0].Order != 1 || attempts[1].Order != 2 || attempts[2].Order != 3 || attempts[2].Group != 1 {
		t.Fatalf("attempt metadata = %+v", attempts)
	}
	if attempts[0].EgressMode != string(egress.ModeDirect) || attempts[0].EgressSource != "fallback-direct" {
		t.Fatalf("egress snapshot = %+v", attempts[0])
	}
}

func TestNormalizeCredentialSnapshotPrefersBoundConnectionsAndZenSessions(t *testing.T) {
	snapshot := []domain.ProviderCredential{
		{ID: "api-key:p", ProviderIDs: []domain.ProviderInstanceID{"p"}, Kind: domain.CredentialAPIKey},
		{ID: "connection:c", ProviderIDs: []domain.ProviderInstanceID{"p"}, Kind: domain.CredentialImportedToken},
		{ID: "opencode:public", ProviderIDs: []domain.ProviderInstanceID{"opencode"}, Kind: domain.CredentialPublic},
		{ID: "opencode:session:s", ProviderIDs: []domain.ProviderInstanceID{"opencode"}, Kind: domain.CredentialSession},
	}
	got := normalizeCredentialSnapshot(snapshot, []providers.CustomProvider{{ID: "p", ConnectionIDs: []string{"c"}, APIKey: "secret"}})
	ids := make([]domain.CredentialID, len(got))
	for i := range got {
		ids[i] = got[i].ID
	}
	if !reflect.DeepEqual(ids, []domain.CredentialID{"connection:c", "opencode:session:s"}) {
		t.Fatalf("credentials = %v", ids)
	}
}

func TestEnsureLegacyClineOfferingsKeepsConfiguredUnknownModelsRoutable(t *testing.T) {
	input := CandidatePlanInput{
		Instances: []domain.ProviderInstance{{ID: "cline", AdapterID: "cline", Name: "Cline", Enabled: true}},
		Credentials: []domain.ProviderCredential{{
			ID: "cline:test", ProviderIDs: []domain.ProviderInstanceID{"cline"}, Status: "active",
		}},
		CredentialRequired: map[domain.ProviderInstanceID]bool{"cline": true},
	}
	ensureLegacyClineOfferings(&input, []string{"unknown/primary", "unknown/backup"})
	plan := PlanFallbackChain(input, []string{"unknown/primary", "unknown/backup"})
	if len(plan.Candidates) != 2 {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.Candidates[0].ModelID != "unknown/primary" || plan.Candidates[1].ModelID != "unknown/backup" {
		t.Fatalf("candidate order = %+v", plan.Candidates)
	}
	for _, candidate := range plan.Candidates {
		if candidate.ProviderID != "cline" || candidate.AdapterID != "cline" {
			t.Fatalf("synthetic candidate = %+v", candidate)
		}
	}
}

func TestEnsureLegacyClineOfferingsDoesNotProjectZenCatalogueRows(t *testing.T) {
	input := CandidatePlanInput{Instances: []domain.ProviderInstance{{ID: "cline", Enabled: true}}}
	ensureLegacyClineOfferings(&input, []string{"deepseek-v4-flash-free"})
	for _, offering := range input.Offerings {
		if offering.ProviderID == "cline" && offering.ModelID == "deepseek-v4-flash-free" {
			t.Fatalf("zen model was projected as a cline compatibility route: %+v", offering)
		}
	}
}

func TestEnsureLegacyClineOfferingsDoesNotInventClineRouteForCustomOffering(t *testing.T) {
	input := CandidatePlanInput{
		Instances: []domain.ProviderInstance{{ID: "cline", Enabled: true}, {ID: "custom", Enabled: true}},
		Offerings: []domain.ModelOffering{{
			ProviderID: "custom", ModelID: "custom/model", UpstreamModelID: "custom/model", ProviderEnabled: true,
		}},
	}
	ensureLegacyClineOfferings(&input, []string{"custom/model"})
	if len(input.Offerings) != 1 || input.Offerings[0].ProviderID != "custom" {
		t.Fatalf("compatibility projection invented a cline route: %+v", input.Offerings)
	}
}
