package models

import (
	"reflect"
	"testing"

	"routenest/internal/accounts"
	"routenest/internal/domain"
	poolstore "routenest/internal/store/accounts"
)

func TestReconcileRemovalPreservesSameIDLiveAndCustomRecords(t *testing.T) {
	withPool(t, &domain.AccountPool{DefaultModel: "shared", Models: []domain.Model{
		{ID: "shared", Source: SourceRemote, Delisted: true},
		{ID: "shared", Source: SourceRemote, Delisted: true},
		{ID: "shared", Source: SourceZen},
		{ID: "shared", Custom: true, Delisted: true},
	}})
	removed, _, err := ReconcileRemoval(domain.ModelRemovalRef{ID: "shared", Source: SourceRemote}, nil, nil, false)
	if err != nil || !removed {
		t.Fatalf("removal = %v, %v", removed, err)
	}
	left := Snapshot()
	if len(left) != 2 || left[0].Source != SourceZen || !left[1].Custom || livePool().DefaultModel != "shared" {
		t.Fatalf("cleanup did not preserve other offerings and default: %+v", livePool())
	}
}

func TestReconcileRemovalDoesNotDeleteAnUnclaimedLiveProviderRow(t *testing.T) {
	withPool(t, &domain.AccountPool{Models: []domain.Model{{ID: "live-orphan", Source: SourceProvider, Provider: "Other", Status: "active"}}})
	before := Snapshot()
	if removed, _, err := ReconcileRemoval(domain.ModelRemovalRef{ID: "live-orphan", Source: SourceRemote}, nil, nil, false); err != nil || removed || !reflect.DeepEqual(Snapshot(), before) {
		t.Fatalf("unverified cleanup deleted live compatibility record: %v %v %+v", removed, err, Snapshot())
	}
}

func TestReconcileRemovalFailedSaveKeepsLivePoolUnchanged(t *testing.T) {
	withPool(t, &domain.AccountPool{DefaultModel: "retired", Models: []domain.Model{{ID: "retired", Source: SourceRemote, Delisted: true}}})
	before := Snapshot()
	accounts.PoolPath = t.TempDir()
	removed, _, err := ReconcileRemoval(domain.ModelRemovalRef{ID: "retired", Source: SourceRemote}, nil, nil, false)
	if removed || err == nil || !reflect.DeepEqual(before, Snapshot()) || livePool().DefaultModel != "retired" || livePool().ModelCatalogInitialized {
		t.Fatalf("failed save published partial cleanup: %v %v %+v", removed, err, livePool())
	}
}

func TestSourceSyncPersistsCatalogInitializationOnlyForValidSnapshots(t *testing.T) {
	for _, source := range []string{SourceRemote, SourceZen} {
		t.Run(source, func(t *testing.T) {
			path := withPool(t, &domain.AccountPool{})
			if _, err := ReplaceSource(source, func([]domain.Model) []domain.Model { return nil }); err == nil || livePool().ModelCatalogInitialized {
				t.Fatal("failed empty discovery initialized the catalogue")
			}
			if _, err := ReplaceSource(source, func([]domain.Model) []domain.Model { return []domain.Model{{ID: "new-model", Source: source}} }); err != nil {
				t.Fatal(err)
			}
			persisted, err := poolstore.Load(path)
			if err != nil || !persisted.ModelCatalogInitialized {
				t.Fatalf("successful source initialization not durable: %+v %v", persisted, err)
			}
		})
	}
}
