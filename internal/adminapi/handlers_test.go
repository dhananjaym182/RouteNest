package adminapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/domain"
	"routenest/internal/httpx"
)

func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder) httpx.Response {
	t.Helper()
	var out httpx.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return out
}

func TestKeyManagerGet(t *testing.T) {
	m := &KeyManager{ListKeys: func() ([]string, error) { return []string{"k1", "k2"}, nil }}
	rec := httptest.NewRecorder()
	m.Get(rec, httptest.NewRequest(http.MethodGet, "/admin/api/keys", nil))
	resp := decodeResponse(t, rec)
	keys, _ := resp.Data.(map[string]any)["keys"].([]any)
	if !resp.Success || len(keys) != 2 || keys[0] != "k1" {
		t.Fatalf("response = %+v", resp)
	}
}

func TestKeyManagerGenerateStoresKey(t *testing.T) {
	var audits []KeyAuditEvent
	m := &KeyManager{
		GenerateKey: func() (string, error) { return "cline_secure", nil },
		Audit: func(_ *http.Request, event KeyAuditEvent) error {
			audits = append(audits, event)
			return nil
		},
	}
	rec := httptest.NewRecorder()
	m.Generate(rec, httptest.NewRequest(http.MethodPost, "/admin/api/keys/generate", nil))
	resp := decodeResponse(t, rec)
	if !resp.Success {
		t.Fatalf("response = %+v", resp)
	}
	key, _ := resp.Data.(map[string]any)["key"].(string)
	if key != "cline_secure" || len(audits) != 1 || audits[0] != (KeyAuditEvent{Kind: KeyAuditCreate, Outcome: "success"}) {
		t.Fatalf("key = %q audits = %+v", key, audits)
	}
}

