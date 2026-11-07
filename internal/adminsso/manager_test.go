package adminsso

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"

	"routenest/internal/adminauth"
)

type managerMemoryRepo struct {
	mu    sync.Mutex
	state adminauth.State
}

func newManagerMemoryRepo() *managerMemoryRepo {
	return &managerMemoryRepo{state: adminauth.EmptyState()}
}

func (r *managerMemoryRepo) Load(context.Context) (adminauth.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return adminauth.CloneState(r.state), nil
}

func (r *managerMemoryRepo) Apply(_ context.Context, _ adminauth.AuditEvent, mutate func(*adminauth.State) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := adminauth.CloneState(r.state)
	if err := mutate(&next); err != nil {
		return err
	}
	next.Revision++
	if err := adminauth.ValidateState(next); err != nil {
		return err
	}
	r.state = next
	return nil
}

func (*managerMemoryRepo) Close() error { return nil }

func newManagerTestService(t *testing.T) (*adminauth.Service, adminauth.Credentials) {
	t.Helper()
	service, err := adminauth.NewService(newManagerMemoryRepo(), adminauth.Options{
		PasswordParams:    adminauth.PasswordParams{Memory: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32},
		BootstrapPassword: "one time bootstrap secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := service.CompleteBootstrap(t.Context(), "one time bootstrap secret", "permanent administrator password")
	if err != nil {
		t.Fatal(err)
	}
	return service, credentials
}

func newManagerForTest(t *testing.T, service *adminauth.Service) *Manager {
	t.Helper()
	canonical, err := url.Parse("http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(t.Context(), ManagerOptions{Service: service, CanonicalURL: canonical})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func managerOIDCConfig(enabled bool) ProviderConfig {
	return ProviderConfig{
		ID: "company", Type: adminauth.MethodOIDC, Enabled: enabled,
		OIDC: &OIDCConfig{
			Issuer: "https://issuer.invalid", ClientID: "routenest-admin",
			ClientSecret: SecretRef{Kind: "env", Name: "ROUTENEST_TEST_MISSING_SSO_SECRET"},
			RedirectURL:  "http://127.0.0.1:8080/admin/api/auth/oidc/callback",
			DefaultRoles: []adminauth.Role{adminauth.RoleViewer},
		},
	}
}

func TestManagerPersistsReferencesAndKeepsBrokenSSOIsolated(t *testing.T) {
	service, admin := newManagerTestService(t)
	manager := newManagerForTest(t, service)

	disabled := managerOIDCConfig(false)
	view, err := manager.Upsert(t.Context(), admin.Principal, disabled)
	if err != nil {
		t.Fatal(err)
	}
	if view.Ready || view.Reason != "disabled" || view.Revision != 1 {
		t.Fatalf("disabled provider readiness = %+v", view)
	}
	state, err := service.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	raw := string(state.Providers[disabled.ID].Configuration)
	if !json.Valid([]byte(raw)) || !containsAll(raw, "ROUTENEST_TEST_MISSING_SSO_SECRET", `"kind":"env"`) {
		t.Fatalf("reference-only provider configuration not persisted: %s", raw)
	}
	if providers, err := manager.LoginProviders(t.Context()); err != nil || len(providers) != 0 {
		t.Fatalf("disabled provider exposed on login: %+v, %v", providers, err)
	}

	enabled := disabled
	enabled.Enabled = true
	view, err = manager.Upsert(t.Context(), admin.Principal, enabled)
	if err != nil {
		t.Fatal(err)
	}
	if view.Ready || view.Reason != "provider_unavailable" || view.Revision != 2 {
		t.Fatalf("broken enabled provider readiness = %+v", view)
	}
	providers, err := manager.LoginProviders(t.Context())
	if err != nil || len(providers) != 1 || providers[0].Ready || providers[0].Reason != "provider_unavailable" {
		t.Fatalf("enabled provider status = %+v, %v", providers, err)
	}
	readinessOIDC, readinessSAML, err := manager.Readiness(t.Context())
	if err != nil || readinessOIDC[enabled.ID].Ready || readinessOIDC[enabled.ID].Reason != "provider_unavailable" || len(readinessSAML) != 0 {
		t.Fatalf("provider readiness = %+v %+v %v", readinessOIDC, readinessSAML, err)
	}
	if _, err := service.Login(t.Context(), adminauth.BreakGlassUsername, "permanent administrator password", "127.0.0.1"); err != nil {
		t.Fatalf("broken SSO disabled local break-glass login: %v", err)
	}
}

func TestManagerRejectsCanonicalMismatchAndUnauthorizedMutation(t *testing.T) {
	service, admin := newManagerTestService(t)
	manager := newManagerForTest(t, service)
	config := managerOIDCConfig(false)
	config.OIDC.RedirectURL = "https://attacker.example/callback"
	if _, err := manager.Upsert(t.Context(), admin.Principal, config); err == nil {
		t.Fatal("noncanonical OIDC callback accepted")
	}
	state, _ := service.State(t.Context())
	if len(state.Providers) != 0 {
		t.Fatal("invalid provider persisted")
	}

	viewer, err := service.CreateLocalUser(t.Context(), admin.Principal, "viewer", "permanent viewer password", []adminauth.Role{adminauth.RoleViewer})
	if err != nil {
		t.Fatal(err)
	}
	viewerCredentials, err := service.Login(t.Context(), viewer.Username, "permanent viewer password", "127.0.0.2")
	if err != nil {
		t.Fatal(err)
	}
	config = managerOIDCConfig(false)
	if _, err := manager.Upsert(t.Context(), viewerCredentials.Principal, config); !errors.Is(err, adminauth.ErrForbidden) {
		t.Fatalf("viewer changed SSO provider: %v", err)
	}
}

func TestManagerDeleteRevokesProviderState(t *testing.T) {
	service, admin := newManagerTestService(t)
	manager := newManagerForTest(t, service)
	config := managerOIDCConfig(false)
	if _, err := manager.Upsert(t.Context(), admin.Principal, config); err != nil {
		t.Fatal(err)
	}
	if err := manager.Delete(t.Context(), admin.Principal, config.ID); err != nil {
		t.Fatal(err)
	}
	state, _ := service.State(t.Context())
	if _, exists := state.Providers[config.ID]; exists {
		t.Fatal("deleted provider remains persisted")
	}
	if err := manager.Delete(t.Context(), admin.Principal, config.ID); !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("second delete = %v", err)
	}
}

func TestManagerOIDCIssuesUnifiedDurableSession(t *testing.T) {
	fixture := newOIDCIssuerFixture(t)
	fixture.callbackURL = fixture.server.URL + "/admin/api/auth/oidc/callback"
	service, admin := newManagerTestService(t)
	canonical, err := url.Parse(fixture.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(t.Context(), ManagerOptions{Service: service, CanonicalURL: canonical, HTTPClient: fixture.server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	oidcConfig := fixture.config(t)
	oidcConfig.DefaultRoles = []adminauth.Role{adminauth.RoleViewer}
	config := ProviderConfig{ID: "fixture", Type: adminauth.MethodOIDC, Enabled: true, OIDC: &oidcConfig}
	view, err := manager.Upsert(t.Context(), admin.Principal, config)
	if err != nil || !view.Ready {
		t.Fatalf("ready OIDC provider = %+v, %v", view, err)
	}
	authorizationURL, err := manager.StartOIDC(t.Context(), config.ID, "127.0.0.1", "/admin/#security")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	values := parsed.Query()
	fixture.mu.Lock()
	fixture.nonce = values.Get("nonce")
	fixture.challenge = values.Get("code_challenge")
	fixture.mu.Unlock()
	credentials, returnLocation, err := manager.CompleteOIDC(t.Context(), values.Get("state"), "valid-code", "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	if returnLocation != "/admin/#security" || credentials.Token == "" || credentials.CSRFToken == "" {
		t.Fatalf("federated credentials or return location invalid: %+v %q", credentials, returnLocation)
	}
	principal, err := service.Authenticate(t.Context(), credentials.Token)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Method != adminauth.MethodOIDC || principal.Provider != config.ID || principal.Username != "user@example.test" || !principal.HasPermission(adminauth.PermissionProviderWrite) || !principal.HasPermission(adminauth.PermissionView) {
		t.Fatalf("unexpected federated principal: %+v", principal)
	}
	if _, _, err := manager.CompleteOIDC(t.Context(), values.Get("state"), "valid-code", "192.0.2.10"); !errors.Is(err, adminauth.ErrReplay) {
		t.Fatalf("OIDC state replay accepted: %v", err)
	}
	state, err := service.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{credentials.Token, credentials.CSRFToken, "fixture-client-secret", "raw-private-token", "private-refresh-token"} {
		if strings.Contains(string(persisted), secret) {
			t.Fatalf("durable administrator state leaked secret %q", secret)
		}
	}
}

func TestSAMLProviderIDBoundedForRelayState(t *testing.T) {
	config := ProviderConfig{
		ID: "a123456789012345678901234567890123456", Type: adminauth.MethodSAML,
		SAML: &SAMLConfig{
			MetadataURL: "https://idp.example/metadata", EntityID: "https://admin.example/saml",
			MetadataEndpoint: "https://admin.example/admin/api/auth/saml/metadata?provider=a", ACSURL: "https://admin.example/admin/api/auth/saml/acs",
			DefaultRoles: []adminauth.Role{adminauth.RoleViewer},
		},
	}
	if err := config.validate(); err == nil {
		t.Fatal("overlong SAML provider ID accepted")
	}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}
