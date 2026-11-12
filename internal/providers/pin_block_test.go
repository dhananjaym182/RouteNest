package providers

import (
	"strings"
	"testing"
	"time"

	"routenest/internal/providerstate"
)

func TestPinUnavailableReasonPrecedence(t *testing.T) {
	now := time.Now().UTC()
	installTestBudget(t, func() time.Time { return now })
	// Every provider below has the model, a per-minute cap already reached and
	// an active cooldown, so the order of checks decides what is reported.
	full := func(id string) *CustomProvider {
		return &CustomProvider{ID: id, Name: id, ModelIDs: []string{"m"}, Enabled: true, RpmLimit: 1}
	}
	disabled := full("prov_a")
	disabled.Enabled = false
	unlisted := full("prov_b")
	unlisted.ModelIDs = []string{"other"}
	installProviders(t, disabled, unlisted, full("prov_c"), full("prov_d"))
	if err := providerstate.Publish(providerstate.NewStore(func() time.Time { return now })); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"prov_a", "prov_b", "prov_c", "prov_d"} {
		admitTestUsage(t, *Find(id), "m", id+"-attempt", id+"-tokens", now, 1)
	}
	SetCooldown("prov_a", "m", now.Add(time.Minute))
	SetCooldown("prov_b", "m", now.Add(time.Minute))
	SetCooldown("prov_c", "m", now.Add(time.Minute))

	for _, tc := range []struct{ id, want, notWant string }{
		{"prov_a", "is disabled", "cooling down"},
		{"prov_b", "does not offer model", "cooling down"},
		{"prov_c", "cooling down", "per-minute"},
		{"prov_d", "per-minute request limit reached (1 of 1)", "cooling down"},
	} {
		got := PinUnavailableReason("m", tc.id)
		if !strings.Contains(got, tc.want) || strings.Contains(got, tc.notWant) {
			t.Errorf("%s: reason %q, want it to contain %q and not %q", tc.id, got, tc.want, tc.notWant)
		}
	}
}

func TestPinUnavailableReasonUnknownAndEmptyPin(t *testing.T) {
	installTestBudget(t, nil)
	installProviders(t, &CustomProvider{ID: "prov_x", Name: "X", ModelIDs: []string{"m"}, Enabled: true})
	if got := PinUnavailableReason("m", "prov_missing"); !strings.Contains(got, `no provider matches "prov_missing"`) {
		t.Errorf("unknown pin: %q", got)
	}
	if got := PinUnavailableReason("m", "  "); !strings.Contains(got, "no provider was selected") {
		t.Errorf("empty pin: %q", got)
	}
}

func TestPinUnavailableReasonNamesEveryBlockerForASharedDisplayName(t *testing.T) {
	now := time.Now().UTC()
	installTestBudget(t, func() time.Time { return now })
	off := &CustomProvider{ID: "prov_s1", Name: "Shared", ModelIDs: []string{"m"}, Enabled: false}
	capped := &CustomProvider{ID: "prov_s2", Name: "Shared", ModelIDs: []string{"m"}, Enabled: true, DailyLimit: 1}
	installProviders(t, off, capped)
	admitTestUsage(t, *Find("prov_s2"), "m", "s2-attempt", "s2-tokens", now, 1)
	got := PinUnavailableReason("m", "Shared")
	if !strings.Contains(got, "is disabled") || !strings.Contains(got, "daily request limit reached") {
		t.Errorf("shared name must list both blockers, got %q", got)
	}
}

func TestPinUnavailableReasonSaysSoWhenTheProviderIsUsable(t *testing.T) {
	installTestBudget(t, nil)
	installProviders(t, &CustomProvider{ID: "prov_ok", Name: "OK", ModelIDs: []string{"m"}, Enabled: true})
	if got := PinUnavailableReason("m", "prov_ok"); !strings.Contains(got, "became available") {
		t.Errorf("usable provider: %q", got)
	}
}

// The explainer and the resolver share pinBlockLocked. This keeps them from
// drifting: whenever the resolver returns nobody, the explainer must name a
// blocker, and whenever it returns somebody the explainer must not.
func TestResolverAndExplainerAgree(t *testing.T) {
	now := time.Now().UTC()
	installTestBudget(t, func() time.Time { return now })
	installProviders(t,
		&CustomProvider{ID: "prov_g1", Name: "G1", ModelIDs: []string{"m"}, Enabled: true},
		&CustomProvider{ID: "prov_g2", Name: "G2", ModelIDs: []string{"m"}, Enabled: true, TokenLimit: 10, TokenWindow: "daily"},
		&CustomProvider{ID: "prov_g3", Name: "G3", ModelIDs: []string{"m"}, Enabled: false},
		&CustomProvider{ID: "prov_g4", Name: "G4", ModelIDs: []string{"z"}, Enabled: true},
	)
	if err := providerstate.Publish(providerstate.NewStore(func() time.Time { return now })); err != nil {
		t.Fatal(err)
	}
	admitTestUsage(t, *Find("prov_g2"), "m", "g2-attempt", "g2-tokens", now, 10)
	SetCooldown("prov_g1", "m", now.Add(time.Minute))
	for _, id := range []string{"prov_g1", "prov_g2", "prov_g3", "prov_g4"} {
		if got := ResolveNamedForModel("m", id); len(got) != 0 {
			t.Errorf("%s: resolver returned %d candidates, want none", id, len(got))
		}
		if reason := PinUnavailableReason("m", id); strings.Contains(reason, "became available") || reason == "" {
			t.Errorf("%s: resolver found nobody but the explainer said %q", id, reason)
		}
	}
	SetCooldown("prov_g1", "m", now.Add(-time.Second)) // expired cooldown
	if got := ResolveNamedForModel("m", "prov_g1"); len(got) != 1 {
		t.Fatalf("expired cooldown must free the provider, got %d candidates", len(got))
	}
}

func TestGroupThousandsAndKindLabels(t *testing.T) {
	for in, want := range map[int64]string{0: "0", 7: "7", 999: "999", 1000: "1,000", 3034885: "3,034,885", -12345: "-12,345"} {
		if got := groupThousands(in); got != want {
			t.Errorf("groupThousands(%d) = %q, want %q", in, got, want)
		}
	}
	if got := ExhaustionKindLabel("something_new"); got != "something_new" {
		t.Errorf("unknown kind must pass through, got %q", got)
	}
}
