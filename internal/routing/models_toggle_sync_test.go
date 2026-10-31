package routing

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"cline-go-proxy/internal/accounts"
)

// syncClineModels：重建 remote 条目时保留用户停用标记（Disabled），
// 周期同步不会把停用状态冲掉。
func TestSyncClineModelsPreservesDisabled(t *testing.T) {
	oldPool, oldPath, oldURL := accounts.Pool, accounts.PoolPath, clineRecommendedModelsURL
	oldSyncRan := SyncRan
	oldLast := lastModelSync
	oldBusy := modelSyncBusy
	t.Cleanup(func() {
		accounts.Pool, accounts.PoolPath, clineRecommendedModelsURL = oldPool, oldPath, oldURL
		SyncMu.Lock()
		SyncRan, lastModelSync, modelSyncBusy = oldSyncRan, oldLast, oldBusy
		SyncMu.Unlock()
	})
	accounts.PoolPath = filepath.Join(t.TempDir(), ".cline-accounts.json")
	accounts.Pool = &AccountPool{Accounts: []*Account{}, Keys: []string{}, Models: []Model{
		{ID: "prov/disabled-model", Provider: "prov", Cost: "free", Status: "active", Source: "remote", Disabled: true},
		{ID: "prov/enabled-model", Provider: "prov", Cost: "free", Status: "active", Source: "remote"},
	}}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// 两个模型都仍在官方列表里（同步会重建 remote 条目）
		w.Write([]byte(`{"free":[{"id":"prov/disabled-model","tags":["FREE"]},{"id":"prov/enabled-model","tags":["FREE"]}]}`))
	}))
	defer srv.Close()
	clineRecommendedModelsURL = srv.URL

	res := syncClineModels()
	if res.Error != "" {
		t.Fatalf("sync error: %s", res.Error)
	}

	byID := map[string]Model{}
	for _, m := range accounts.LoadPool().Models {
		byID[m.ID] = m
	}
	if m, ok := byID["prov/disabled-model"]; !ok {
		t.Fatal("disabled model missing after sync")
	} else if !m.Disabled {
		t.Error("Disabled flag must survive a sync that rebuilds the entry")
	}
	if m, ok := byID["prov/enabled-model"]; !ok {
		t.Fatal("enabled model missing after sync")
	} else if m.Disabled {
		t.Error("enabled model must not become disabled by sync")
	}
}
