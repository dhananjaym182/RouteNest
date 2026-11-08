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

func TestProjectOfferingsProviderOverlayOverridesLegacy(t *testing.T) {
	legacy := domain.Model{
		ID: "shared/model", Provider: "Acme", Source: SourceProvider,
		Cost: CostPass, TierSignal: "manual", TierDecidedAt: 11,
		Status: "inactive", Disabled: true, Delisted: true, Custom: true,
		Context: 1000, Output: 2000, MetaLocked: true,
		ThinkingLevels: []string{"medium"}, InputModalities: []string{"text"}, OutputModalities: []string{"audio"},
	}
	verified, cleared := true, false
	active, zero, output := "active", 0, 8192
	empty, inputs := []string{}, []string{"text", "image"}
	state := domain.ModelOfferingState{
		UpstreamModelID: "vendor/shared", Cost: CostFree, Access: domain.AccessFreeTier,
		AllowanceVerified: &verified, TierSignal: "pricing-zero", TierDecidedAt: 22,
		Status: &active, Disabled: &cleared, Delisted: &cleared, Manual: &cleared, Custom: &cleared,
		Context: &zero, Output: &output, MetaLocked: &cleared,
		ThinkingLevels: &empty, InputModalities: &inputs, OutputModalities: &empty,
	}
	want := domain.ModelOffering{
		ProviderID: "provider-a", ModelID: legacy.ID, UpstreamModelID: "vendor/shared",
		Access: domain.AccessFreeTier, AllowanceVerified: true,
		TierSignal: "pricing-zero", TierDecidedAt: 22, ProviderEnabled: true,
		Status: "active", Output: 8192, InputModalities: []string{"text", "image"},
	}
	for _, tc := range []struct {
		name     string
		modelIDs []string
	}{
		{name: "declared model", modelIDs: []string{legacy.ID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			offerings := ProjectOfferings([]domain.Model{legacy}, []OfferingSource{{
				Instance: domain.ProviderInstance{ID: "provider-a", Name: "Acme", Enabled: true},
				ModelIDs: tc.modelIDs, States: map[string]domain.ModelOfferingState{legacy.ID: state},
			}})
			if len(offerings) != 1 || !reflect.DeepEqual(offerings[0], want) {
				t.Fatalf("provider overlay = %+v, want %+v", offerings, want)
			}
			if !offerings[0].FreeOnlyEligible() {
				t.Fatal("verified provider allowance must qualify its free-tier offering")
			}
		})
	}
}

func TestProjectOfferingsProviderOverlayFallback(t *testing.T) {
	legacy := domain.Model{
		ID: "model", Source: SourceProvider, Cost: CostPass, TierSignal: "manual", TierDecidedAt: 11,
		Status: "active", Disabled: true, Context: 1024, Output: 512,
	}
	fallback := domain.ModelOffering{
		ProviderID: "provider", ModelID: legacy.ID, UpstreamModelID: legacy.ID,
		Access: domain.AccessPaidAPI, TierSignal: "manual", TierDecidedAt: 11, ProviderEnabled: true,
		Status: "active", Disabled: true, Manual: true, Context: 1024, Output: 512,
	}
	zero, negative, cleared, verified := 0, -1, false, true
	sparse := fallback
	sparse.UpstreamModelID, sparse.Output = "vendor/model", 0
	accessOnly := fallback
	accessOnly.Access, accessOnly.AllowanceVerified = domain.AccessFreeTier, true
	for _, tc := range []struct {
		name   string
		states map[string]domain.ModelOfferingState
		want   domain.ModelOffering
	}{
		{name: "missing state", want: fallback},
		{name: "empty state", states: map[string]domain.ModelOfferingState{legacy.ID: {}}, want: fallback},
		{name: "sparse state", states: map[string]domain.ModelOfferingState{legacy.ID: {UpstreamModelID: "vendor/model", Output: &zero}}, want: sparse},
		{name: "access without compatibility cost", states: map[string]domain.ModelOfferingState{legacy.ID: {Access: domain.AccessFreeTier, AllowanceVerified: &verified}}, want: accessOnly},
		{name: "invalid cost", states: map[string]domain.ModelOfferingState{legacy.ID: {Cost: "invalid", UpstreamModelID: "ignored", Disabled: &cleared}}, want: fallback},
		{name: "invalid access", states: map[string]domain.ModelOfferingState{legacy.ID: {Cost: CostFree, Access: "invalid"}}, want: fallback},
		{name: "invalid context", states: map[string]domain.ModelOfferingState{legacy.ID: {Context: &negative, Output: &zero}}, want: fallback},
	} {
		t.Run(tc.name, func(t *testing.T) {
			offerings := ProjectOfferings([]domain.Model{legacy}, []OfferingSource{{
				Instance: domain.ProviderInstance{ID: "provider", Enabled: true},
				ModelIDs: []string{legacy.ID}, States: tc.states,
			}})
			if len(offerings) != 1 || !reflect.DeepEqual(offerings[0], tc.want) {
				t.Fatalf("provider offering = %+v, want %+v", offerings, tc.want)
			}
		})
	}
}