func TestKeyManagerGenerateFailureIsExplicitAndAudited(t *testing.T) {
	var audits []KeyAuditEvent
	m := &KeyManager{
		GenerateKey: func() (string, error) { return "", errors.New("injected persistence failure") },
		Audit: func(_ *http.Request, event KeyAuditEvent) error {
			audits = append(audits, event)
			return nil
		},
	}
	rec := httptest.NewRecorder()
	m.Generate(rec, httptest.NewRequest(http.MethodPost, "/admin/api/keys/generate", nil))
	if rec.Code != http.StatusInternalServerError || decodeResponse(t, rec).Error != "key_persistence_failed" {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !reflect.DeepEqual(audits, []KeyAuditEvent{{Kind: KeyAuditCreate, Outcome: "failed"}}) {
		t.Fatalf("audits = %+v", audits)
	}
}

func TestKeyManagerMethodsRejected(t *testing.T) {
	m := &KeyManager{Translate: func(*http.Request, string) string { return "nope" }}
	rec := httptest.NewRecorder()
	m.Generate(rec, httptest.NewRequest(http.MethodGet, "/admin/api/keys/generate", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", rec.Code)
	}
	if resp := decodeResponse(t, rec); resp.Error != "nope" || resp.Success {
		t.Fatalf("response = %+v", resp)
	}
}

func TestKeyManagerDelete(t *testing.T) {
	var deleted []string
	var audits []KeyAuditEvent
	m := &KeyManager{
		DeleteKey: func(k string, confirmed, elevated bool) (accounts.ClientKeyDeleteResult, error) {
			deleted = append(deleted, k)
			if confirmed || elevated {
				t.Fatalf("ordinary delete confirmation=%v elevated=%v", confirmed, elevated)
			}
			return accounts.ClientKeyDeleteResult{}, nil
		},
		Audit: func(_ *http.Request, event KeyAuditEvent) error {
			audits = append(audits, event)
			return nil
		},
	}
	rec := httptest.NewRecorder()
	m.Delete(rec, httptest.NewRequest(http.MethodPost, "/admin/api/keys/delete", strings.NewReader(`{"key":"k1"}`)))
	if resp := decodeResponse(t, rec); !resp.Success || resp.Message != "key_deleted" {
		t.Fatalf("response = %+v", resp)
	}
	if len(deleted) != 1 || deleted[0] != "k1" || len(audits) != 1 || audits[0] != (KeyAuditEvent{Kind: KeyAuditDelete, Outcome: "success"}) {
		t.Fatalf("deleted = %v audits = %+v", deleted, audits)
	}

	rec = httptest.NewRecorder()
	m.Delete(rec, httptest.NewRequest(http.MethodPost, "/admin/api/keys/delete", strings.NewReader(`{`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid body status = %d", rec.Code)
	}
}

func TestKeyManagerFinalDeleteRequiresConfirmationAndElevation(t *testing.T) {
	var audits []KeyAuditEvent
	elevated := false
	m := &KeyManager{
		DeleteKey: func(_ string, confirmed, allowed bool) (accounts.ClientKeyDeleteResult, error) {
			if !confirmed {
				return accounts.ClientKeyDeleteResult{}, accounts.ErrFinalClientKeyConfirmationRequired
			}
			if !allowed {
				return accounts.ClientKeyDeleteResult{}, accounts.ErrFinalClientKeyAuthorizationRequired
			}
			return accounts.ClientKeyDeleteResult{DeletedFinalKey: true}, nil
		},
		CanDeleteFinal: func(*http.Request) bool { return elevated },
		Audit: func(_ *http.Request, event KeyAuditEvent) error {
			audits = append(audits, event)
			return nil
		},
	}

	request := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		m.Delete(rec, httptest.NewRequest(http.MethodPost, "/admin/api/keys/delete", strings.NewReader(body)))
		return rec
	}
	if rec := request(`{"key":"last"}`); rec.Code != http.StatusConflict || decodeResponse(t, rec).Error != "final_key_confirmation_required" {
		t.Fatalf("unconfirmed status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := request(`{"key":"last","confirmation":"disable-client-authentication"}`); rec.Code != http.StatusForbidden || decodeResponse(t, rec).Error != "permission_denied" {
		t.Fatalf("unelevated status=%d body=%s", rec.Code, rec.Body.String())
	}
	elevated = true
	if rec := request(`{"key":"last","confirmation":"disable-client-authentication"}`); rec.Code != http.StatusOK {
		t.Fatalf("confirmed status=%d body=%s", rec.Code, rec.Body.String())
	}
	want := []KeyAuditEvent{
		{Kind: KeyAuditDeleteFinal, Outcome: "failed"},
		{Kind: KeyAuditDeleteFinal, Outcome: "failed"},
		{Kind: KeyAuditDeleteFinal, Outcome: "success"},
	}
	if !reflect.DeepEqual(audits, want) {
		t.Fatalf("audits = %+v, want %+v", audits, want)
	}
}

func TestMonitoringStats(t *testing.T) {
	m := &Monitoring{
		Accounts: func() []*domain.Account {
			return []*domain.Account{
				{Status: "active", UsageCount: 2, PromptTokens: 10, TotalTokens: 15},
				{Status: "cooldown", CachedTokens: 3},
				{Status: "expired"},
			}
		},
		Strategy:      func() string { return "round" },
		ModelChain:    func() []string { return []string{"a"} },
		Version:       func() string { return "v9" },
		OpencodeToday: func() any { return map[string]any{"requests": 4} },
	}
	rec := httptest.NewRecorder()
	m.Stats(rec, httptest.NewRequest(http.MethodGet, "/admin/api/stats", nil))
	resp := decodeResponse(t, rec)
	data, _ := resp.Data.(map[string]any)
	if !resp.Success || data["total"] != float64(3) || data["active"] != float64(1) ||
		data["cooldown"] != float64(1) || data["expired"] != float64(1) ||
		data["usageCount"] != float64(2) || data["promptTokens"] != float64(10) ||
		data["totalTokens"] != float64(15) || data["cachedTokens"] != float64(3) ||
		data["strategy"] != "round" || data["version"] != "v9" {
		t.Fatalf("data = %v", data)
	}
	if _, ok := data["opencodeToday"].(map[string]any); !ok {
		t.Fatalf("opencodeToday = %v", data["opencodeToday"])
	}
	if _, ok := data["startedAt"]; ok {
		t.Fatalf("startedAt should be absent without StartedAt: %v", data["startedAt"])
	}

	m.StartedAt = func() time.Time { return time.Now().Add(-90 * time.Second) }
	rec = httptest.NewRecorder()
	m.Stats(rec, httptest.NewRequest(http.MethodGet, "/admin/api/stats", nil))
	data, _ = decodeResponse(t, rec).Data.(map[string]any)
	if _, ok := data["startedAt"].(string); !ok {
		t.Fatalf("startedAt = %v", data["startedAt"])
	}
	if up, _ := data["uptimeSeconds"].(float64); up < 80 || up > 120 {
		t.Fatalf("uptimeSeconds = %v", data["uptimeSeconds"])
	}
}

func TestMonitoringLogsLimitValidation(t *testing.T) {
	called := false
	m := &Monitoring{ListRequestLogs: func(limit int, cursor string) (any, error) {
		called = true
		if limit != 10 || cursor != "c1" {
			t.Fatalf("limit=%d cursor=%q", limit, cursor)
		}
		return map[string]any{"items": []any{}}, nil
	}}
	rec := httptest.NewRecorder()
	m.Logs(rec, httptest.NewRequest(http.MethodGet, "/admin/api/request-logs?limit=10&cursor=c1", nil))
	if !called || rec.Code != http.StatusOK {
		t.Fatalf("called=%v status=%d", called, rec.Code)
	}

	rec = httptest.NewRecorder()
	m.Logs(rec, httptest.NewRequest(http.MethodGet, "/admin/api/request-logs?limit=zero", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad limit status = %d", rec.Code)
	}
}
