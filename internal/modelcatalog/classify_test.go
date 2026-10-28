package modelcatalog

import "testing"

func f(v float64) *float64 { return &v }

func TestFreeWord(t *testing.T) {
	cases := map[string]bool{
		"free":              true,
		"cline-free/x":      true,
		"x-free":            true,
		"x:free:y":          true,
		"Flash (Free)":      true,
		"freemap":           false,
		"freemium-model":    false,
		"freedom":           false,
		"":                  false,
		"model-with-free-x": true,
	}
	for in, want := range cases {
		if got := FreeWord(in); got != want {
			t.Errorf("FreeWord(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestClassifySignals(t *testing.T) {
	allow := map[string]bool{"seed/model-a": true}
	cases := []struct {
		name         string
		entry        Entry
		providerFree bool
		wantTier     string
		wantSignal   string
	}{
		{"zero pricing", Entry{ID: "paid-ish", PricingPrompt: f(0), PricingComplet: f(0)}, false, "free", TierSignalPricingZero},
		{"zero pricing with request fee", Entry{ID: "x", PricingPrompt: f(0), PricingComplet: f(0), PricingRequest: f(0.5)}, false, "pass", TierSignalPricingPaid},
		{"free flag", Entry{ID: "x", FreeFlag: true}, false, "free", TierSignalFlagFree},
		{"free flag beats nonzero price", Entry{ID: "x", FreeFlag: true, PricingPrompt: f(0.1), PricingComplet: f(0.2)}, false, "free", TierSignalFlagFree},
		{"mode free", Entry{ID: "x", Mode: "FREE"}, false, "free", TierSignalFlagFree},
		{"suffix colon", Entry{ID: "x:free"}, false, "free", TierSignalSuffixFree},
		{"suffix dash", Entry{ID: "x-FREE"}, false, "free", TierSignalSuffixFree},
		{"suffix beats nonzero price", Entry{ID: "x:free", PricingPrompt: f(0.1), PricingComplet: f(0.2)}, false, "free", TierSignalSuffixFree},
		{"name token is weak", Entry{ID: "ns/free-model"}, false, "pass", TierSignalNameToken},
		{"display name is weak", Entry{ID: "x", Name: "Model (Free)"}, false, "pass", TierSignalDisplayName},
		{"tag", Entry{ID: "x", Tags: []string{"FREE"}}, false, "free", TierSignalAllowlist},
		{"allowlist", Entry{ID: "SEED/MODEL-A"}, false, "free", TierSignalAllowlist},
		{"pricing beats provider free", Entry{ID: "x", PricingPrompt: f(1)}, true, "pass", TierSignalPricingPaid},
		{"provider free flag is metadata", Entry{ID: "x"}, true, "pass", TierSignalProviderFree},
		{"name token with provider free stays weak", Entry{ID: "ns/free-model"}, true, "pass", TierSignalNameToken},
		{"unknown defaults to pass", Entry{ID: "x"}, false, "pass", TierSignalPaidDefault},
		{"nonzero price beats display name", Entry{ID: "x", Name: "Model (Free)", PricingPrompt: f(0.001), PricingComplet: f(0.002)}, false, "pass", TierSignalPricingPaid},
		{"nonzero price beats name token", Entry{ID: "ns/free-model", PricingComplet: f(0.00000024)}, false, "pass", TierSignalPricingPaid},
		{"nonzero price beats tag allowlist", Entry{ID: "x", Tags: []string{"free"}, PricingPrompt: f(0.001), PricingComplet: f(0.002)}, false, "pass", TierSignalPricingPaid},
		{"nonzero price beats allowlist", Entry{ID: "SEED/MODEL-A", PricingPrompt: f(0.001), PricingComplet: f(0.002)}, false, "pass", TierSignalPricingPaid},
		{"nonzero request fee alone is paid", Entry{ID: "x", PricingRequest: f(0.25)}, false, "pass", TierSignalPricingPaid},
		{"request-only fee beats provider free", Entry{ID: "x", PricingRequest: f(0.25)}, true, "pass", TierSignalPricingPaid},
		{"zero pricing request nil", Entry{ID: "x", PricingPrompt: f(0), PricingComplet: f(0)}, false, "free", TierSignalPricingZero},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tier, signal := Classify(tc.entry, tc.providerFree, allow)
			if tier != tc.wantTier || signal != tc.wantSignal {
				t.Fatalf("got %q/%q, want %q/%q", tier, signal, tc.wantTier, tc.wantSignal)
			}
		})
	}
}

func TestClassifyDoesNotTreatFreemiumAsFree(t *testing.T) {
	if tier, _ := Classify(Entry{ID: "freemium-model"}, false, nil); tier != "pass" {
		t.Fatalf("tier = %q, want pass", tier)
	}
}

func TestClassifyUnparseablePricingStaysWeak(t *testing.T) {
	// Pricing components parse to nil, so weak signals still apply.
	e := Entry{ID: "a/credit-openai-m-5.3-flash-free", PricingPrompt: f(0)}
	tier, signal := Classify(e, false, nil)
	if tier != "free" || signal != TierSignalSuffixFree {
		t.Fatalf("got %q/%q, want free/%s", tier, signal, TierSignalSuffixFree)
	}
}