func TestProjectOfferingsSharedModelKeepsProviderOverlaysIndependent(t *testing.T) {
	legacy := domain.Model{
		ID: "shared/model", Provider: "Acme", Source: SourceProvider,
		Cost: CostPass, TierSignal: "manual", TierDecidedAt: 11,
		Status: "inactive", Disabled: true, Delisted: true, Custom: true,
		Context: 1000, Output: 256, MetaLocked: true, ThinkingLevels: []string{"legacy"},
	}
	cleared, active, zero := false, "active", 0
	contextA, contextB := 4096, 8192
	thinkingA, thinkingB := []string{"high"}, []string{"low"}
	sources := []OfferingSource{
		{
			Instance: domain.ProviderInstance{ID: "provider-a", Name: "Acme", Enabled: false},
			ModelIDs: []string{legacy.ID},
			States: map[string]domain.ModelOfferingState{legacy.ID: {
				UpstreamModelID: "vendor-a/model", Cost: CostFree, Access: domain.AccessFree,
				TierSignal: "manual", TierDecidedAt: 22, Context: &contextA, ThinkingLevels: &thinkingA,
			}},
		},
		{
			Instance: domain.ProviderInstance{ID: "provider-b", Enabled: true},
			ModelIDs: []string{legacy.ID},
			States: map[string]domain.ModelOfferingState{legacy.ID: {
				UpstreamModelID: "vendor-b/model", Cost: CostPass, Access: domain.AccessPaidAPI,
				TierSignal: "pricing-paid", TierDecidedAt: 33, Status: &active,
				Disabled: &cleared, Delisted: &cleared, Manual: &cleared, Custom: &cleared,
				Context: &contextB, Output: &zero, MetaLocked: &cleared, ThinkingLevels: &thinkingB,
			}},
		},
	}
	want := []domain.ModelOffering{
		{
			ProviderID: "provider-a", ModelID: legacy.ID, UpstreamModelID: "vendor-a/model",
			Access: domain.AccessFree, TierSignal: "manual", TierDecidedAt: 22,
			Status: "inactive", Disabled: true, Delisted: true, Manual: true, Custom: true,
			Context: 4096, Output: 256, MetaLocked: true, ThinkingLevels: []string{"high"},
		},
		{
			ProviderID: "provider-b", ModelID: legacy.ID, UpstreamModelID: "vendor-b/model",
			Access: domain.AccessPaidAPI, TierSignal: "pricing-paid", TierDecidedAt: 33, ProviderEnabled: true,
			Status: "active", Context: 8192, ThinkingLevels: []string{"low"},
		},
	}
	offerings := ProjectOfferings([]domain.Model{legacy}, sources)
	if !reflect.DeepEqual(offerings, want) {
		t.Fatalf("shared model offerings = %+v, want %+v", offerings, want)
	}
	offerings[0].ThinkingLevels[0] = "changed"
	if thinkingA[0] != "high" || thinkingB[0] != "low" || legacy.ThinkingLevels[0] != "legacy" || offerings[1].ThinkingLevels[0] != "low" {
		t.Fatal("projected metadata aliases a provider overlay or the legacy model")
	}
}

