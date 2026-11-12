package models

import (
	"encoding/json"
	"strings"
	"testing"

	"routenest/internal/domain"
)

func policyFixture(policy domain.ProviderAccessPolicy) map[domain.OfferingKey]domain.ModelOffering {
	records := []domain.Model{
		{ID: "free-m", Source: SourceProvider, Cost: CostFree, TierSignal: "suffix-free"},
		{ID: "hint-m", Source: SourceProvider, Cost: CostFree, TierSignal: "provider-free"},
		{ID: "paid-m", Source: SourceProvider, Cost: CostPass, TierSignal: "paid-default"},
		{ID: "hand-paid", Source: SourceProvider, Cost: CostPass, TierSignal: "manual"},
	}
	source := OfferingSource{
		Instance: domain.ProviderInstance{ID: "prov_x", Name: "X", Enabled: true, AccessPolicy: policy},
		ModelIDs: []string{"free-m", "hint-m", "paid-m", "hand-paid"},
	}
	return offeringMap(ProjectOfferings(records, []OfferingSource{{Instance: domain.ProviderInstance{ID: "cline", Enabled: true}}, source}))
}

func TestProjectOfferingsAppliesTheProviderPolicy(t *testing.T) {
	eligible := func(policy domain.ProviderAccessPolicy) map[string]bool {
		out := map[string]bool{}
		for key, offering := range policyFixture(policy) {
			if key.ProviderID == "prov_x" {
				out[key.ModelID] = offering.FreeOnlyEligible()
			}
		}
		return out
	}
	for _, tc := range []struct {
		policy domain.ProviderAccessPolicy
		want   map[string]bool
	}{
		// Default: exactly the behaviour before the setting existed.
		{"", map[string]bool{"free-m": true, "hint-m": false, "paid-m": false, "hand-paid": false}},
		{domain.AccessPolicyDetect, map[string]bool{"free-m": true, "hint-m": false, "paid-m": false, "hand-paid": false}},
		// Everything passes, except the model whose tier the administrator set to paid.
		{domain.AccessPolicyAllFree, map[string]bool{"free-m": true, "hint-m": true, "paid-m": true, "hand-paid": false}},
		{domain.AccessPolicyAllowAll, map[string]bool{"free-m": true, "hint-m": true, "paid-m": true, "hand-paid": false}},
	} {
		got := eligible(tc.policy)
		for model, want := range tc.want {
			if got[model] != want {
				t.Errorf("policy %q: %s eligible = %v, want %v", tc.policy, model, got[model], want)
			}
		}
	}
}

func TestProjectOfferingsKeepsDetectedClassesWhateverThePolicy(t *testing.T) {
	// "Allow every model" lets paid models through but must not relabel them:
	// the administrator can still see which models are paid.
	got := policyFixture(domain.AccessPolicyAllowAll)
	if class := got[domain.OfferingKey{ProviderID: "prov_x", ModelID: "paid-m"}].Access; class != domain.AccessPaidAPI {
		t.Fatalf("paid model on an allow_all provider shows access %q, want paid_api", class)
	}
	if class := got[domain.OfferingKey{ProviderID: "prov_x", ModelID: "hint-m"}].Access; class != domain.AccessUnknown {
		t.Fatalf("provider-free hint shows access %q, want unknown", class)
	}
}

func TestDefaultPolicyAddsNothingToTheOfferingJSON(t *testing.T) {
	for _, policy := range []domain.ProviderAccessPolicy{"", domain.AccessPolicyDetect} {
		for key, offering := range policyFixture(policy) {
			raw, err := json.Marshal(offering)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "providerAccessPolicy") {
				t.Fatalf("policy %q: %+v serialises the setting: %s", policy, key, raw)
			}
		}
	}
	for key, offering := range policyFixture(domain.AccessPolicyAllowAll) {
		if key.ProviderID != "prov_x" {
			continue
		}
		raw, _ := json.Marshal(offering)
		if !strings.Contains(string(raw), `"providerAccessPolicy":"allow_all"`) {
			t.Fatalf("a set policy must be visible in %s", raw)
		}
	}
}

func TestBuiltInRoutesNeverCarryAProviderPolicy(t *testing.T) {
	// Cline and OpenCode are not custom providers; the setting does not apply.
	for key, offering := range policyFixture(domain.AccessPolicyAllowAll) {
		if key.ProviderID == "cline" && offering.ProviderPolicy != "" {
			t.Fatalf("%+v carries policy %q", key, offering.ProviderPolicy)
		}
	}
}
