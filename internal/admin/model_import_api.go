package admin

// HTTP adapters for the model import workflow and manual model cost. Discovery,
// batching and pool writes live in internal/providers and internal/models.

import (
	"encoding/json"
	"errors"
	"net/http"

	"routenest/internal/httpx"
	"routenest/internal/providers"
)

// POST /admin/api/models/import/preview
//
// Body {id} for a saved provider, or a draft {baseURL, apiKey, headers, free, name}.
// Upstream discovery failures return 200 with {error}, as the console expects.
func handleModelImportPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteAPI(w, http.StatusMethodNotAllowed, httpx.Response{Error: "method_not_allowed"})
		return
	}
	var req struct {
		ID      string            `json:"id"`
		BaseURL string            `json:"baseURL"`
		APIKey  string            `json:"apiKey"`
		Headers map[string]string `json:"headers"`
		Free    bool              `json:"free"`
		Name    string            `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "invalid_json"})
		return
	}
	batch, err := providers.PreviewImport(providers.ImportSource{
		ID: req.ID, Name: req.Name, BaseURL: req.BaseURL, APIKey: req.APIKey, Headers: req.Headers, Free: req.Free,
	})
	var discovery *providers.DiscoveryError
	switch {
	case errors.As(err, &discovery):
		httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"error": discovery.Message}})
	case errors.Is(err, providers.ErrProviderNotFound):
		httpx.WriteAPI(w, http.StatusNotFound, httpx.Response{Error: "provider not found"})
	case err != nil:
		httpx.WriteAPI(w, http.StatusInternalServerError, httpx.Response{Error: err.Error()})
	default:
		httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"batch": batch}})
	}
}

// POST /admin/api/models/import/apply  body: {batchId, approve: [modelId...]}
// Only approved add and tier_change items are written.
func handleModelImportApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteAPI(w, http.StatusMethodNotAllowed, httpx.Response{Error: "method_not_allowed"})
		return
	}
	var req struct {
		BatchID string   `json:"batchId"`
		Approve []string `json:"approve"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "invalid_json"})
		return
	}
	res, err := providers.ApplyImport(req.BatchID, req.Approve)
	if err != nil {
		if errors.Is(err, providers.ErrImportBatchNotFound) || errors.Is(err, providers.ErrImportProviderGone) {
			httpx.WriteAPI(w, http.StatusNotFound, httpx.Response{Error: err.Error()})
			return
		}
		httpx.WriteAPI(w, http.StatusInternalServerError, httpx.Response{Error: err.Error()})
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"added": res.Added, "changed": res.Changed}})
}

// GET /admin/api/models/import/pending: batches awaiting review, newest first.
func handleModelImportPending(w http.ResponseWriter, r *http.Request) {
	list := providers.PendingImports()
	out := make([]map[string]any, 0, len(list))
	for _, b := range list {
		out = append(out, map[string]any{
			"id": b.ID, "providerId": b.ProviderID, "providerName": b.ProviderName,
			"createdAt": b.CreatedAt, "items": b.Items, "auto": b.Auto,
		})
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"batches": out}})
}

// GET /admin/api/models/import/batch?id=x: one pending batch in full.
func handleModelImportBatch(w http.ResponseWriter, r *http.Request) {
	batch, err := providers.PendingImport(r.URL.Query().Get("id"))
	if err != nil {
		httpx.WriteAPI(w, http.StatusNotFound, httpx.Response{Error: err.Error()})
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"batch": batch}})
}

// POST /admin/api/models/import/discard  body: {batchId}
func handleModelImportDiscard(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BatchID string `json:"batchId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "invalid_json"})
		return
	}
	providers.DiscardImport(req.BatchID)
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true})
}

// POST /admin/api/models/cost  body: {id, cost: "free"|"pass"|""}
func handleModelCost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteAPI(w, http.StatusMethodNotAllowed, httpx.Response{Error: "method_not_allowed"})
		return
	}
	var req struct {
		ID   string `json:"id"`
		Cost string `json:"cost"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "invalid_json"})
		return
	}
	switch err := providers.SetModelCost(req.ID, req.Cost); {
	case errors.Is(err, providers.ErrInvalidModelCost):
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: err.Error()})
	case errors.Is(err, providers.ErrModelNotFound):
		httpx.WriteAPI(w, http.StatusNotFound, httpx.Response{Error: err.Error()})
	case err != nil:
		httpx.WriteAPI(w, http.StatusInternalServerError, httpx.Response{Error: err.Error()})
	default:
		httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true})
	}
}
