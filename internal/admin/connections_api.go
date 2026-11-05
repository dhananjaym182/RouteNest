package admin

// HTTP adapters for provider login connections. They parse requests and build
// response envelopes; behaviour lives in internal/connections. Status codes and
// error strings are the admin contract and must not change.

import (
	"encoding/json"
	"net/http"
	"strings"

	"routenest/internal/connections"
	"routenest/internal/httpx"
	"routenest/internal/providerreg"
)

// GET /admin/api/connections
func handleConnectionsList(w http.ResponseWriter, r *http.Request) {
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{
		"connections": connections.ListConnections(),
		"flows":       connections.ConnectionFlowNames(),
		// flowLabels is the canonical display name for each auth flow, taken
		// from the provider catalogue. The UI must not keep a parallel table.
		"flowLabels": providerreg.Builtin().AuthFlowLabels(),
	}})
}

// POST /admin/api/connections/login/start  body: {provider}
func handleConnectionLoginStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "invalid_json"})
		return
	}
	start, err := connections.StartProviderLogin(strings.TrimSpace(req.Provider))
	if err != nil {
		httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"error": err.Error()}})
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{
		"sessionId": start.SessionID, "userCode": start.UserCode, "authUrl": start.AuthURL,
	}})
}

// GET /admin/api/connections/login/status?sessionId=x
func handleConnectionLoginStatus(w http.ResponseWriter, r *http.Request) {
	status, ok := connections.ProviderLoginStatus(r.URL.Query().Get("sessionId"))
	if !ok {
		httpx.WriteAPI(w, http.StatusNotFound, httpx.Response{Error: "session not found"})
		return
	}
	out := map[string]any{
		"done": status.Done, "success": status.Success, "error": status.Error,
		"userCode": status.UserCode, "authUrl": status.AuthURL,
	}
	if status.Connection != nil {
		out["connection"] = *status.Connection
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: out})
}

// POST /admin/api/connections/import  body: {provider, accessToken, email?, orgId?}
func handleConnectionImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider    string `json:"provider"`
		AccessToken string `json:"accessToken"`
		Email       string `json:"email"`
		OrgID       string `json:"orgId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "provider and accessToken required"})
		return
	}
	conn, err := connections.ImportConnectionToken(req.Provider, req.AccessToken, req.Email, req.OrgID)
	if err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: err.Error()})
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{
		"connection": connections.RedactedConnection(*conn),
	}})
}

// connectionImportBatchItem is one batch import input. The token appears only in
// the request and never in the response.
type connectionImportBatchItem struct {
	Provider    string `json:"provider"`
	AccessToken string `json:"accessToken"`
	Email       string `json:"email"`
	OrgID       string `json:"orgId"`
}

// connectionImportBatchResult is one batch result, kept in input order. A failed
// item does not stop the batch.
type connectionImportBatchResult struct {
	Index    int    `json:"index"`
	OK       bool   `json:"ok"`
	ID       string `json:"id,omitempty"`
	Provider string `json:"provider"`
	Email    string `json:"email,omitempty"`
	Error    string `json:"error,omitempty"`
}

// maxConnectionImportBatch bounds one batch import request.
const maxConnectionImportBatch = 500

// POST /admin/api/connections/import-batch  body: {items:[{provider, accessToken, email?, orgId?}]}
func handleConnectionImportBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Items []connectionImportBatchItem `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Items) == 0 {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "items required"})
		return
	}
	if len(req.Items) > maxConnectionImportBatch {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "too many items (max 500)"})
		return
	}
	results := make([]connectionImportBatchResult, 0, len(req.Items))
	imported, failed := 0, 0
	for i, item := range req.Items {
		res := connectionImportBatchResult{Index: i, Provider: strings.TrimSpace(item.Provider)}
		conn, err := connections.ImportConnectionToken(item.Provider, item.AccessToken, item.Email, item.OrgID)
		if err != nil {
			res.Error = err.Error()
			failed++
		} else {
			res.OK, res.ID, res.Provider, res.Email = true, conn.ID, conn.Provider, conn.Email
			imported++
		}
		results = append(results, res)
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{
		"total": len(req.Items), "imported": imported, "failed": failed, "results": results,
	}})
}

// POST /admin/api/connections/delete  body: {id}
func handleConnectionDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "invalid_json"})
		return
	}
	if !connections.RemoveConnection(req.ID) {
		httpx.WriteAPI(w, http.StatusNotFound, httpx.Response{Error: "connection not found"})
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true})
}

// POST /admin/api/connections/test  body: {id}
// Probes the provider's model-list endpoint with the connection's credential.
func handleConnectionTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "invalid_json"})
		return
	}
	result, found := connections.TestConnection(req.ID)
	if !found {
		httpx.WriteAPI(w, http.StatusNotFound, httpx.Response{Error: "connection not found"})
		return
	}
	if result.Error != "" {
		httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"ok": false, "error": result.Error}})
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{
		"ok": result.OK, "status": result.Status, "body": result.Body,
	}})
}
