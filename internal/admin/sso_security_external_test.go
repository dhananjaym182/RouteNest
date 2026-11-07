package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"routenest/internal/adminapi"
)

const testOIDCProviderJSON = `{
  "id":"company",
  "type":"oidc",
  "enabled":false,
  "oidc":{
    "issuer":"https://issuer.invalid",
    "clientID":"routenest-admin",
    "clientSecret":{"kind":"env","name":"ROUTENEST_TEST_MISSING_SSO_SECRET"},
    "redirectURL":"http://127.0.0.1:3457/admin/api/auth/oidc/callback",
    "defaultRoles":["viewer"]
  }
}`

func TestAdminSSOManagementRBACCSRFAndReadinessIsolation(t *testing.T) {
	harness := newAdminRouteTestHarness(t)

	request := httptest.NewRequest(http.MethodPut, "http://127.0.0.1:3457/admin/api/auth/sso/providers", strings.NewReader(testOIDCProviderJSON))
	request.RemoteAddr = "127.0.0.1:41000"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://127.0.0.1:3457")
	recorder := httptest.NewRecorder()
	harness.mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous provider mutation = %d %s", recorder.Code, recorder.Body.String())
	}

	request = harness.request(http.MethodPut, "/admin/api/auth/sso/providers", testOIDCProviderJSON)
	request.Header.Del("X-RouteNest-CSRF")
	recorder = httptest.NewRecorder()
	harness.mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("provider mutation without CSRF = %d %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	harness.mux.ServeHTTP(recorder, harness.request(http.MethodPut, "/admin/api/auth/sso/providers", testOIDCProviderJSON))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"reason":"disabled"`) {
		t.Fatalf("save disabled provider = %d %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	harness.mux.ServeHTTP(recorder, harness.request(http.MethodGet, "/admin/api/auth/sso/providers", ""))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "ROUTENEST_TEST_MISSING_SSO_SECRET") || strings.Contains(recorder.Body.String(), "resolved-secret") {
		t.Fatalf("provider list = %d %s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3457/admin/api/auth/status", nil)
	request.RemoteAddr = "127.0.0.1:41000"
	recorder = httptest.NewRecorder()
	harness.mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), `"id":"company"`) {
		t.Fatalf("disabled provider exposed before login = %d %s", recorder.Code, recorder.Body.String())
	}

	enabled := strings.Replace(testOIDCProviderJSON, `"enabled":false`, `"enabled":true`, 1)
	recorder = httptest.NewRecorder()
	harness.mux.ServeHTTP(recorder, harness.request(http.MethodPut, "/admin/api/auth/sso/providers", enabled))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"reason":"provider_unavailable"`) {
		t.Fatalf("save broken enabled provider = %d %s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3457/admin/api/auth/status", nil)
	request.RemoteAddr = "127.0.0.1:41000"
	recorder = httptest.NewRecorder()
	harness.mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"id":"company"`) || !strings.Contains(recorder.Body.String(), `"ready":false`) || !strings.Contains(recorder.Body.String(), `"adminReady":true`) {
		t.Fatalf("broken provider readiness affected local admin = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestAdminSSOStartIsPublicButStrictlySameOrigin(t *testing.T) {
	harness := newAdminRouteTestHarness(t)
	body := `{"provider":"missing","returnLocation":"/admin/"}`

	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:3457/admin/api/auth/oidc/start", strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:41000"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://127.0.0.1:3457")
	recorder := httptest.NewRecorder()
	harness.mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("same-origin anonymous SSO start = %d %s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:3457/admin/api/auth/oidc/start", strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:41000"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://evil.example")
	recorder = httptest.NewRecorder()
	harness.mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin SSO start = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestAdminUserLifecycleAndViewerRBAC(t *testing.T) {
	harness := newAdminRouteTestHarness(t)
	recorder := httptest.NewRecorder()
	harness.mux.ServeHTTP(recorder, harness.request(http.MethodPost, "/admin/api/auth/users", `{"username":"viewer","password":"permanent viewer password","roles":["viewer"]}`))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create viewer = %d %s", recorder.Code, recorder.Body.String())
	}
	user := decodeAdminUser(t, recorder.Body.Bytes())

	login := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:3457/admin/api/login", strings.NewReader(`{"username":"viewer","password":"permanent viewer password"}`))
	login.RemoteAddr = "127.0.0.1:41001"
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("Origin", "http://127.0.0.1:3457")
	recorder = httptest.NewRecorder()
	harness.mux.ServeHTTP(recorder, login)
	if recorder.Code != http.StatusOK {
		t.Fatalf("viewer login = %d %s", recorder.Code, recorder.Body.String())
	}
	var viewerSession *http.Cookie
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == adminapi.SessionCookieName {
			viewerSession = cookie
		}
	}
	if viewerSession == nil {
		t.Fatal("viewer session cookie missing")
	}
	for path, want := range map[string]int{
		"/admin/api/models":     http.StatusOK,
		"/admin/api/accounts":   http.StatusForbidden,
		"/admin/api/auth/users": http.StatusForbidden,
	} {
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3457"+path, nil)
		request.RemoteAddr = "127.0.0.1:41001"
		request.AddCookie(viewerSession)
		recorder = httptest.NewRecorder()
		harness.mux.ServeHTTP(recorder, request)
		if recorder.Code != want {
			t.Fatalf("viewer GET %s = %d, want %d: %s", path, recorder.Code, want, recorder.Body.String())
		}
	}

	updateBody := `{"userID":"` + user.ID + `","revision":` + strconv.FormatUint(user.Revision, 10) + `,"roles":["viewer","provider_admin"],"disabled":false}`
	recorder = httptest.NewRecorder()
	harness.mux.ServeHTTP(recorder, harness.request(http.MethodPost, "/admin/api/auth/users/update", updateBody))
	if recorder.Code != http.StatusOK {
		t.Fatalf("update viewer roles = %d %s", recorder.Code, recorder.Body.String())
	}
	updated := decodeAdminUser(t, recorder.Body.Bytes())

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3457/admin/api/models", nil)
	request.RemoteAddr = "127.0.0.1:41001"
	request.AddCookie(viewerSession)
	recorder = httptest.NewRecorder()
	harness.mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("old viewer session survived role change = %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	harness.mux.ServeHTTP(recorder, harness.request(http.MethodPost, "/admin/api/auth/users/update", updateBody))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("stale user update = %d %s", recorder.Code, recorder.Body.String())
	}

	resetBody := `{"userID":"` + updated.ID + `","revision":` + strconv.FormatUint(updated.Revision, 10) + `,"password":"rotated viewer password"}`
	recorder = httptest.NewRecorder()
	harness.mux.ServeHTTP(recorder, harness.request(http.MethodPost, "/admin/api/auth/users/password", resetBody))
	if recorder.Code != http.StatusOK {
		t.Fatalf("reset viewer password = %d %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	harness.mux.ServeHTTP(recorder, harness.request(http.MethodGet, "/admin/api/auth/users", ""))
	latest := findAdminUser(t, recorder.Body.Bytes(), updated.ID)
	deleteBody := `{"userID":"` + latest.ID + `","revision":` + strconv.FormatUint(latest.Revision, 10) + `}`
	recorder = httptest.NewRecorder()
	harness.mux.ServeHTTP(recorder, harness.request(http.MethodPost, "/admin/api/auth/users/delete", deleteBody))
	if recorder.Code != http.StatusOK {
		t.Fatalf("delete viewer = %d %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	harness.mux.ServeHTTP(recorder, harness.request(http.MethodPost, "/admin/api/auth/users/update", `{"userID":"break-glass","revision":1,"roles":["viewer"],"disabled":true}`))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("break-glass update = %d %s", recorder.Code, recorder.Body.String())
	}
}

type adminUserResponse struct {
	ID       string   `json:"id"`
	Roles    []string `json:"roles"`
	Revision uint64   `json:"revision"`
}

func decodeAdminUser(t *testing.T, body []byte) adminUserResponse {
	t.Helper()
	var envelope struct {
		Data struct {
			User adminUserResponse `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Data.User.ID == "" {
		t.Fatalf("decode administrator user: %v, body=%s", err, body)
	}
	return envelope.Data.User
}

func findAdminUser(t *testing.T, body []byte, id string) adminUserResponse {
	t.Helper()
	var envelope struct {
		Data struct {
			Users []adminUserResponse `json:"users"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	for _, user := range envelope.Data.Users {
		if user.ID == id {
			return user
		}
	}
	t.Fatalf("administrator user %s missing: %s", id, body)
	return adminUserResponse{}
}
