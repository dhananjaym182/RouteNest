package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The provider adapters: method rejection, invalid JSON, validation messages,
// not-found, and the probe envelope. The owner rules are tested in providers.
func TestProviderAdapterContracts(t *testing.T) {
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
		{"save wrong method", handleProviderSave, http.MethodGet, ``, http.StatusMethodNotAllowed, "方法不允许"},
		{"save invalid json", handleProviderSave, http.MethodPost, `{`, http.StatusBadRequest, "invalid_json"},
		{"save missing name", handleProviderSave, http.MethodPost, `{"baseURL":"https://x.example"}`, http.StatusBadRequest, "name and baseURL are required"},
		{"save bad scheme", handleProviderSave, http.MethodPost, `{"name":"n","baseURL":"ftp://x"}`, http.StatusBadRequest, "baseURL must start with http:// or https://"},
		{"save bad token window", handleProviderSave, http.MethodPost, `{"name":"n","baseURL":"https://x.example","tokenWindow":"weekly"}`, http.StatusBadRequest, `tokenWindow must be "monthly" or "daily"`},
		{"delete wrong method", handleProviderDelete, http.MethodGet, ``, http.StatusMethodNotAllowed, "方法不允许"},
		{"delete invalid json", handleProviderDelete, http.MethodPost, `{`, http.StatusBadRequest, "invalid_json"},
		{"delete unknown id", handleProviderDelete, http.MethodPost, `{"id":"prov_missing"}`, http.StatusNotFound, "provider not found"},
		{"test invalid json", handleProviderTest, http.MethodPost, `{`, http.StatusBadRequest, "invalid_json"},
		{"test unknown saved id", handleProviderTest, http.MethodPost, `{"id":"prov_missing"}`, http.StatusNotFound, "provider not found"},
		{"test unsaved bad scheme", handleProviderTest, http.MethodPost, `{"baseURL":"ftp://x","model":"m"}`, http.StatusBadRequest, "baseURL must start with http:// or https://"},
		{"cooldown wrong method", handleProviderCooldownReset, http.MethodGet, ``, http.StatusMethodNotAllowed, "方法不允许"},
		{"cooldown invalid json", handleProviderCooldownReset, http.MethodPost, `{`, http.StatusBadRequest, "invalid_json"},
		{"cooldown missing id", handleProviderCooldownReset, http.MethodPost, `{}`, http.StatusBadRequest, "id is required"},
		{"account health wrong method", handleAccountHealth, http.MethodPost, ``, http.StatusMethodNotAllowed, "方法不允许"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		tc.call(rec, httptest.NewRequest(tc.method, "/x", strings.NewReader(tc.body)))
		var env envelope
		json.Unmarshal(rec.Body.Bytes(), &env)
		if rec.Code != tc.status || env.Error != tc.message {
			t.Errorf("%s: got %d %q, want %d %q", tc.name, rec.Code, env.Error, tc.status, tc.message)
		}
	}
}

// Probing an unsaved provider with a model reports upstream failure inside a 200
// envelope, not as an HTTP error.
func TestProviderTestUnsavedUpstreamFailureEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	}))
	defer srv.Close()
	rec := httptest.NewRecorder()
	handleProviderTest(rec, httptest.NewRequest(http.MethodPost, "/x",
		strings.NewReader(`{"baseURL":"`+srv.URL+`","model":"m1"}`)))
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
	if !env.Success || env.Data["ok"] != false || env.Data["model"] != "m1" {
		t.Fatalf("envelope = %s", rec.Body.String())
	}
}
