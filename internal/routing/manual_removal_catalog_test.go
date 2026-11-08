package routing

import (
	"testing"

	"routenest/internal/accounts"
	"routenest/internal/domain"
	"routenest/internal/models"
	"routenest/internal/zen"
)

func TestManualBuiltinReservationKeepsOfflineSiblingsAfterRestart(t *testing.T) {
	removedID := builtinModels[0].ID
	pool := &domain.AccountPool{DefaultModel: removedID}
	isolateRemovedCatalog(t, pool)
	models.ReserveOfferingRemoval(pool, domain.ModelRemovalRef{ID: removedID, Source: models.SourceRemote, ProviderID: "cline"})
	if err := accounts.SavePoolStrict(); err != nil {
		t.Fatal(err)
	}
	for _, restart := range []bool{false, true} {
		if restart {
			accounts.Pool = nil
			accounts.LoadPool()
		}
		got := AllModels()
		if len(got) != len(builtinModels)+len(zen.SeedModels)-1 {
			t.Fatalf("restart=%v lost unrelated offline siblings: %+v", restart, got)
		}
		for _, model := range got {
			if model.ID == removedID {
				t.Fatalf("restart=%v restored the removed model: %+v", restart, model)
			}
		}
		if model := DefaultModel(); model == removedID || model == "" {
			t.Fatalf("restart=%v default ignored provider removal: %q", restart, model)
		}
		for _, model := range defaultFreeChain() {
			if model == removedID {
				t.Fatalf("restart=%v free chain restored removed model: %v", restart, defaultFreeChain())
			}
		}
	}
}

func TestManualClineReservationDoesNotRemoveSameIDZenOffering(t *testing.T) {
	id := "org/shared@revision"
	pool := &domain.AccountPool{DefaultModel: id, ModelCatalogInitialized: true, Models: []domain.Model{
		{ID: id, Source: models.SourceZen, Cost: "free", Status: "active"},
		{ID: id, Source: models.SourceProvider, Provider: "AntSeed", Cost: "free", Status: "active"},
	}}
	isolateRemovedCatalog(t, pool)
	models.ReserveOfferingRemoval(pool, domain.ModelRemovalRef{ID: id, Source: models.SourceRemote, ProviderID: "cline"})
	if got := AllModels(); len(got) != 2 || DefaultModel() != id {
		t.Fatalf("removed a sibling/default: models=%+v default=%q", got, DefaultModel())
	}
}

func TestManualReservationsPreventSyntheticClineResurrection(t *testing.T) {
	id := "org/removed-model"
	pool := &domain.AccountPool{}
	isolateRemovedCatalog(t, pool)
	models.ReserveOfferingRemoval(pool, domain.ModelRemovalRef{ID: id, Source: models.SourceRemote, ProviderID: "cline"})
	input := CandidatePlanInput{Instances: []domain.ProviderInstance{{ID: "cline", Enabled: true}}}
	ensureLegacyClineOfferings(&input, []string{id, "still-supported-model"})
	if len(input.Offerings) != 1 || input.Offerings[0].ModelID != "still-supported-model" {
		t.Fatalf("synthetic fallback restored a removed ID: %+v", input.Offerings)
	}
}

func TestRemovingEveryBuiltinFreeFallbackDoesNotRestoreConstantChain(t *testing.T) {
	pool := &domain.AccountPool{}
	isolateRemovedCatalog(t, pool)
	for _, id := range FreeModelChain {
		models.ReserveOfferingRemoval(pool, domain.ModelRemovalRef{ID: id, Source: models.SourceRemote, ProviderID: "cline"})
	}
	if got := defaultFreeChain(); len(got) != 0 {
		t.Fatalf("empty filtered chain restored constants: %v", got)
	}
}
