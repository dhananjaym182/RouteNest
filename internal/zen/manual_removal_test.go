package zen

import (
	"testing"

	"routenest/internal/accounts"
	"routenest/internal/domain"
	"routenest/internal/models"
)

func TestManualZenReservationExcludesSeedAndAliases(t *testing.T) {
	installZenSync(t, `{"data":[]}`, nil)
	seed := SeedModels[0]
	accounts.PoolMu.Lock()
	models.ReserveOfferingRemoval(accounts.Pool, domain.ModelRemovalRef{ID: seed.ID, Source: models.SourceZen, ProviderID: "opencode"})
	accounts.PoolMu.Unlock()
	if got := CurrentModels(); len(got) != len(SeedModels)-1 {
		t.Fatalf("removal lost untouched seeds: %+v", got)
	}
	for _, id := range append([]string{seed.ID, "opencode/" + seed.ID}, seed.Aliases...) {
		if model, ok := ResolveInfo(id); ok {
			t.Fatalf("removed seed still resolves %q: %+v", id, model)
		}
	}
	if _, ok := ResolveInfo(SeedModels[1].ID); !ok {
		t.Fatal("unremoved seed no longer resolves")
	}
}

func TestManualZenReservationFiltersPersistedAndOfflineSources(t *testing.T) {
	seed := SeedModels[0]
	installZenSync(t, `{"data":[]}`, []domain.Model{{ID: seed.ID, Source: models.SourceZen, Cost: "free"}})
	accounts.PoolMu.Lock()
	models.ReserveOfferingRemoval(accounts.Pool, domain.ModelRemovalRef{ID: seed.ID, Source: models.SourceZen, ProviderID: "opencode"})
	accounts.Pool.ModelCatalogInitialized = true
	accounts.PoolMu.Unlock()
	if _, ok := ResolveInfo(seed.ID); ok {
		t.Fatal("persisted removed Zen record still resolves")
	}
}
