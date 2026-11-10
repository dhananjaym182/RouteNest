package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"routenest/internal/adminapi"
)

func TestEgressCompatibilityAliasesRetainIdenticalSecurityPolicy(t *testing.T) {
	policies := AdminRoutePolicies()
	for _, pair := range []struct {
		method, oldPath, currentPath string
	}{
		{http.MethodGet, "/admin/api/cline-proxy/config", "/admin/api/routenest/config"},
		{http.MethodPost, "/admin/api/cline-proxy/config/update", "/admin/api/routenest/config/update"},
	} {
		var oldPolicy, currentPolicy *RoutePolicy
		for index := range policies {
			policy := &policies[index]
			if policy.Method == pair.method && policy.Path == pair.oldPath {
				oldPolicy = policy
			}
			if policy.Method == pair.method && policy.Path == pair.currentPath {
				currentPolicy = policy
			}
		}
		if oldPolicy == nil || currentPolicy == nil {
			t.Fatalf("compatibility policy missing for %s", pair.oldPath)
		}
		if oldPolicy.Public != currentPolicy.Public || oldPolicy.CSRF != currentPolicy.CSRF || len(oldPolicy.Permissions) != len(currentPolicy.Permissions) {
			t.Fatal("compatibility policy differs from current policy")
		}
		for index := range currentPolicy.Permissions {
			if currentPolicy.Permissions[index] != oldPolicy.Permissions[index] {
				t.Fatal("compatibility alias changed permission")
			}
		}
	}
}

func TestEgressCompatibilityAliasDoesNotBypassAuthenticationOrCSRF(t *testing.T) {
	mux, runtime := newAdminAuthTestMux(t)
	credentials, err := runtime.service.CompleteBootstrap(t.Context(), "one time bootstrap secret", "AliasPassword8!")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/admin/api/cline-proxy/config", "/admin/api/routenest/config"} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, adminTestRequest(http.MethodGet, path, ""))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous alias returned %d", recorder.Code)
		}
	}
	for _, path := range []string{"/admin/api/cline-proxy/config/update", "/admin/api/routenest/config/update"} {
		request := adminTestRequest(http.MethodPost, path, `{}`)
		request.AddCookie(&http.Cookie{Name: adminapi.SessionCookieName, Value: credentials.Token})
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("missing CSRF alias returned %d", recorder.Code)
		}
	}
}
