package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/domain"
	"routenest/internal/models"
	"routenest/internal/providers"
	"routenest/internal/store/syncstate"
)

// Contract tests for the model import and cost adapters. The workflow itself is
// tested in internal/providers.
func TestModelImportAdapterContracts(t *testing.T) {
	type envelope struct {
		Error string `json:"error"`
	}
	cases := []struct {
		name    string
		call    func(http.ResponseWriter, *http.Request)
		method  string
		body    string
		status  int
		message string
	}{
		{"preview wrong method", handleModelImportPreview, http.MethodGet, ``, http.StatusMethodNotAllowed, "method_not_allowed"},
		{"preview invalid json", handleModelImportPreview, http.MethodPost, `{`, http.StatusBadRequest, "invalid_json"},
		{"preview unknown provider", handleModelImportPreview, http.MethodPost, `{"id":"prov_missing"}`, http.StatusNotFound, "provider not found"},
		{"apply wrong method", handleModelImportApply, http.MethodGet, ``, http.StatusMethodNotAllowed, "method_not_allowed"},
		{"apply invalid json", handleModelImportApply, http.MethodPost, `{`, http.StatusBadRequest, "invalid_json"},
		{"apply unknown batch", handleModelImportApply, http.MethodPost, `{"batchId":"nope"}`, http.StatusNotFound, "batch not found (already applied or expired)"},
		{"discard invalid json", handleModelImportDiscard, http.MethodPost, `{`, http.StatusBadRequest, "invalid_json"},
		{"batch unknown id", handleModelImportBatch, http.MethodGet, ``, http.StatusNotFound, "batch not found (already applied or expired)"},
		{"cost wrong method", handleModelCost, http.MethodGet, ``, http.StatusMethodNotAllowed, "method_not_allowed"},
		{"cost invalid json", handleModelCost, http.MethodPost, `{`, http.StatusBadRequest, "invalid_json"},
		{"cost bad value", handleModelCost, http.MethodPost, `{"id":"m","cost":"gold"}`, http.StatusBadRequest, "id and cost(free|pass|empty) required"},
		{"cost unknown model", handleModelCost, http.MethodPost, `{"id":"model_missing","cost":"free"}`, http.StatusNotFound, "model not found"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		url := "/x"
		if tc.name == "batch unknown id" {
			url = "/x?id=nope"
		}
		tc.call(rec, httptest.NewRequest(tc.method, url, strings.NewReader(tc.body)))
		var env envelope
		json.Unmarshal(rec.Body.Bytes(), &env)
		if rec.Code != tc.status || env.Error != tc.message {
			t.Errorf("%s: got %d %q, want %d %q", tc.name, rec.Code, env.Error, tc.status, tc.message)
		}
	}
}

// Upstream discovery failure is a 200 with {error}, not an HTTP error.
func TestModelImportPreviewDiscoveryFailureEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	rec := httptest.NewRecorder()
	handleModelImportPreview(rec, httptest.NewRequest(http.MethodPost, "/x",
		strings.NewReader(`{"baseURL":"`+srv.URL+`","name":"draft"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if !env.Success || env.Data["error"] == nil || env.Data["error"] == "" || env.Data["batch"] != nil {
		t.Fatalf("envelope = %s", rec.Body.String())
	}
}

// The pending list keeps its envelope key even when nothing is pending.
func TestModelImportPendingEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	handleModelImportPending(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	var env struct {
		Success bool `json:"success"`
		Data    struct {
			Batches []map[string]any `json:"batches"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if !env.Success || env.Data.Batches == nil {
		t.Fatalf("envelope = %s", rec.Body.String())
	}
}

func TestModelCostRequiresProviderForAmbiguousOffering(t *testing.T) {
	const helperEnv = "ROUTENEST_MODEL_COST_HELPER"
	if os.Getenv(helperEnv) != "1" {
		dataDir := t.TempDir()
		cmd := exec.Command(os.Args[0], "-test.run=^TestModelCostRequiresProviderForAmbiguousOffering$")
		env := make([]string, 0, len(os.Environ())+2)
		for _, entry := range os.Environ() {
			if strings.HasPrefix(entry, helperEnv+"=") || strings.HasPrefix(entry, "ROUTENEST_DATA_DIR=") || strings.HasPrefix(entry, "CLINE2API_DATA_DIR=") {
				continue
			}
			env = append(env, entry)
		}
		cmd.Env = append(env, helperEnv+"=1", "ROUTENEST_DATA_DIR="+dataDir)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated provider-cost test failed: %v\n%s", err, output)
		}
		return
	}

	dataDir := os.Getenv("ROUTENEST_DATA_DIR")
	providers.Mu.Lock()
	providers.FilePath = filepath.Join(dataDir, ".routenest-providers.json")
	providers.CooldownsPath = filepath.Join(dataDir, ".routenest-providers-cooldowns.json")
	providers.CustomProviders = []*providers.CustomProvider{
		{ID: "alpha", Name: "Alpha", Enabled: true, ModelIDs: []string{"shared/model"}},
		{ID: "beta", Name: "Beta", Enabled: true, ModelIDs: []string{"shared/model"}},
	}
	providers.Loaded = true
	providers.Cooldowns = map[string]time.Time{}
	providers.Mu.Unlock()

	accounts.PoolMu.Lock()
	accounts.PoolPath = filepath.Join(dataDir, ".routenest-accounts.json")
	accounts.Pool = &domain.AccountPool{Models: []domain.Model{{
		ID: "shared/model", Provider: "Alpha", Source: models.SourceProvider,
		Cost: models.CostPass, TierSignal: "pricing-paid", Status: "active",
	}}}
	accounts.PoolMu.Unlock()

	ambiguous := httptest.NewRecorder()
	handleModelCost(ambiguous, httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"id":"shared/model","cost":"free"}`)))
	if ambiguous.Code != http.StatusConflict {
		t.Fatalf("ambiguous status = %d body=%s, want 409", ambiguous.Code, ambiguous.Body.String())
	}

	scoped := httptest.NewRecorder()
	handleModelCost(scoped, httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"providerId":"beta","id":"shared/model","cost":"free"}`)))
	if scoped.Code != http.StatusOK {
		t.Fatalf("provider-scoped status = %d body=%s, want 200", scoped.Code, scoped.Body.String())
	}

	states, err := syncstate.Load(filepath.Join(dataDir, syncstate.FileName))
	if err != nil || states["beta"] == nil {
		t.Fatalf("provider state = %+v err=%v", states, err)
	}
	beta := states["beta"].Offerings["shared/model"]
	if beta.Cost != models.CostFree || beta.TierSignal != "manual" || beta.Manual == nil || !*beta.Manual {
		t.Fatalf("Beta scoped override = %+v", beta)
	}
	if alpha := states["alpha"]; alpha != nil && len(alpha.Offerings) != 0 {
		t.Fatalf("Beta scoped override created Alpha state: %+v", alpha)
	}
	accounts.PoolMu.Lock()
	legacy := accounts.Pool.Models[0]
	accounts.PoolMu.Unlock()
	if legacy.Cost != models.CostPass || legacy.TierSignal != "pricing-paid" {
		t.Fatalf("Beta scoped override changed the shared compatibility row: %+v", legacy)
	}
}
