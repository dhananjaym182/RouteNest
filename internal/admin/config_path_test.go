package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"routenest/internal/accounts"
)

func TestAdminConfigUsesResolvedPoolPath(t *testing.T) {
	accounts.PoolMu.Lock()
	originalPool, originalPath := accounts.Pool, accounts.PoolPath
	path := filepath.Join(t.TempDir(), ".routenest-accounts.json")
	accounts.Pool, accounts.PoolPath = nil, path
	accounts.PoolMu.Unlock()
	t.Cleanup(func() {
		accounts.PoolMu.Lock()
		defer accounts.PoolMu.Unlock()
		accounts.Pool, accounts.PoolPath = originalPool, originalPath
	})

	response := httptest.NewRecorder()
	adminConfig.Get(response, httptest.NewRequest(http.MethodGet, "/admin/api/config", nil))
	var envelope struct {
		Data struct {
			PoolPath string `json:"poolPath"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.PoolPath != path {
		t.Fatalf("config pool path = %q, want the resolved runtime path %q", envelope.Data.PoolPath, path)
	}
}
