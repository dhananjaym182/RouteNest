package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"routenest/internal/adminapi"
	"routenest/internal/adminauth"
	storeadminauth "routenest/internal/store/adminauth"
)

func newAdminAuthTestMux(t *testing.T) (*http.ServeMux, *adminSecurityRuntime) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	repo, err := storeadminauth.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	service, err := adminauth.NewService(repo, adminauth.Options{
		PasswordParams:    adminauth.PasswordParams{Memory: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32},
		BootstrapPassword: "one time bootstrap secret",
	})
	if err != nil {
		_ = repo.Close()
		t.Fatal(err)
	}
	canonical, err := url.Parse("http://127.0.0.1:3457")
	if err != nil {
		t.Fatal(err)
	}
	runtime := &adminSecurityRuntime{
		service: service,
		store:   repo,
		requestOptions: adminauth.RequestSecurityOptions{
			CanonicalURL: canonical,
		},
	}
	mux := http.NewServeMux()
	registerRoutesWithSecurity(mux, runtime)
	t.Cleanup(func() { _ = repo.Close() })
	return mux, runtime
}

func adminTestRequest(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, "http://127.0.0.1:3457"+path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:41000"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Origin", "http://127.0.0.1:3457")
	}
	return req
}

func cookieNamed(t *testing.T, recorder *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("cookie %s missing: %+v", name, recorder.Result().Cookies())
	return nil
}

func TestAdminBootstrapLoginSessionAndLogout(t *testing.T) {
	mux, _ := newAdminAuthTestMux(t)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, adminTestRequest(http.MethodGet, "/admin/api/auth/status", ""))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"bootstrapRequired":true`) {
		t.Fatalf("status before bootstrap = %d %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, adminTestRequest(http.MethodGet, "/admin/api/models", ""))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous models status = %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, adminTestRequest(http.MethodPost, "/admin/api/auth/bootstrap", `{"secret":"one time bootstrap secret","password":"permanent admin password"}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	sessionCookie := cookieNamed(t, recorder, adminapi.SessionCookieName)
	csrfCookie := cookieNamed(t, recorder, adminCSRFCookie)
	if !sessionCookie.HttpOnly || csrfCookie.HttpOnly {
		t.Fatalf("credential cookie flags session=%+v csrf=%+v", sessionCookie, csrfCookie)
	}

	request := adminTestRequest(http.MethodGet, "/admin/api/auth/session", "")
	request.AddCookie(sessionCookie)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"username":"admin"`) {
		t.Fatalf("session status = %d body=%s", recorder.Code, recorder.Body.String())
	}

	request = adminTestRequest(http.MethodPost, "/admin/api/logout", `{}`)
	request.AddCookie(sessionCookie)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("logout without CSRF status = %d", recorder.Code)
	}

	request = adminTestRequest(http.MethodPost, "/admin/api/logout", `{}`)
	request.AddCookie(sessionCookie)
	request.Header.Set("X-RouteNest-CSRF", csrfCookie.Value)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("logout status = %d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, adminTestRequest(http.MethodPost, "/admin/api/login", `{"username":"admin","password":"wrong password value"}`))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, adminTestRequest(http.MethodPost, "/admin/api/login", `{"username":"admin","password":"permanent admin password"}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("login status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAdminSecurityHeadersAndOriginEnforcement(t *testing.T) {
	mux, _ := newAdminAuthTestMux(t)
	recorder := httptest.NewRecorder()
	request := adminTestRequest(http.MethodPost, "/admin/api/login", `{"username":"admin","password":"x"}`)
	request.Header.Set("Origin", "https://evil.example")
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin login status = %d", recorder.Code)
	}
	for _, header := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Permissions-Policy"} {
		if recorder.Header().Get(header) == "" {
			t.Fatalf("security header %s missing", header)
		}
	}
}
