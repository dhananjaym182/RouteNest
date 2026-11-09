package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"routenest/internal/domain"
)

func strKeys(keys ...string) func() ([]string, error) {
	return func() ([]string, error) { return keys, nil }
}

func unauthorized(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusUnauthorized)
}

func TestAPIKeyAuthOpenWhenNoKeys(t *testing.T) {
	auth := &APIKeyAuth{Snapshot: strKeys(), Unauthorized: unauthorized}
	called := false
	h := auth.Wrap(func(w http.ResponseWriter, r *http.Request) { called = true })
	h(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if !called {
		t.Fatal("expected next handler")
	}
}

func TestAPIKeyAuthFailsClosedWhenStorageUnavailable(t *testing.T) {
	auth := &APIKeyAuth{
		Snapshot:     func() ([]string, error) { return nil, errors.New("corrupt pool") },
		Unauthorized: unauthorized,
	}
	called := false
	rec := httptest.NewRecorder()
	auth.Wrap(func(w http.ResponseWriter, r *http.Request) { called = true })(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if called {
		t.Fatal("unavailable key storage must not call next")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"type":"auth_storage_error"`) || strings.Contains(rec.Body.String(), "corrupt pool") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestAPIKeyAuthFailsClosedWithoutSnapshotProvider(t *testing.T) {
	auth := &APIKeyAuth{Unauthorized: unauthorized}
	called := false
	rec := httptest.NewRecorder()
	auth.Wrap(func(w http.ResponseWriter, r *http.Request) { called = true })(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if called || rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("called=%v status=%d, want fail-closed 503", called, rec.Code)
	}
}

func TestAPIKeyAuthAcceptsBothHeaders(t *testing.T) {
	auth := &APIKeyAuth{Snapshot: strKeys("sk-1"), Unauthorized: unauthorized}
	for _, tc := range []struct {
		name  string
		setup func(*http.Request)
	}{
		{"x-api-key", func(r *http.Request) { r.Header.Set("x-api-key", "sk-1") }},
		{"bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer sk-1") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			tc.setup(req)
			auth.Wrap(func(w http.ResponseWriter, r *http.Request) { called = true })(httptest.NewRecorder(), req)
			if !called {
				t.Fatal("expected next handler")
			}
		})
	}
}

func TestAPIKeyAuthRejectsBadKey(t *testing.T) {
	auth := &APIKeyAuth{Snapshot: strKeys("sk-1"), Unauthorized: unauthorized}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("x-api-key", "nope")
	auth.Wrap(func(w http.ResponseWriter, r *http.Request) { t.Fatal("next must not run") })(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestAPIKeyAuthSetsCORS(t *testing.T) {
	auth := &APIKeyAuth{Snapshot: strKeys(), Unauthorized: unauthorized}
	rec := httptest.NewRecorder()
	auth.Wrap(func(w http.ResponseWriter, r *http.Request) {})(rec, httptest.NewRequest(http.MethodOptions, "/v1/models", nil))
	if rec.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Fatal("missing CORS header")
	}
}

func TestExtractKey(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if ExtractKey(r) != "" {
		t.Fatal("expected empty")
	}
	r.Header.Set("Authorization", "Bearer abc")
	if ExtractKey(r) != "abc" {
		t.Fatalf("bearer = %q", ExtractKey(r))
	}
	r.Header.Set("x-api-key", "xyz")
	if ExtractKey(r) != "xyz" {
		t.Fatalf("x-api-key should win: %q", ExtractKey(r))
	}
}

func TestHealthHandler(t *testing.T) {
	h := Health{Version: func() string { return "1.2.3" }, ActiveAccounts: func() int { return 7 }}
	rec := httptest.NewRecorder()
	h.Handler(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`"status":"ok"`, `"version":"1.2.3"`, `"activeAccounts":7`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body %s missing %s", body, want)
		}
	}
}

func TestHealthRuntimeStateUnavailableWithoutProvider(t *testing.T) {
	h := Health{Version: func() string { return "1.2.3" }, ActiveAccounts: func() int { return 0 }}
	rec := httptest.NewRecorder()
	h.Handler(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	var payload struct {
		Status         string                    `json:"status"`
		Version        string                    `json:"version"`
		ActiveAccounts int                       `json:"activeAccounts"`
		RuntimeState   domain.RuntimeStateHealth `json:"runtimeState"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || payload.Status != "ok" || payload.Version != "1.2.3" || payload.ActiveAccounts != 0 {
		t.Fatalf("legacy health payload changed: %s", rec.Body.String())
	}
	want := domain.RuntimeStateHealth{
		Backend: "unknown", Degraded: true, Stale: true,
		Enforcement: "unavailable", Reason: "runtime_state_unavailable",
	}
	if !reflect.DeepEqual(payload.RuntimeState, want) {
		t.Fatalf("runtimeState = %+v, want %+v", payload.RuntimeState, want)
	}
}

func TestHealthRuntimeStateRedactedDTO(t *testing.T) {
	now := time.Date(2026, 11, 7, 7, 0, 0, 0, time.UTC)
	want := domain.RuntimeStateHealth{
		Backend: "local_file", Ready: true, SingleNodeReady: true,
		MultiReplicaSupported: false, Enforcement: "single_node",
		LastSuccessfulRead: now, LastSuccessfulWrite: now,
	}
	called := false
	h := Health{
		Version: func() string { return "1.2.3" }, ActiveAccounts: func() int { return 0 },
		RuntimeState: func() domain.RuntimeStateHealth { called = true; return want },
	}
	rec := httptest.NewRecorder()
	h.Handler(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if !called || rec.Code != http.StatusOK {
		t.Fatalf("runtime provider not called: %d", rec.Code)
	}
	var payload struct {
		RuntimeState domain.RuntimeStateHealth `json:"runtimeState"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(payload.RuntimeState, want) {
		t.Fatalf("runtimeState = %+v, want %+v", payload.RuntimeState, want)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &object); err != nil {
		t.Fatal(err)
	}
	var runtimeState map[string]json.RawMessage
	if err := json.Unmarshal(object["runtimeState"], &runtimeState); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"backend": true, "ready": true, "singleNodeReady": true,
		"multiReplicaSupported": true, "degraded": true, "stale": true,
		"enforcement": true, "lastSuccessfulRead": true, "lastSuccessfulWrite": true, "reason": true,
	}
	for key := range runtimeState {
		if !allowed[key] {
			t.Fatalf("unexpected public runtime field %q", key)
		}
	}
	if string(runtimeState["multiReplicaSupported"]) != "false" {
		t.Fatal("local backend claimed multi-replica support")
	}
}

func TestReadinessHandler(t *testing.T) {
	t.Run("ready", func(t *testing.T) {
		rec := httptest.NewRecorder()
		Readiness{AuthenticationStorage: func() error { return nil }}.Handler(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
		if rec.Code != http.StatusOK || rec.Body.String() != "{\"authenticationStorage\":\"ready\",\"status\":\"ready\"}\n" {
			t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("storage unavailable is redacted", func(t *testing.T) {
		rec := httptest.NewRecorder()
		Readiness{AuthenticationStorage: func() error { return errors.New("secret path and detail") }}.Handler(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", rec.Code)
		}
		if body := rec.Body.String(); !strings.Contains(body, `"authenticationStorage":"unavailable"`) || strings.Contains(body, "secret path") {
			t.Fatalf("body = %s", body)
		}
	})

	t.Run("missing provider fails closed", func(t *testing.T) {
		rec := httptest.NewRecorder()
		Readiness{}.Handler(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", rec.Code)
		}
	})
}
