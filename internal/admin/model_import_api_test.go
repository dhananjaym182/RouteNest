package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
