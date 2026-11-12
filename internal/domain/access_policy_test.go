package domain

import "testing"

// The provider's Free-only setting is an explicit statement by the
// administrator. The rule under test, most specific choice first:
//
//  1. A model whose tier the administrator set by hand follows that tier,
//     whatever the provider setting says.
//  2. Otherwise "all_free" and "allow_all" let every model of the provider
//     through Free-only routing.
//  3. Otherwise (the default, "detect") the model's own access class decides.
func TestDecideFreeOnlyMatrix(t *testing.T) {
	classes := []struct {
		name     string
		class    AccessClass
		verified bool
		byClass  bool // what the class alone allows
	}{
		{"free", AccessFree, false, true},
		{"free_tier unverified", AccessFreeTier, false, false},
		{"free_tier verified", AccessFreeTier, true, true},
		{"subscription", AccessSubscription, true, false},
		{"paid_api", AccessPaidAPI, false, false},
		{"trial", AccessTrial, true, false},
		{"unknown", AccessUnknown, false, false},
	}
	for _, c := range classes {
		for _, policy := range []ProviderAccessPolicy{"", AccessPolicyDetect} {
			got := DecideFreeOnly(c.class, c.verified, false, policy)
			if got.Eligible != c.byClass || got.Basis != BasisClass {
				t.Errorf("%s / policy %q: got %+v, want eligible=%v basis=class (default must be today's behaviour)", c.name, policy, got, c.byClass)
			}
		}
		if got := DecideFreeOnly(c.class, c.verified, false, AccessPolicyAllFree); !got.Eligible || got.Basis != BasisProviderAllFree {
			t.Errorf("%s / all_free: got %+v, want eligible via provider_all_free", c.name, got)
		}
		if got := DecideFreeOnly(c.class, c.verified, false, AccessPolicyAllowAll); !got.Eligible || got.Basis != BasisProviderAllowAll {
			t.Errorf("%s / allow_all: got %+v, want eligible via provider_allow_all", c.name, got)
		}
		// A hand-set tier wins over every provider setting, in both directions.
		for _, policy := range []ProviderAccessPolicy{"", AccessPolicyDetect, AccessPolicyAllFree, AccessPolicyAllowAll} {
			got := DecideFreeOnly(c.class, c.verified, true, policy)
			if got.Eligible != c.byClass || got.Basis != BasisManual {
				t.Errorf("%s / manual / policy %q: got %+v, want eligible=%v basis=manual", c.name, policy, got, c.byClass)
			}
		}
	}
}

func TestDecideFreeOnlyUnreadableClassIsOnlyAllowedByAnExplicitProviderChoice(t *testing.T) {
	const garbage AccessClass = "not-a-class"
	if DecideFreeOnly(garbage, true, false, AccessPolicyDetect).Eligible {
		t.Fatal("an unreadable class must never pass by detection")
	}
	if !DecideFreeOnly(garbage, true, false, AccessPolicyAllowAll).Eligible {
		t.Fatal("the administrator's explicit provider choice applies to every model")
	}
	if DecideFreeOnly(garbage, true, true, AccessPolicyAllowAll).Eligible {
		t.Fatal("a hand-set tier with an unreadable class must stay blocked")
	}
}

func TestNormalizeProviderAccessPolicy(t *testing.T) {
	for raw, want := range map[string]ProviderAccessPolicy{
		"": AccessPolicyDetect, "detect": AccessPolicyDetect, "  Detect ": AccessPolicyDetect,
		"all_free": AccessPolicyAllFree, "ALL_FREE": AccessPolicyAllFree,
		"allow_all": AccessPolicyAllowAll, " Allow_All": AccessPolicyAllowAll,
	} {
		got, ok := NormalizeProviderAccessPolicy(raw)
		if !ok || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q, true", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"free", "both", "all", "allow-all", "true", "1", "detect;all_free"} {
		if got, ok := NormalizeProviderAccessPolicy(raw); ok {
			t.Errorf("Normalize(%q) = %q, true; unknown values must be rejected, not guessed", raw, got)
		}
	}
}

func TestOfferingFreeOnlyDecisionUsesTheProviderPolicyAndOnlyAHandSetTier(t *testing.T) {
	base := ModelOffering{ProviderID: "p", ModelID: "m", Access: AccessPaidAPI, TierSignal: "paid-default"}
	cases := []struct {
		name string
		edit func(o *ModelOffering)
		want bool
		by   FreeOnlyBasis
	}{
		{"default blocks a paid model", func(o *ModelOffering) {}, false, BasisClass},
		{"all_free lets it through", func(o *ModelOffering) { o.ProviderPolicy = AccessPolicyAllFree }, true, BasisProviderAllFree},
		{"allow_all lets it through", func(o *ModelOffering) { o.ProviderPolicy = AccessPolicyAllowAll }, true, BasisProviderAllowAll},
		{"hand-set paid beats allow_all", func(o *ModelOffering) { o.ProviderPolicy = AccessPolicyAllowAll; o.TierSignal = "manual" }, false, BasisManual},
		{"hand-set paid beats all_free", func(o *ModelOffering) { o.ProviderPolicy = AccessPolicyAllFree; o.TierSignal = "manual" }, false, BasisManual},
		{"hand-set free passes under detect", func(o *ModelOffering) { o.Access = AccessFree; o.TierSignal = "manual" }, true, BasisManual},
		// Custom and Manual flags describe how a model got into the catalog, not
		// a tier decision. Only TierSignal == "manual" is the administrator's tier.
		{"custom model follows the provider", func(o *ModelOffering) {
			o.Custom, o.Manual, o.ProviderPolicy = true, true, AccessPolicyAllowAll
		}, true, BasisProviderAllowAll},
	}
	for _, tc := range cases {
		o := base
		tc.edit(&o)
		got := o.FreeOnlyDecision()
		if got.Eligible != tc.want || got.Basis != tc.by || o.FreeOnlyEligible() != tc.want {
			t.Errorf("%s: decision %+v (FreeOnlyEligible=%v), want eligible=%v basis=%s", tc.name, got, o.FreeOnlyEligible(), tc.want, tc.by)
		}
	}
}
