package integration

// The provider's Free-only setting is the administrator's explicit choice. These
// tests run real combos and real model checks with Free-only routing on, for a
// provider shaped like a mixed catalog: some models detected free, one paid by
// default, one only carrying the provider-level free hint, and two whose tier
// the administrator set by hand.

import (
	"net/http"
	"strings"
	"testing"

	"routenest/internal/admin"
	"routenest/internal/combos"
	"routenest/internal/config"
	"routenest/internal/domain"
	"routenest/internal/routing"
)

var dispatchModes = []config.DispatchMode{config.DispatchModeLegacy, config.DispatchModeShadow, config.DispatchModeEnforce}

const policyProviderID = "prov_policy1"

type policyModel struct {
	id         string
	cost       string
	tierSignal string
}

var policyModels = []policyModel{
	{"vendor-free", "free", "suffix-free"},
	{"vendor-paid", "pass", "paid-default"},
	{"vendor-hint", "free", "provider-free"},
	{"hand-paid", "pass", "manual"},
	{"hand-free", "free", "manual"},
}

func installPolicyProvider(t *testing.T, policy domain.ProviderAccessPolicy, mode config.DispatchMode) {
	t.Helper()
	var records []Model
	var ids []string
	for _, m := range policyModels {
		ids = append(ids, m.id)
		records = append(records, Model{ID: m.id, Provider: "Mixed", Source: "provider", Cost: m.cost, TierSignal: m.tierSignal, Status: "active"})
	}
	installPool(t, records)
	cfg := admin.DefaultProxyConfig()
	cfg.OnlyFree = true
	cfg.DispatchMode = mode
	cfg.ProviderModelPrefixes = map[string]string{"mx": policyProviderID, "op": "opencode", "cl": "cline"}
	installConfig(t, cfg)
	installProviders(t, &CustomProvider{
		ID: policyProviderID, Name: "Mixed", BaseURL: "http://mixed.test/v1", APIKey: "sk",
		ModelIDs: ids, Enabled: true, Free: true, AccessPolicy: policy,
	})
	installTransport(t, func(req *http.Request) *http.Response { return stubResp(req, http.StatusOK, okCompletion) })
}

// served runs a one-entry combo pinned to the provider and reports whether the
// entry was used, plus the trace reason when it was not.
func served(t *testing.T, model string) (bool, string) {
	t.Helper()
	combo := ModelCombo{ID: "c", Name: "C", Enabled: true,
		Entries: []ComboEntry{{Model: model, Source: "provider", Provider: policyProviderID}}}
	run := combos.RunCombo(chatParams("c"), combo, false, "")
	if len(run.Trace) != 1 {
		t.Fatalf("%s: trace = %+v", model, run.Trace)
	}
	if run.Err == nil && run.Trace[0].Outcome == "success" {
		return true, ""
	}
	return false, run.Trace[0].Reason
}

func TestProviderFreeOnlySettingDecidesWhichModelsAreServed(t *testing.T) {
	cases := []struct {
		policy domain.ProviderAccessPolicy
		want   map[string]bool
	}{
		{"", map[string]bool{"vendor-free": true, "vendor-paid": false, "vendor-hint": false, "hand-paid": false, "hand-free": true}},
		{domain.AccessPolicyDetect, map[string]bool{"vendor-free": true, "vendor-paid": false, "vendor-hint": false, "hand-paid": false, "hand-free": true}},
		// A hand-set tier wins over the provider setting, in both directions.
		{domain.AccessPolicyAllFree, map[string]bool{"vendor-free": true, "vendor-paid": true, "vendor-hint": true, "hand-paid": false, "hand-free": true}},
		{domain.AccessPolicyAllowAll, map[string]bool{"vendor-free": true, "vendor-paid": true, "vendor-hint": true, "hand-paid": false, "hand-free": true}},
	}
	for _, tc := range cases {
		for _, mode := range dispatchModes {
			t.Run("policy="+string(tc.policy)+"/"+string(mode), func(t *testing.T) {
				installPolicyProvider(t, tc.policy, mode)
				for model, want := range tc.want {
					got, reason := served(t, model)
					if got != want {
						t.Errorf("%s served = %v, want %v (reason %q)", model, got, want, reason)
					}
				}
			})
		}
	}
}

