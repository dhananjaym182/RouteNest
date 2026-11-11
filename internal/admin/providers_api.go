package admin

// HTTP adapters for the provider admin endpoints. Rules and persistence live in
// internal/providers; these functions parse requests and build envelopes.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"routenest/internal/httpx"
	"routenest/internal/i18n"
	"routenest/internal/providerreg"
	"routenest/internal/providers"
	"routenest/internal/requestlog"
)

// GET /admin/api/providers
func handleProvidersList(w http.ResponseWriter, r *http.Request) {
	ov := providers.ProviderOverview(requestlog.Snapshot())
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{
		"providers":     ov.Providers,
		"health":        ov.Health,
		"accountHealth": ov.AccountHealth,
	}})
}

// POST /admin/api/providers/save
func handleProviderSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		httpx.WriteAPI(w, http.StatusMethodNotAllowed, httpx.Response{Error: i18n.TAPI(r, "method_not_allowed")})
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: err.Error()})
		return
	}
	defer r.Body.Close()
	var p providers.CustomProvider
	if err := json.Unmarshal(body, &p); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "invalid_json"})
		return
	}
	saved, err := providers.SaveCustomProvider(p)
	if err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: err.Error()})
		return
	}
	clearOnboardingProviderVerification(saved.ID)
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"provider": saved}})
}

// POST /admin/api/providers/delete  body: {id}
func handleProviderDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		httpx.WriteAPI(w, http.StatusMethodNotAllowed, httpx.Response{Error: i18n.TAPI(r, "method_not_allowed")})
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "invalid_json"})
		return
	}
	if err := providers.DeleteCustomProvider(req.ID); err != nil {
		if errors.Is(err, providers.ErrProviderNotFound) {
			httpx.WriteAPI(w, http.StatusNotFound, httpx.Response{Error: "provider not found"})
			return
		}
		httpx.WriteAPI(w, http.StatusInternalServerError, httpx.Response{Error: err.Error()})
		return
	}
	clearOnboardingProviderVerification(req.ID)
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true})
}

// POST /admin/api/providers/test
//
// Two request shapes: {id, model?} tests a saved provider, and {baseURL, apiKey?,
// headers?, model?} tests an unsaved configuration. Upstream failures are
// reported inside a 200 envelope as {ok:false, error}.
func handleProviderTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		httpx.WriteAPI(w, http.StatusMethodNotAllowed, httpx.Response{Error: i18n.TAPI(r, "method_not_allowed")})
		return
	}
	var req struct {
		ID      string            `json:"id"`
		BaseURL string            `json:"baseURL"`
		APIKey  string            `json:"apiKey"`
		Headers map[string]string `json:"headers"`
		Model   string            `json:"model"`
		// UpstreamModel is the editor's draft upstream ID for Model. It lets a
		// model be tested before it is added or before the mapping is saved.
		UpstreamModel string `json:"upstreamModel"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "invalid_json"})
		return
	}
	model := strings.TrimSpace(req.Model)
	if strings.TrimSpace(req.BaseURL) != "" {
		result, err := providers.ProbeUnsavedProvider(req.BaseURL, req.APIKey, req.Headers, model, req.UpstreamModel)
		if err != nil {
			httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: err.Error()})
			return
		}
		httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: result})
		return
	}
	result, err := providers.ProbeSavedProvider(req.ID, model, req.UpstreamModel)
	if err != nil {
		httpx.WriteAPI(w, http.StatusNotFound, httpx.Response{Error: "provider not found"})
		return
	}
	if ok, _ := result["ok"].(bool); ok {
		markOnboardingProviderVerified(req.ID)
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: result})
}

// GET /admin/api/providers/presets
func handleProviderPresets(w http.ResponseWriter, r *http.Request) {
	type presetOut struct {
		Key           string                     `json:"key"`
		Name          string                     `json:"name"`
		BaseURL       string                     `json:"baseURL"`
		ModelIDs      []string                   `json:"modelIDs,omitempty"`
		ModelsFetcher *providerreg.ModelsFetcher `json:"modelsFetcher,omitempty"`
		Headers       map[string]string          `json:"headers,omitempty"`
		Notes         string                     `json:"notes,omitempty"`
		FreeTier      bool                       `json:"freeTier"`
		Kind          string                     `json:"kind"`
		LoginFlow     bool                       `json:"loginFlow,omitempty"`
		AuthFlow      string                     `json:"authFlow,omitempty"`
	}
	entries := providers.PresetEntries()
	out := make([]presetOut, 0, len(entries))
	for _, p := range entries {
		out = append(out, presetOut{Key: p.Key, Name: p.Name, BaseURL: p.BaseURL, Headers: p.Headers, Notes: p.Notes, FreeTier: p.FreeTier, ModelIDs: p.ModelIDs, ModelsFetcher: p.ModelsFetcher, Kind: p.Kind, LoginFlow: p.LoginFlow, AuthFlow: p.AuthFlow})
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"presets": out}})
}

// GET /admin/api/account-health
func handleAccountHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteAPI(w, http.StatusMethodNotAllowed, httpx.Response{Error: i18n.TAPI(r, "method_not_allowed")})
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"accounts": providers.AccountOutcomes(requestlog.Snapshot())}})
}

// POST /admin/api/providers/cooldown-reset  body: {id, model?}
func handleProviderCooldownReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteAPI(w, http.StatusMethodNotAllowed, httpx.Response{Error: i18n.TAPI(r, "method_not_allowed")})
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: err.Error()})
		return
	}
	defer r.Body.Close()
	var req struct {
		ID    string `json:"id"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "invalid_json"})
		return
	}
	id := strings.TrimSpace(req.ID)
	model := strings.TrimSpace(req.Model)
	if id == "" {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "id is required"})
		return
	}
	removed := providers.ResetCooldowns(id, model)
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"removed": removed}})
}
