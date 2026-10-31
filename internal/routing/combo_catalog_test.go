package routing

import (
	"testing"

	"routenest/internal/domain"
)

// fakeCombos is a ComboCatalog that lists exactly one enabled combo.
type fakeCombos struct{ id string }

func (f fakeCombos) FindCombo(id string) (domain.ModelCombo, bool) {
	if id == f.id {
		return domain.ModelCombo{ID: f.id}, true
	}
	return domain.ModelCombo{}, false
}

func (f fakeCombos) EnabledCombos() []domain.ModelCombo {
	return []domain.ModelCombo{{ID: f.id}}
}

// An installed combo source is consulted by the /v1/models listing and by
// the combo lookup. Installing nil restores the empty catalog.
func TestComboCatalogPortDrivesListingAndLookup(t *testing.T) {
	t.Cleanup(func() { SetComboCatalog(testCombos{}) })

	SetComboCatalog(fakeCombos{id: "port-combo"})
	if !HasComboCatalog() {
		t.Fatal("combo catalog should report installed")
	}
	if _, ok := comboCatalog.FindCombo("port-combo"); !ok {
		t.Fatal("installed combo must be found")
	}
	found := false
	for _, entry := range BuildModelsList(false) {
		if entry["id"] == "port-combo" && entry["owned_by"] == "combo" {
			found = true
		}
	}
	if !found {
		t.Fatal("installed combo must appear in the model listing")
	}

	SetComboCatalog(nil)
	if HasComboCatalog() {
		t.Fatal("nil install must restore the empty catalog")
	}
	for _, entry := range BuildModelsList(false) {
		if entry["owned_by"] == "combo" {
			t.Fatalf("empty catalog still lists a combo: %v", entry["id"])
		}
	}
}
