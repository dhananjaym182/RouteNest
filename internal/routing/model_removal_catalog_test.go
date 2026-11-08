package routing

import (
	"path/filepath"
	"testing"

	"routenest/internal/accounts"
	"routenest/internal/domain"
	"routenest/internal/models"
	"routenest/internal/zen"
)

func isolateRemovedCatalog(t *testing.T, pool *domain.AccountPool) {
	t.Helper()
	oldPool, oldPath := accounts.Pool, accounts.PoolPath
	accounts.PoolPath = filepath.Join(t.TempDir(), ".routenest-accounts.json")
	accounts.Pool = pool
	remoteModelsEnabledMu.Lock()
	oldRemote := remoteModelsEnabled
	remoteModelsEnabled = false
	remoteModelsEnabledMu.Unlock()
	zen.RemoteEnabledMu.Lock()
	oldZen := zen.RemoteEnabled
	zen.RemoteEnabled = false
	zen.RemoteEnabledMu.Unlock()
	t.Cleanup(func() {
		accounts.Pool, accounts.PoolPath = oldPool, oldPath
		remoteModelsEnabledMu.Lock()
		remoteModelsEnabled = oldRemote
		remoteModelsEnabledMu.Unlock()
		zen.RemoteEnabledMu.Lock()
		zen.RemoteEnabled = oldZen
		zen.RemoteEnabledMu.Unlock()
	})
}

func TestRemovedLastSyncedModelDoesNotReturnAsOfflineSeedAfterRestart(t *testing.T) {
	modelID := builtinModels[0].ID
	isolateRemovedCatalog(t, &domain.AccountPool{DefaultModel: modelID, Models: []domain.Model{{ID: modelID, Source: models.SourceRemote, Delisted: true, Cost: "free", Status: "active"}}})
	if removed, _, err := models.ReconcileRemoval(domain.ModelRemovalRef{ID: modelID, Source: models.SourceRemote}, nil, nil, false); err != nil || !removed {
		t.Fatalf("cleanup = %v, %v", removed, err)
	}
	for _, restart := range []bool{false, true} {
		if restart {
			accounts.Pool = nil
			accounts.LoadPool()
		}
		if got := AllModels(); len(got) != 0 {
			t.Fatalf("restart=%v restored offline seeds after removal: %+v", restart, got)
		}
		if got := DefaultModel(); got != "" {
			t.Fatalf("restart=%v restored retired default: %q", restart, got)
		}
		if got := defaultFreeChain(); len(got) != 0 {
			t.Fatalf("restart=%v restored retired free chain: %v", restart, got)
		}
	}
}

func TestCustomOnlyUnsyncedPoolRetainsOfflineCatalog(t *testing.T) {
	isolateRemovedCatalog(t, &domain.AccountPool{Models: []domain.Model{{ID: "manual-model", Custom: true, Status: "active"}}})
	if got := AllModels(); len(got) <= 1 {
		t.Fatalf("unsynced custom-only pool lost offline catalog: %+v", got)
	}
	if got := defaultFreeChain(); len(got) == 0 {
		t.Fatal("unsynced pool lost offline free chain")
	}
}
