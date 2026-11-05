package admin_test

import (
	"net/http"
	"strings"
	"testing"

	"routenest/internal/admin"
)

// Characterization (current admin behaviour): an empty header value in a probe
// request deletes that header, including a default one such as Authorization.
// The unified discovery path keeps this semantics; see Phase 3 decisions.
func TestProviderModelsEmptyHeaderDeletesDefault(t *testing.T) {
	installConfig(t, admin.DefaultProxyConfig())
	var gotAuth string
	var sawHeader bool
	installTransport(t, func(req *http.Request) *http.Response {
		gotAuth = req.Header.Get("Authorization")
		_, sawHeader = req.Header["Authorization"]
		return stubResp(req, http.StatusOK, `{"data":[{"id":"x"}]}`)
	})
	code, resp := postAdminJSON(t, admin.HandleProviderModels,
		`{"baseURL":"http://m.test/v1","apiKey":"sk-k","headers":{"Authorization":""}}`)
	if code != 200 || !resp.Success {
		t.Fatalf("status=%d success=%v err=%s", code, resp.Success, resp.Error)
	}
	if sawHeader || gotAuth != "" {
		t.Fatalf("Authorization = %q (present=%v), want deleted", gotAuth, sawHeader)
	}
}

// The probe's error envelope is preserved (200 with error and an empty models
// list), and the typed key is masked when the upstream echoes it.
func TestProviderModelsErrorEnvelopeIsRedacted(t *testing.T) {
	installConfig(t, admin.DefaultProxyConfig())
	const key = "sk-typed-probe-key"
	installTransport(t, func(req *http.Request) *http.Response {
		return stubResp(req, http.StatusUnauthorized, "denied for "+key)
	})
	code, resp := postAdminJSON(t, admin.HandleProviderModels,
		`{"baseURL":"http://m.test/v1","apiKey":"`+key+`"}`)
	if code != 200 || !resp.Success {
		t.Fatalf("status=%d success=%v", code, resp.Success)
	}
	d := dataMap(t, resp)
	msg, _ := d["error"].(string)
	if strings.Contains(msg, key) || !strings.Contains(msg, "HTTP 401") {
		t.Fatalf("error = %q, want HTTP 401 with the key masked", msg)
	}
	if models, _ := d["models"].([]any); len(models) != 0 {
		t.Fatalf("models = %v, want empty", models)
	}
}
