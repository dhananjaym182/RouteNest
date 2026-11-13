package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"routenest/internal/accounts"
	"routenest/internal/adminapi"
	"routenest/internal/adminauth"
	"routenest/internal/domain"
	"routenest/internal/platform"
	poolstore "routenest/internal/store/accounts"
	storeadminauth "routenest/internal/store/adminauth"
)

func TestListenerChangeUsesDurableAdminAudit(t *testing.T) {
	_, runtime := newAdminAuthTestMux(t)
	credentials, err := runtime.service.CompleteBootstrap(t.Context(), "one time bootstrap secret", "permanent admin password")
	if err != nil {
		t.Fatal(err)
	}
	request := adminTestRequest(http.MethodPost, "/admin/api/config/listener", `{"host":"localhost"}`)
	ctx := context.WithValue(request.Context(), adminSecurityRuntimeContextKey{}, runtime)
	ctx = context.WithValue(ctx, adminPrincipalContextKey{}, credentials.Principal)
	request = request.WithContext(ctx)
	if err := adminConfig.AuditListenerChange(request, "127.0.0.1", "localhost"); err != nil {
		t.Fatal(err)
	}
	events, err := runtime.store.AuditEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Kind == adminauth.AuditListenerChange && event.ActorID == credentials.Principal.UserID && event.TargetID == "localhost" {
			return
		}
	}
	t.Fatalf("durable listener audit event missing: %+v", events)
}

func TestClientKeyMutationsRequireFinalConfirmationAndWriteRedactedAudit(t *testing.T) {
	accounts.PoolMu.Lock()
	oldPool, oldPath := accounts.Pool, accounts.PoolPath
	path := filepath.Join(t.TempDir(), poolstore.FileName)
	seed := &domain.AccountPool{Accounts: []*domain.Account{}, Keys: []string{}, Models: []domain.Model{}}
	accounts.Pool, accounts.PoolPath = seed, path
	accounts.PoolMu.Unlock()
	t.Cleanup(func() {
		accounts.PoolMu.Lock()
		accounts.Pool, accounts.PoolPath = oldPool, oldPath
		accounts.PoolMu.Unlock()
	})
	if err := poolstore.SaveDurable(path, seed); err != nil {
		t.Fatal(err)
	}

	mux, runtime := newAdminAuthTestMux(t)
	credentials, err := runtime.service.CompleteBootstrap(t.Context(), "one time bootstrap secret", "permanent admin password")
	if err != nil {
		t.Fatal(err)
	}
	request := func(path, body string) *http.Request {
		req := adminTestRequest(http.MethodPost, path, body)
		req.AddCookie(&http.Cookie{Name: adminapi.SessionCookieName, Value: credentials.Token})
		req.Header.Set("X-RouteNest-CSRF", credentials.CSRFToken)
		return req
	}

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request("/admin/api/keys/generate", `{}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("generate status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var generated struct {
		Success bool `json:"success"`
		Data    struct {
			Key string `json:"key"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &generated); err != nil || generated.Data.Key == "" {
		t.Fatalf("generate response=%s err=%v", recorder.Body.String(), err)
	}

	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request("/admin/api/keys/delete", `{"key":"`+generated.Data.Key+`"}`))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "final_key_confirmation_required") {
		t.Fatalf("unconfirmed final delete status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request("/admin/api/keys/delete", `{"key":"`+generated.Data.Key+`","confirmation":"disable-client-authentication"}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("confirmed final delete status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	events, err := runtime.store.AuditEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := map[adminauth.AuditKind]string{
		adminauth.AuditClientKeyCreate:      "success",
		adminauth.AuditClientKeyDeleteFinal: "success",
	}
	foundFailedFinal := false
	for _, event := range events {
		if strings.Contains(event.TargetID, generated.Data.Key) || strings.Contains(event.ActorID, generated.Data.Key) {
			t.Fatalf("raw key entered audit event: %+v", event)
		}
		if event.Kind == adminauth.AuditClientKeyDeleteFinal && event.Outcome == "failed" {
			foundFailedFinal = true
		}
		if outcome, ok := want[event.Kind]; ok && event.Outcome == outcome {
			delete(want, event.Kind)
		}
	}
	if len(want) != 0 || !foundFailedFinal {
		t.Fatalf("missing key audit events: remaining=%v events=%+v", want, events)
	}
}

func newAdminAuthTestMux(t *testing.T) (*http.ServeMux, *adminSecurityRuntime) {
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

func TestDeleteAllAccountsAuthorizationMatrix(t *testing.T) {
	mux, runtime := newAdminAuthTestMux(t)
	bootstrap, err := runtime.service.CompleteBootstrap(t.Context(), "one time bootstrap secret", "permanent admin password")
	if err != nil {
		t.Fatal(err)
	}
	breakGlass, err := runtime.service.Authenticate(t.Context(), bootstrap.Token)
	if err != nil {
		t.Fatal(err)
	}
	createSession := func(username string, role adminauth.Role) (string, string) {
		t.Helper()
		password := username + " permanent password"
		if _, err := runtime.service.CreateLocalUser(t.Context(), breakGlass, username, password, []adminauth.Role{role}); err != nil {
			t.Fatal(err)
		}
		credentials, err := runtime.service.Login(t.Context(), username, password, "127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		return credentials.Token, credentials.CSRFToken
	}
	credentialToken, _ := createSession("credential-operator", adminauth.RoleCredentialAdmin)
	providerToken, providerCSRF := createSession("provider-operator-c1", adminauth.RoleProviderAdmin)
	request := func(token, csrf string) *http.Request {
		req := adminTestRequest(http.MethodPost, "/admin/api/accounts/delete-all", `{}`)
		if token != "" {
			req.AddCookie(&http.Cookie{Name: adminapi.SessionCookieName, Value: token})
		}
		if csrf != "" {
			req.Header.Set("X-RouteNest-CSRF", csrf)
		}
		return req
	}
	for _, tc := range []struct {
		name        string
		token, csrf string
		want        int
	}{
		{name: "anonymous", want: http.StatusUnauthorized},
		{name: "missing CSRF", token: credentialToken, want: http.StatusForbidden},
		{name: "missing credential permission", token: providerToken, csrf: providerCSRF, want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, request(tc.token, tc.csrf))
			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tc.want, recorder.Body.String())
			}
		})
	}
}