func TestProjectOfferingsBuiltinsKeepLegacyState(t *testing.T) {
	cleared, active, zero := false, "active", 0
	empty := []string{}
	overlay := domain.ModelOfferingState{
		UpstreamModelID: "vendor/model", Cost: CostFree, Access: domain.AccessFree,
		TierSignal: "pricing-zero", TierDecidedAt: 22, Status: &active,
		Disabled: &cleared, Delisted: &cleared, Manual: &cleared, Custom: &cleared,
		Context: &zero, Output: &zero, MetaLocked: &cleared,
		ThinkingLevels: &empty, InputModalities: &empty, OutputModalities: &empty,
	}
	for _, tc := range []struct {
		name       string
		source     string
		cost       string
		providerID domain.ProviderInstanceID
		access     domain.AccessClass
		enabled    bool
		custom     bool
	}{
		{name: "Cline subscription", source: SourceRemote, cost: CostPass, providerID: "cline", access: domain.AccessSubscription, enabled: true},
		{name: "Cline free", source: SourceRemote, cost: CostFree, providerID: "cline", access: domain.AccessFree, enabled: true},
		{name: "Zen paid", source: SourceZen, cost: CostPass, providerID: "opencode", access: domain.AccessPaidAPI},
		{name: "legacy Zen seed", source: SourceSeed, cost: CostFree, providerID: "opencode", access: domain.AccessFree},
		{name: "legacy manual Cline route", source: SourceCustom, cost: CostPass, providerID: "cline", access: domain.AccessPaidAPI, enabled: true, custom: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			legacy := domain.Model{
				ID: "shared/model", Source: tc.source, Cost: tc.cost, TierSignal: "legacy", TierDecidedAt: 11,
				Status: "active", Disabled: true, Delisted: true, Custom: tc.custom,
				Context: 1000, Output: 512, MetaLocked: true,
				ThinkingLevels: []string{"legacy"}, InputModalities: []string{"text"}, OutputModalities: []string{"text"},
			}
			sources := []OfferingSource{
				{Instance: domain.ProviderInstance{ID: "cline", Enabled: true}, ModelIDs: []string{legacy.ID, "not-in-catalog"}, States: map[string]domain.ModelOfferingState{legacy.ID: overlay}},
				{Instance: domain.ProviderInstance{ID: "opencode", Enabled: false}, ModelIDs: []string{legacy.ID, "not-in-catalog"}, States: map[string]domain.ModelOfferingState{legacy.ID: overlay}},
				{Instance: domain.ProviderInstance{ID: "provider", Enabled: true}, ModelIDs: []string{legacy.ID}, States: map[string]domain.ModelOfferingState{legacy.ID: overlay}},
			}
			offerings := ProjectOfferings([]domain.Model{legacy}, sources)
			if len(offerings) != 2 {
				t.Fatalf("offerings = %+v, want one built-in and one custom-provider route", offerings)
			}
			byKey := offeringMap(offerings)
			want := domain.ModelOffering{
				ProviderID: tc.providerID, ModelID: legacy.ID, UpstreamModelID: legacy.ID,
				Access: tc.access, TierSignal: "legacy", TierDecidedAt: 11, ProviderEnabled: tc.enabled,
				Status: "active", Disabled: true, Delisted: true, Manual: tc.custom, Custom: tc.custom,
				Context: 1000, Output: 512, MetaLocked: true,
				ThinkingLevels: []string{"legacy"}, InputModalities: []string{"text"}, OutputModalities: []string{"text"},
			}
			if got := byKey[want.Key()]; !reflect.DeepEqual(got, want) {
				t.Fatalf("built-in offering = %+v, want %+v", got, want)
			}
			custom := byKey[domain.OfferingKey{ProviderID: "provider", ModelID: legacy.ID}]
			if custom.Access != domain.AccessFree || custom.UpstreamModelID != "vendor/model" || custom.Disabled || custom.Delisted || custom.Custom || custom.Context != 0 {
				t.Fatalf("custom-provider overlay lost under a built-in ID collision: %+v", custom)
			}
		})
	}
}
