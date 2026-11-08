package models

import (
	"reflect"
	"testing"

	"routenest/internal/accounts"
	"routenest/internal/domain"
	poolstore "routenest/internal/store/accounts"
)

const explicitRemovalLiteralID = "org/shared-model@revision"

func TestReconcileExplicitRemovalKeepsBuiltinProviderSiblings(t *testing.T) {
	for _, target := range []struct{ source, providerID string }{{SourceRemote, "cline"}, {SourceZen, "opencode"}} {
		t.Run(target.providerID, func(t *testing.T) {
			pool := &domain.AccountPool{DefaultModel: explicitRemovalLiteralID, Models: []domain.Model{
				{ID: explicitRemovalLiteralID, Source: SourceRemote, Custom: true, Cost: "free", Status: "active"},
				{ID: explicitRemovalLiteralID, Source: SourceCustom, Custom: true, Status: "active"},
				{ID: explicitRemovalLiteralID, Source: SourceZen, Custom: true, Cost: "free", Status: "active"},
				{ID: explicitRemovalLiteralID, Source: SourceSeed, Cost: "free", Status: "active"},
				{ID: explicitRemovalLiteralID, Source: SourceProvider, Provider: "Sibling", Custom: true, Cost: "free", Status: "active"},
				{ID: "org/shared-model", Source: SourceCustom, Custom: true, Status: "active"},
			}}
			path := withPool(t, pool)
			ref := domain.ModelRemovalRef{ID: explicitRemovalLiteralID, Source: target.source, ProviderID: target.providerID}
			removed, err := ReconcileExplicitRemoval(ref, []ProviderClaim{{ID: explicitRemovalLiteralID, Provider: "Sibling", Cost: "free"}}, nil, true)
			if err != nil || !removed {
				t.Fatalf("explicit cleanup = %v, %v", removed, err)
			}
			for _, model := range pool.Models {
				if model.ID != explicitRemovalLiteralID {
					continue
				}
				if target.providerID == "cline" && (model.Source == SourceRemote || model.Source == SourceCustom) || target.providerID == "opencode" && (model.Source == SourceZen || model.Source == SourceSeed) {
					t.Fatalf("requested provider record survived: %+v", model)
				}
			}
			if len(pool.Models) != 4 || pool.DefaultModel != explicitRemovalLiteralID || !OfferingRemovalReserved(pool, target.providerID, explicitRemovalLiteralID) || OfferingRemovalReserved(pool, "other", explicitRemovalLiteralID) {
				t.Fatalf("source/default/reservation isolation failed: models=%+v default=%q reservations=%+v", pool.Models, pool.DefaultModel, pool.RemovedModelOfferings)
			}
			stored, err := poolstore.Load(path)
			if err != nil || !reflect.DeepEqual(stored.RemovedModelOfferings, pool.RemovedModelOfferings) {
				t.Fatalf("removal reservation did not persist: stored=%+v err=%v", stored, err)
			}
			if again, err := ReconcileExplicitRemoval(ref, []ProviderClaim{{ID: explicitRemovalLiteralID, Provider: "Sibling", Cost: "free"}}, nil, true); err != nil || again {
				t.Fatalf("repeat removal = %v, %v", again, err)
			}
		})
	}
}

func TestReconcileExplicitRemovalDeletesCustomProviderProjection(t *testing.T) {
	pool := &domain.AccountPool{DefaultModel: explicitRemovalLiteralID, Models: []domain.Model{{
		ID: explicitRemovalLiteralID, Source: SourceProvider, Provider: "AntSeed", Custom: true, Cost: "free", Status: "active",
	}}}
	withPool(t, pool)
	if removed, err := ReconcileExplicitRemoval(domain.ModelRemovalRef{ID: explicitRemovalLiteralID, Source: SourceProvider, ProviderID: "antseed", Provider: "AntSeed"}, nil, nil, false); err != nil || !removed {
		t.Fatalf("custom explicit cleanup = %v, %v", removed, err)
	}
	if len(pool.Models) != 0 || pool.DefaultModel != "" || len(pool.RemovedModelOfferings) != 0 {
		t.Fatalf("custom provider cleanup left model/default or created a builtin reservation: %+v", pool)
	}
}