func TestAdminConfigMutationRoleMatrix(t *testing.T) {
	mux, runtime := newAdminAuthTestMux(t)
	bootstrap, err := runtime.service.CompleteBootstrap(t.Context(), "one time bootstrap secret", "permanent admin password")
	if err != nil {
		t.Fatal(err)
	}
	breakGlass, err := runtime.service.Authenticate(t.Context(), bootstrap.Token)
	if err != nil {
		t.Fatal(err)
	}

	type roleSession struct {
		token string
		csrf  string
	}
	createSession := func(username string, role adminauth.Role) roleSession {
		t.Helper()
		password := username + " permanent password"
		if _, err := runtime.service.CreateLocalUser(t.Context(), breakGlass, username, password, []adminauth.Role{role}); err != nil {
			t.Fatal(err)
		}
		credentials, err := runtime.service.Login(t.Context(), username, password, "127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		return roleSession{token: credentials.Token, csrf: credentials.CSRFToken}
	}
	viewer := createSession("read-only-viewer", adminauth.RoleViewer)
	provider := createSession("provider-operator", adminauth.RoleProviderAdmin)
	security := createSession("security-operator", adminauth.RoleSecurityAdmin)

	requestAs := func(session roleSession, path, body string) *http.Request {
		req := adminTestRequest(http.MethodPost, path, body)
		req.AddCookie(&http.Cookie{Name: adminapi.SessionCookieName, Value: session.token})
		req.Header.Set("X-RouteNest-CSRF", session.csrf)
		return req
	}
	tests := []struct {
		name    string
		session roleSession
		path    string
		body    string
		want    int
	}{
		{"provider updates routing", provider, "/admin/api/config/update", `{}`, http.StatusOK},
		{"provider cannot update listener", provider, "/admin/api/config/listener", `{"host":""}`, http.StatusForbidden},
		{"security cannot update routing", security, "/admin/api/config/update", `{}`, http.StatusForbidden},
		{"security reaches listener handler", security, "/admin/api/config/listener", `{"host":""}`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, requestAs(tt.session, tt.path, tt.body))
			if recorder.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tt.want, recorder.Body.String())
			}
		})
	}

	for _, path := range []string{
		"/admin/api/opencode/config",
		"/admin/api/config",
		"/admin/api/providers",
	} {
		t.Run("viewer cannot read provider secrets "+path, func(t *testing.T) {
			request := adminTestRequest(http.MethodGet, path, "")
			request.AddCookie(&http.Cookie{Name: adminapi.SessionCookieName, Value: viewer.token})
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("viewer status = %d, want 403: %s", recorder.Code, recorder.Body.String())
			}
		})
		t.Run("provider admin can read provider configuration "+path, func(t *testing.T) {
			request := adminTestRequest(http.MethodGet, path, "")
			request.AddCookie(&http.Cookie{Name: adminapi.SessionCookieName, Value: provider.token})
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("provider admin status = %d, want 200: %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
