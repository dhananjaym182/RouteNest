package providers

import (
	"strings"
	"testing"

	"routenest/internal/accounts"
	"routenest/internal/domain"
)

func TestUpstreamModelForFallsBackToTheName(t *testing.T) {
	p := CustomProvider{ModelMap: map[string]string{"friendly": " vendor/real "}}
	if got := UpstreamModelFor(p, "friendly"); got != "vendor/real" {
		t.Errorf("mapped name = %q, want trimmed vendor/real", got)
	}
	if got := UpstreamModelFor(p, "other"); got != "other" {
		t.Errorf("unmapped name = %q, want unchanged", got)
	}
	if got := UpstreamModelFor(CustomProvider{}, "x"); got != "x" {
		t.Errorf("nil map = %q, want unchanged", got)
	}
	if got := UpstreamModelFor(CustomProvider{ModelMap: map[string]string{"x": "  "}}, "x"); got != "x" {
		t.Errorf("blank mapping = %q, want the name", got)
	}
}

func TestSanitizeModelMap(t *testing.T) {
	declared := []string{"a", "b", "c"}
	got, err := SanitizeModelMap(declared, map[string]string{
		" a ":  " up/a ", // trimmed
		"b":    "b",      // identity: carries no information
		"c":    "",       // blank: means "send the name"
		"gone": "x",      // not declared
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["a"] != "up/a" {
		t.Errorf("sanitized = %v, want only a -> up/a", got)
	}
	if none, _ := SanitizeModelMap(declared, map[string]string{"b": "b"}); none != nil {
		t.Errorf("all-identity map = %v, want nil so it is omitted from JSON", none)
	}
	for name, bad := range map[string]string{"control": "up\n/a", "long": strings.Repeat("x", modelMapMaxLen+1)} {
		if _, err := SanitizeModelMap(declared, map[string]string{"a": bad}); err == nil {
			t.Errorf("%s upstream ID was accepted, want an error", name)
		}
	}
}

func TestSaveCustomProviderSanitizesModelMap(t *testing.T) {
	installOfferingStateTest(t, nil)
	saved, err := SaveCustomProvider(CustomProvider{
		Name: "Mapped", BaseURL: "http://mapped.test/v1", Enabled: true,
		ModelIDs: []string{"friendly", "plain"},
		ModelMap: map[string]string{"friendly": "vendor/real", "plain": "plain", "undeclared": "zzz"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.ModelMap) != 1 || saved.ModelMap["friendly"] != "vendor/real" {
		t.Errorf("saved map = %v, want only friendly -> vendor/real", saved.ModelMap)
	}
	if _, err := SaveCustomProvider(CustomProvider{
		Name: "Bad", BaseURL: "http://bad.test/v1", ModelIDs: []string{"m"},
		ModelMap: map[string]string{"m": "bad\x00id"},
	}); err == nil {
		t.Error("control character in upstream ID was accepted by SaveCustomProvider")
	}
	// Removing a model from the list drops its mapping on the next save.
	saved.ModelIDs = []string{"plain"}
	again, err := SaveCustomProvider(saved)
	if err != nil {
		t.Fatal(err)
	}
	if again.ModelMap != nil {
		t.Errorf("map after removing the model = %v, want nil", again.ModelMap)
	}
}

func TestOfferingUpstreamIDFollowsModelMapOverStalePersistedState(t *testing.T) {
	p := &CustomProvider{ID: "mapped_offer", Name: "MappedOffer", BaseURL: "http://m.test/v1",
		ModelIDs: []string{offeringStateSharedModel}, Enabled: true,
		ModelMap: map[string]string{offeringStateSharedModel: "vendor/new-id"}}
	installOfferingStateTest(t, nil, p)
	provRecheckMu.Lock()
	providerRecheckStat[p.ID] = &providerRecheckState{Offerings: map[string]domain.ModelOfferingState{
		offeringStateSharedModel: {UpstreamModelID: "vendor/stale-id", Cost: "free"},
	}}
	provRecheckMu.Unlock()
	if got := offeringForStateTest(t, p.ID); got.UpstreamModelID != "vendor/new-id" {
		t.Errorf("offering upstream = %q, want the ModelMap value vendor/new-id, not the stale persisted one", got.UpstreamModelID)
	}
	if got := offeringForStateTest(t, p.ID); got.ModelID != offeringStateSharedModel {
		t.Errorf("offering model = %q, want the client-visible name to stay %q", got.ModelID, offeringStateSharedModel)
	}
}

// A mapped name is served by its upstream ID, so recheck must neither delist it
// while that ID is in the catalogue nor offer that ID again as a new model.
func TestRecheckTreatsMappedNameAsPresentWhenItsUpstreamIDIs(t *testing.T) {
	isolateProviderRecheck(t)
	up := &mutableUpstream{}
	srv := up.server()
	defer srv.Close()
	accounts.Pool = &AccountPool{Accounts: []*Account{}, Models: []Model{
		{ID: "friendly", Provider: "MapProv", Cost: "free", Status: "active", Source: "provider"},
		{ID: "plain", Provider: "MapProv", Cost: "free", Status: "active", Source: "provider"},
	}}
	p := CustomProvider{ID: "prov_map", Name: "MapProv", BaseURL: srv.URL, Enabled: true,
		ModelIDs: []string{"friendly", "plain"}, ModelMap: map[string]string{"friendly": "vendor/real"}}

	up.set([]string{"vendor/real", "plain"})
	for i := 0; i < providerRecheckMissLimit+1; i++ {
		st := recheckProviderModels(p)
		if st.LastError != "" {
			t.Fatalf("recheck error: %s", st.LastError)
		}
		if st.NewQueued != 0 {
			t.Fatalf("round %d: upstream ID of a mapped model was re-offered as new (queued=%d)", i, st.NewQueued)
		}
	}
	if m, _ := poolModelByID("friendly"); m.Delisted {
		t.Error("mapped model was delisted although its upstream ID is in the catalogue")
	}

	// When the upstream ID itself disappears, the mapped model is delisted like any other.
	up.set([]string{"plain"})
	for i := 0; i < providerRecheckMissLimit; i++ {
		recheckProviderModels(p)
	}
	if m, _ := poolModelByID("friendly"); !m.Delisted {
		t.Error("mapped model must be delisted after its upstream ID is missing repeatedly")
	}
}
