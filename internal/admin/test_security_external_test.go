package admin_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"routenest/internal/admin"
	"routenest/internal/adminapi"
	"routenest/internal/adminauth"
	"routenest/internal/platform"
	storeadminauth "routenest/internal/store/adminauth"
)

type adminRouteTestHarness struct {
	mux     *http.ServeMux
	session *http.Cookie
	csrf    string
}

func newAdminRouteTestHarness(t *testing.T) *adminRouteTestHarness {
	t.Helper()
	dir := t.TempDir()
	if err := platform.EnsurePrivateDir(dir); err != nil {
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
	credentials, err := service.CompleteBootstrap(t.Context(), "one time bootstrap secret", "permanent admin password", "127.0.0.1")
	if err != nil {
		_ = repo.Close()
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	routes, err := admin.RegisterRoutesWithService(mux, service, admin.RouteSecurityOptions{CanonicalURL: "http://127.0.0.1:3457"})
	if err != nil {
		_ = repo.Close()
		t.Fatal(err)
	}
	if routes == nil {
		_ = repo.Close()
		t.Fatal("route registration did not return lifecycle ownership")
	}
	t.Cleanup(func() {
		_ = routes.Close()
		_ = repo.Close()
	})
	return &adminRouteTestHarness{
		mux:     mux,
		session: &http.Cookie{Name: adminapi.SessionCookieName, Value: credentials.Token, Path: "/admin"},
		csrf:    credentials.CSRFToken,
	}
}

func (h *adminRouteTestHarness) request(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, "http://127.0.0.1:3457"+path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:41000"
	req.AddCookie(h.session)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Origin", "http://127.0.0.1:3457")
		req.Header.Set("X-RouteNest-CSRF", h.csrf)
	}
	return req
}
