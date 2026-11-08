package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"routenest/internal/accounts"
	"routenest/internal/adminauth"
	"routenest/internal/config"
	"routenest/internal/domain"
	"routenest/internal/models"
	"routenest/internal/providers"
)

const modelRemovalURL = "/admin/api/models/delisted/remove"

func TestDelistedModelRemovalRequiresProviderPermissionAndCSRF(t *testing.T) {
	registry := newAdminRouteRegistry()
	registerModelsRoutes(registry)
	policy, exists := registry.Policy(http.MethodPost, modelRemovalURL)
	if !exists || policy.CSRF != csrfSession || !reflect.DeepEqual(policy.Permissions, []adminauth.Permission{adminauth.PermissionProviderWrite}) {
		t.Fatalf("delisted cleanup security policy = %+v, registered=%v", policy, exists)
	}
}

func TestDelistedModelRemovalAdapter(t *testing.T) {
	const helper = "ROUTENEST_MODEL_REMOVAL_HELPER"
	if os.Getenv(helper) != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestDelistedModelRemovalAdapter$")
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, helper+"=") && !strings.HasPrefix(entry, "ROUTENEST_DATA_DIR=") && !strings.HasPrefix(entry, "CLINE2API_DATA_DIR=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, helper+"=1", "ROUTENEST_DATA_DIR="+t.TempDir())
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated cleanup regression: %v\n%s", err, out)
		}
		return
	}
	path := os.Getenv("ROUTENEST_DATA_DIR")
	accounts.PoolMu.Lock()
	accounts.PoolPath = filepath.Join(path, ".routenest-accounts.json")
	accounts.Pool = &domain.AccountPool{DefaultModel: "retired", Models: []domain.Model{
		{ID: "retired", Source: models.SourceRemote, Delisted: true},
		{ID: "shared", Source: models.SourceRemote, Delisted: true},
		{ID: "shared", Source: models.SourceZen, Status: "active"},
		{ID: "live", Source: models.SourceRemote, Status: "active"},
		{ID: "manual", Source: models.SourceRemote, Custom: true, Delisted: true},
	}}
	accounts.PoolMu.Unlock()
	providers.Mu.Lock()
	providers.Loaded = true
	providers.FilePath = filepath.Join(path, ".routenest-providers.json")
	providers.CustomProviders = nil
	providers.Mu.Unlock()
	cfg := config.DefaultProxy()
	cfg.ModelChain = []string{"retired", "shared", "live"}
	cfg.ModelAliases = []config.ModelAlias{{Alias: "old", Target: "retired"}, {Alias: "shared-alias", Target: "shared"}}
	cfg.Combos = []domain.ModelCombo{{ID: "combo", Enabled: true, Entries: []domain.ComboEntry{
		{Model: "retired"}, {Model: "shared", Source: "cline"}, {Model: "shared", Source: "zen"}, {Model: "shared"},
	}}}
	config.Publish(cfg)
	registry := newAdminRouteRegistry()
	registerModelsRoutes(registry)
	request := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		registry.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, modelRemovalURL, strings.NewReader(body)))
		return rec
	}
	for _, body := range []string{`{}`, `{"models":[]}`, `{"models":[{"id":"retired","source":"unknown"}]}`, `{"models":[{"id":"shared","source":"provider"}]}`, `{"models":[{"id":"retired","source":"remote","providerId":"beta"}]}`, `{"models":[{"id":"retired","source":"remote"}],"all":true}`, `{"models":[{"id":"retired","source":"remote"}]} {}`} {
		if rec := request(body); rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid identity/body accepted: %d %s", rec.Code, rec.Body.String())
		}
	}
	rec := request(`{"models":[{"id":"retired","source":"remote"},{"id":"shared","source":"remote"},{"id":"live","source":"remote"},{"id":"manual","source":"remote"},{"id":"missing","source":"zen"}]}`)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Removed []map[string]string `json:"removed"`
			Skipped []map[string]string `json:"skipped"`
			Errors  []map[string]any    `json:"errors"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || rec.Code != http.StatusOK || !response.Success || len(response.Data.Removed) != 2 || len(response.Data.Skipped) != 3 || len(response.Data.Errors) != 0 {
		t.Fatalf("cleanup response = %d %s, decode=%v", rec.Code, rec.Body.String(), err)
	}
	left := models.Snapshot()
	if len(left) != 3 || left[0].ID != "shared" || left[0].Source != models.SourceZen || left[1].ID != "live" || !left[2].Custom {
		t.Fatalf("source cleanup removed live/manual records or kept delisted rows: %+v", left)
	}
	accounts.PoolMu.Lock()
	def := accounts.Pool.DefaultModel
	accounts.PoolMu.Unlock()
	if def != "" {
		t.Fatalf("retired default remains: %q", def)
	}
	current := config.Live()
	if !reflect.DeepEqual(current.ModelChain, []string{"shared", "live"}) || len(current.ModelAliases) != 1 || len(current.Combos[0].Entries) != 2 || current.Combos[0].Entries[0].Source != "zen" {
		t.Fatalf("routing references not scoped/pruned: %+v", current)
	}
	if rec := request(`{"models":[{"id":"retired","source":"remote"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("repeat cleanup is not idempotent: %d %s", rec.Code, rec.Body.String())
	}
}