func TestReconcileExplicitRemovalPreservesGlobalDisabledFlag(t *testing.T) {
	pool := &domain.AccountPool{Models: []domain.Model{{
		ID: explicitRemovalLiteralID, Source: SourceProvider, Provider: "AntSeed", Disabled: true,
		Custom: true, Context: 131072, Output: 8192, Cost: "pass", TierSignal: "manual", Status: "active",
	}}}
	path := withPool(t, pool)
	ref := domain.ModelRemovalRef{ID: explicitRemovalLiteralID, Source: SourceProvider, ProviderID: "antseed", Provider: "AntSeed"}
	if _, err := ReconcileExplicitRemoval(ref, []ProviderClaim{{ID: explicitRemovalLiteralID, Provider: "Sibling", Cost: "free", TierSignal: "classified-free"}}, nil, true); err != nil {
		t.Fatalf("shared provider cleanup failed: %v", err)
	}
	if len(pool.Models) != 1 || !pool.Models[0].Disabled {
		t.Fatalf("provider removal re-enabled a globally disabled shared model: %+v", pool.Models)
	}
	row := pool.Models[0]
	if row.Provider != "Sibling" || row.Cost != "free" || row.TierSignal != "classified-free" || row.Custom || row.Context != 0 || row.Output != 0 {
		t.Fatalf("surviving provider inherited removed provider state: %+v", row)
	}
	stored, err := poolstore.Load(path)
	if err != nil || len(stored.Models) != 1 || !stored.Models[0].Disabled {
		t.Fatalf("global disabled flag did not survive persisted reload: models=%+v err=%v", stored.Models, err)
	}
}

func TestReconcileExplicitRemovalPreservesDeclaredLiteralDefault(t *testing.T) {
	for _, target := range []struct{ source, providerID, defaultID string }{
		{SourceProvider, "antseed", "base@antseed"},
		{SourceRemote, "cline", "cl/base"},
	} {
		t.Run(target.providerID, func(t *testing.T) {
			literal := domain.Model{ID: target.defaultID, Source: SourceCustom, Custom: true, Cost: "free", Status: "active"}
			pool := &domain.AccountPool{DefaultModel: target.defaultID, Models: []domain.Model{
				{ID: "base", Source: target.source, Provider: "AntSeed", Custom: true, Status: "active"}, literal,
			}}
			path := withPool(t, pool)
			ref := domain.ModelRemovalRef{ID: "base", Source: target.source, ProviderID: target.providerID, Provider: "AntSeed"}
			if _, err := ReconcileExplicitRemoval(ref, []ProviderClaim{{ID: "base", Provider: "Sibling", Cost: "free"}}, nil, true); err != nil {
				t.Fatalf("provider cleanup failed: %v", err)
			}
			if pool.DefaultModel != target.defaultID {
				t.Fatalf("removal interpreted a declared literal default as a provider pin: got=%q want=%q", pool.DefaultModel, target.defaultID)
			}
			found := false
			for _, row := range pool.Models {
				found = found || reflect.DeepEqual(row, literal)
			}
			if !found {
				t.Fatalf("separate literal default model changed: %+v", pool.Models)
			}
			stored, err := poolstore.Load(path)
			if err != nil || stored.DefaultModel != target.defaultID {
				t.Fatalf("literal default did not survive persisted reload: default=%q err=%v", stored.DefaultModel, err)
			}
		})
	}
}

