package admin_test

import (
	"net/http"
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