// A client that names the model directly, or pins it with model@Provider or a
// prefix, goes through the same check as a combo entry.
func TestProviderFreeOnlySettingAppliesToDirectModelReferences(t *testing.T) {
	refs := func(model string) []string {
		return []string{model, "mx/" + model, model + "@Mixed", model + "@" + policyProviderID}
	}
	for _, tc := range []struct {
		policy domain.ProviderAccessPolicy
		model  string
		want   bool
	}{
		{"", "vendor-paid", false},
		{"", "vendor-hint", false},
		{"", "vendor-free", true},
		{domain.AccessPolicyAllowAll, "vendor-paid", true},
		{domain.AccessPolicyAllowAll, "vendor-hint", true},
		{domain.AccessPolicyAllFree, "vendor-paid", true},
		{domain.AccessPolicyAllowAll, "hand-paid", false}, // the administrator's tier wins
		{domain.AccessPolicyAllFree, "hand-paid", false},
	} {
		for _, mode := range dispatchModes {
			t.Run(string(tc.policy)+"/"+tc.model+"/"+string(mode), func(t *testing.T) {
				installPolicyProvider(t, tc.policy, mode)
				for _, ref := range refs(tc.model) {
					if got := routing.ModelFreeOnlyEligible(ref); got != tc.want {
						t.Errorf("ModelFreeOnlyEligible(%q) = %v, want %v", ref, got, tc.want)
					}
					rejected := routing.FreeOnlyRejection(ref) != ""
					if rejected == tc.want {
						t.Errorf("FreeOnlyRejection(%q) rejected = %v, want %v", ref, rejected, !tc.want)
					}
				}
			})
		}
	}
}

// Presets, syncs and imports never choose a policy for the administrator, and a
// provider flagged free is still only a hint.
func TestFreeFlagAloneNeverAllowsPaidModels(t *testing.T) {
	for _, mode := range dispatchModes {
		t.Run(string(mode), func(t *testing.T) {
			installPolicyProvider(t, "", mode)
			if got, _ := served(t, "vendor-paid"); got {
				t.Fatal("a provider flagged free (statistics only) must not let a paid model through Free-only routing")
			}
			if got, reason := served(t, "vendor-hint"); got || !strings.Contains(reason, "onlyFree") {
				t.Fatalf("provider-free hint served=%v reason=%q", got, reason)
			}
		})
	}
}

// A refusal names the access class, as it always has, and now also what would
// allow the route: the provider's setting, or the model's own tier. When the
// administrator set the model's tier by hand, it says so instead of pointing at
// the provider.
func TestFreeOnlyRefusalSaysHowToAllowIt(t *testing.T) {
	installPolicyProvider(t, "", config.DispatchModeShadow)

	_, paid := served(t, "vendor-paid")
	for _, want := range []string{"onlyFree", "access class: paid_api", "provider's Free-only setting", "tier to free"} {
		if !strings.Contains(paid, want) {
			t.Errorf("paid-by-default refusal %q lacks %q", paid, want)
		}
	}
	_, hint := served(t, "vendor-hint")
	if !strings.Contains(hint, "access class: unknown") || !strings.Contains(hint, "provider's Free-only setting") {
		t.Errorf("provider-hint refusal %q must name the class and the provider setting", hint)
	}

	// A tier set by hand is the administrator's own decision: say that, and do
	// not suggest changing the provider, which would not help.
	_, manual := served(t, "hand-paid")
	if !strings.Contains(manual, "you set") || strings.Contains(manual, "provider's Free-only setting") {
		t.Errorf("hand-set refusal %q must say the tier was set by the administrator and not point at the provider", manual)
	}

	// Direct references get the same wording from the early check.
	msg := routing.FreeOnlyRejection("mx/vendor-paid")
	if !strings.Contains(msg, "access class: paid_api") || !strings.Contains(msg, "provider's Free-only setting") {
		t.Errorf("FreeOnlyRejection = %q", msg)
	}
	if msg := routing.FreeOnlyRejection("mx/hand-paid"); !strings.Contains(msg, "you set") {
		t.Errorf("FreeOnlyRejection for a hand-set tier = %q", msg)
	}

	// Anything over the trace's 200-byte limit would be cut mid-sentence.
	for _, reason := range []string{paid, hint, manual} {
		if len(reason) > 200 {
			t.Errorf("reason is %d bytes, the trace truncates at 200: %q", len(reason), reason)
		}
	}
}
