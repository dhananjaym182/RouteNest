package admin

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"routenest/internal/adminauth"
)

func noopAdminHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
}

func TestAdminRouteRegistryRequiresPermissionAndCSRFPolicy(t *testing.T) {
	registry := newAdminRouteRegistry()
	tests := []adminRouteDefinition{
		{Method: http.MethodGet, Path: "/admin/api/config", CSRF: csrfSafe, Handler: noopAdminHandler()},
		{Method: http.MethodPost, Path: "/admin/api/config", Permissions: []adminauth.Permission{adminauth.PermissionProviderWrite}, Handler: noopAdminHandler()},
		{Method: http.MethodPost, Path: "/admin/api/login", Public: true, CSRF: csrfSession, Handler: noopAdminHandler()},
	}
	for _, def := range tests {
		if err := registry.Handle(def); err == nil {
			t.Fatalf("Handle(%+v) succeeded", def)
		}
	}
}

func TestAdminRouteRegistryRejectsDuplicates(t *testing.T) {
	registry := newAdminRouteRegistry()
	def := adminRouteDefinition{
		Method:      http.MethodGet,
		Path:        "/admin/api/config",
		Permissions: []adminauth.Permission{adminauth.PermissionView},
		CSRF:        csrfSafe,
		Handler:     noopAdminHandler(),
	}
	if err := registry.Handle(def); err != nil {
		t.Fatal(err)
	}
	if err := registry.Handle(def); err == nil {
		t.Fatal("duplicate route succeeded")
	}
}

func TestAdminRouteRegistryUsesMethodSpecificServeMuxPatterns(t *testing.T) {
	registry := newAdminRouteRegistry()
	registry.MustHandle(adminRouteDefinition{
		Method:      http.MethodGet,
		Path:        "/admin/api/config",
		Permissions: []adminauth.Permission{adminauth.PermissionView},
		CSRF:        csrfSafe,
		Handler:     noopAdminHandler(),
	})

	rec := httptest.NewRecorder()
	registry.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/api/config", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("GET status = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	registry.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/api/config", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != http.MethodGet+", "+http.MethodHead {
		t.Fatalf("Allow = %q", got)
	}
	if _, ok := registry.Policy(http.MethodHead, "/admin/api/config"); !ok {
		t.Fatal("HEAD must inherit the GET security policy")
	}
}

func TestAdminRouteRegistryPolicyIsDefensiveCopy(t *testing.T) {
	registry := newAdminRouteRegistry()
	registry.MustHandle(adminRouteDefinition{
		Method:      http.MethodDelete,
		Path:        "/admin/api/accounts",
		Permissions: []adminauth.Permission{adminauth.PermissionCredentialWrite, adminauth.PermissionDangerousExecute},
		CSRF:        csrfSession,
		Handler:     noopAdminHandler(),
	})
	policy, ok := registry.Policy(http.MethodDelete, "/admin/api/accounts")
	if !ok {
		t.Fatal("policy missing")
	}
	policy.Permissions[0] = adminauth.PermissionView
	again, _ := registry.Policy(http.MethodDelete, "/admin/api/accounts")
	want := []adminauth.Permission{adminauth.PermissionCredentialWrite, adminauth.PermissionDangerousExecute}
	if !reflect.DeepEqual(again.Permissions, want) {
		t.Fatalf("permissions = %v, want %v", again.Permissions, want)
	}
}

func TestAdminRouteRegistryAllowsOnlyExactProtocolExemptions(t *testing.T) {
	registry := newAdminRouteRegistry()
	for _, def := range []adminRouteDefinition{
		{Method: http.MethodPost, Path: "/admin/api/login", Public: true, CSRF: csrfLocalEntry, Handler: noopAdminHandler()},
		{Method: http.MethodPost, Path: "/admin/api/auth/oidc/start", Public: true, CSRF: csrfSSOStart, Handler: noopAdminHandler()},
		{Method: http.MethodPost, Path: "/admin/api/auth/saml/start", Public: true, CSRF: csrfSSOStart, Handler: noopAdminHandler()},
		{Method: http.MethodGet, Path: "/admin/api/auth/oidc/callback", Public: true, CSRF: csrfOIDCCallback, Handler: noopAdminHandler()},
		{Method: http.MethodPost, Path: "/admin/api/auth/saml/acs", Public: true, CSRF: csrfSAMLACS, Handler: noopAdminHandler()},
	} {
		if err := registry.Handle(def); err != nil {
			t.Fatalf("valid exemption rejected: %v", err)
		}
	}
	bad := adminRouteDefinition{Method: http.MethodPost, Path: "/admin/api/sso/import", Public: true, CSRF: csrfSAMLACS, Handler: noopAdminHandler()}
	if err := registry.Handle(bad); err == nil {
		t.Fatal("Cline SSO import must not qualify as a SAML ACS exemption")
	}
}