func TestReconcileExplicitRemovalReservationAndPoolMutationRollbackTogether(t *testing.T) {
	pool := &domain.AccountPool{DefaultModel: explicitRemovalLiteralID, Models: []domain.Model{{ID: explicitRemovalLiteralID, Source: SourceRemote, Cost: "free", Custom: true, Status: "active"}}}
	path := withPool(t, pool)
	before := Snapshot()
	accounts.PoolPath = t.TempDir()
	ref := domain.ModelRemovalRef{ID: explicitRemovalLiteralID, Source: SourceRemote, ProviderID: "cline"}
	if removed, err := ReconcileExplicitRemoval(ref, nil, nil, false); err == nil || removed {
		t.Fatalf("failed persistence reported successful removal: %v, %v", removed, err)
	}
	if !reflect.DeepEqual(pool.Models, before) || pool.DefaultModel != explicitRemovalLiteralID || pool.ModelCatalogInitialized || len(pool.RemovedModelOfferings) != 0 {
		t.Fatalf("failed pool write leaked deletion/reservation/default changes: %+v", pool)
	}
	accounts.PoolPath = path
	if removed, err := ReconcileExplicitRemoval(ref, nil, nil, false); err != nil || !removed {
		t.Fatalf("retry cleanup = %v, %v", removed, err)
	}
	stored, err := poolstore.Load(path)
	if err != nil || len(stored.Models) != 0 || stored.DefaultModel != "" || !stored.ModelCatalogInitialized || !OfferingRemovalReserved(stored, "cline", explicitRemovalLiteralID) {
		t.Fatalf("retry did not durably finish removal: %+v err=%v", stored, err)
	}
}

func TestBuiltinRemovalReservationFiltersSourceRefreshWithoutDelisting(t *testing.T) {
	pool := &domain.AccountPool{Models: []domain.Model{{ID: explicitRemovalLiteralID, Source: SourceRemote, Cost: "free", Status: "active"}, {ID: explicitRemovalLiteralID, Source: SourceZen, Cost: "free", Status: "active"}}}
	withPool(t, pool)
	if removed, err := ReconcileExplicitRemoval(domain.ModelRemovalRef{ID: explicitRemovalLiteralID, Source: SourceRemote, ProviderID: "cline"}, nil, nil, true); err != nil || !removed {
		t.Fatalf("explicit cleanup = %v, %v", removed, err)
	}
	update, err := ReplaceSource(SourceRemote, func([]domain.Model) []domain.Model {
		return []domain.Model{{ID: explicitRemovalLiteralID, Source: SourceRemote, Status: "active"}, {ID: "remote-keep", Source: SourceRemote, Status: "active"}}
	})
	if err != nil || update.Total != 1 || !reflect.DeepEqual(update.Added, []string{"remote-keep"}) {
		t.Fatalf("removed builtin model was announced/re-added by refresh: %+v, %v", update, err)
	}
	for _, model := range pool.Models {
		if model.ID == explicitRemovalLiteralID && model.Source == SourceRemote || model.Delisted {
			t.Fatalf("refresh resurrected or relabeled a manually removed model: %+v", pool.Models)
		}
	}
	if _, err := ReplaceSource(SourceZen, func([]domain.Model) []domain.Model {
		return []domain.Model{{ID: explicitRemovalLiteralID, Source: SourceZen, Status: "active"}}
	}); err != nil {
		t.Fatalf("sibling source refresh failed: %v", err)
	}
}

func TestAllRemovedSourceModelsRemainIntentionallyEmptyAfterRefresh(t *testing.T) {
	pool := &domain.AccountPool{Models: []domain.Model{{ID: explicitRemovalLiteralID, Source: SourceZen, Status: "active"}}}
	withPool(t, pool)
	if removed, err := ReconcileExplicitRemoval(domain.ModelRemovalRef{ID: explicitRemovalLiteralID, Source: SourceZen, ProviderID: "opencode"}, nil, nil, false); err != nil || !removed {
		t.Fatalf("explicit cleanup = %v, %v", removed, err)
	}
	update, err := ReplaceSource(SourceZen, func([]domain.Model) []domain.Model {
		return []domain.Model{{ID: explicitRemovalLiteralID, Source: SourceZen, Status: "active"}}
	})
	if err != nil || update.Total != 0 || len(update.Added) != 0 || len(update.Removed) != 0 || len(pool.Models) != 0 {
		t.Fatalf("fully suppressed source was treated as failure or resurrected: update=%+v models=%+v err=%v", update, pool.Models, err)
	}
}
