package models

import (
	"reflect"
	"testing"

	"routenest/internal/domain"
)

func offeringMap(offerings []domain.ModelOffering) map[domain.OfferingKey]domain.ModelOffering {
	out := make(map[domain.OfferingKey]domain.ModelOffering, len(offerings))
	for _, offering := range offerings {
		out[offering.Key()] = offering
	}
	return out
}

func TestProjectOfferingsKeepsProviderRoutesIndependent(t *testing.T) {
	records := []domain.Model{
		{ID: "cline/free", Source: SourceRemote, Cost: CostFree, TierSignal: "flag-free", Context: 1000},
		{ID: "shared/model", Source: SourceProvider, Cost: CostFree, TierSignal: "manual", TierDecidedAt: 11, Output: 2000},
		{ID: "zen/free", Source: SourceZen, Cost: CostFree, TierSignal: "suffix-free", Delisted: true},
		{ID: "manual/model", Source: SourceCustom, Cost: CostPass, TierSignal: "manual", Custom: true},
	}
	sources := []OfferingSource{
		{Instance: domain.ProviderInstance{ID: "cline", Enabled: true}},
		{Instance: domain.ProviderInstance{ID: "opencode", Enabled: false}},
		{Instance: domain.ProviderInstance{ID: "provider-a", Enabled: true}, ModelIDs: []string{"shared/model"}},
		{Instance: domain.ProviderInstance{ID: "provider-b", Enabled: true}, ModelIDs: []string{"shared/model"}},
	}

	offerings := ProjectOfferings(records, sources)
	byKey := offeringMap(offerings)
	for _, key := range []domain.OfferingKey{
		{ProviderID: "cline", ModelID: "cline/free"},
		{ProviderID: "cline", ModelID: "manual/model"},
		{ProviderID: "opencode", ModelID: "zen/free"},
		{ProviderID: "provider-a", ModelID: "shared/model"},
		{ProviderID: "provider-b", ModelID: "shared/model"},
	} {
		if _, ok := byKey[key]; !ok {
			t.Errorf("missing offering %+v in %+v", key, offerings)
		}
	}
	if len(byKey) != 5 {
		t.Fatalf("offerings = %d, want 5: %+v", len(byKey), offerings)
	}

	a := byKey[domain.OfferingKey{ProviderID: "provider-a", ModelID: "shared/model"}]
	b := byKey[domain.OfferingKey{ProviderID: "provider-b", ModelID: "shared/model"}]
	if a.ProviderID == b.ProviderID || a.Access != domain.AccessFree || b.Access != domain.AccessFree {
		t.Fatalf("shared routes collapsed or misclassified: a=%+v b=%+v", a, b)
	}
	if a.TierSignal != "manual" || a.TierDecidedAt != 11 || a.Output != 2000 {
		t.Fatalf("manual evidence lost: %+v", a)
	}
	if got := byKey[domain.OfferingKey{ProviderID: "opencode", ModelID: "zen/free"}]; got.ProviderEnabled || !got.Delisted {
		t.Fatalf("Zen runtime state lost: %+v", got)
	}
}

func TestProjectOfferingsAccessClasses(t *testing.T) {
	records := []domain.Model{
		{ID: "free", Source: SourceProvider, Cost: CostFree, TierSignal: "pricing-zero"},
		{ID: "provider-hint", Source: SourceProvider, Cost: CostFree, TierSignal: "provider-free"},
		{ID: "subscription", Source: SourceRemote, Cost: CostPass},
		{ID: "paid", Source: SourceProvider, Cost: CostPass, TierSignal: "pricing-paid"},
		{ID: "manual-free", Source: SourceProvider, Cost: CostFree, TierSignal: "manual"},
		{ID: "unknown", Source: SourceProvider},
	}
	source := OfferingSource{
		Instance: domain.ProviderInstance{ID: "provider", Enabled: true},
		ModelIDs: []string{"free", "provider-hint", "subscription", "paid", "manual-free", "unknown"},
	}
	offerings := ProjectOfferings(records, []OfferingSource{
		{Instance: domain.ProviderInstance{ID: "cline", Enabled: true}}, source,
	})
	byKey := offeringMap(offerings)
	want := map[domain.OfferingKey]domain.AccessClass{
		{ProviderID: "provider", ModelID: "free"}:          domain.AccessFree,
		{ProviderID: "provider", ModelID: "provider-hint"}: domain.AccessUnknown,
		{ProviderID: "cline", ModelID: "subscription"}:     domain.AccessSubscription,
		{ProviderID: "provider", ModelID: "subscription"}:  domain.AccessPaidAPI,
		{ProviderID: "provider", ModelID: "paid"}:          domain.AccessPaidAPI,
		{ProviderID: "provider", ModelID: "manual-free"}:   domain.AccessFree,
		{ProviderID: "provider", ModelID: "unknown"}:       domain.AccessUnknown,
	}
	for key, access := range want {
		if got := byKey[key].Access; got != access {
			t.Errorf("%+v access = %q, want %q", key, got, access)
		}
	}
	if !byKey[domain.OfferingKey{ProviderID: "provider", ModelID: "free"}].FreeOnlyEligible() {
		t.Fatal("verified free offering must be eligible for free-only routing")
	}
	if byKey[domain.OfferingKey{ProviderID: "provider", ModelID: "provider-hint"}].FreeOnlyEligible() {
		t.Fatal("provider-wide free hint must not silently qualify for free-only routing")
	}
}

func TestProjectOfferingsIsDeterministic(t *testing.T) {
	records := []domain.Model{{ID: "z", Source: SourceZen, Cost: CostFree}, {ID: "a", Source: SourceRemote, Cost: CostFree}}
	sources := []OfferingSource{{Instance: domain.ProviderInstance{ID: "opencode"}}, {Instance: domain.ProviderInstance{ID: "cline"}}}
	first := ProjectOfferings(records, sources)
	second := ProjectOfferings(records, sources)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("offering projection is not deterministic: %v / %v", first, second)
	}
	if first[0].ProviderID != "cline" || first[0].ModelID != "a" {
		t.Fatalf("unexpected stable order: %+v", first)
	}
}
