package adminauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type memoryRepo struct {
	mu     sync.Mutex
	state  State
	events []AuditEvent
	fail   bool
}

func newMemoryRepo() *memoryRepo { return &memoryRepo{state: EmptyState()} }
func (r *memoryRepo) Load(context.Context) (State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return CloneState(r.state), nil
}
func (r *memoryRepo) Apply(_ context.Context, e AuditEvent, fn func(*State) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		return fmt.Errorf("disk unavailable")
	}
	s := CloneState(r.state)
	if err := fn(&s); err != nil {
		return err
	}
	s.Revision++
	if err := ValidateState(s); err != nil {
		return err
	}
	r.state = s
	r.events = append(r.events, e)
	return nil
}
func (r *memoryRepo) Close() error { return nil }
func testService(t *testing.T) (*Service, *memoryRepo) {
	t.Helper()
	r := newMemoryRepo()
	s, e := NewService(r, Options{PasswordParams: PasswordParams{19 * 1024, 2, 1, 16, 32}, BootstrapPassword: "one time bootstrap secret"})
	if e != nil {
		t.Fatal(e)
	}
	return s, r
}
func bootstrap(t *testing.T, s *Service) Credentials {
	t.Helper()
	c, e := s.CompleteBootstrap(context.Background(), "one time bootstrap secret", "permanent admin password")
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func TestBootstrapAndSession(t *testing.T) {
	s, r := testService(t)
	ctx := context.Background()
	if _, e := s.Login(ctx, "admin", "anything", "127.0.0.1"); !errors.Is(e, ErrNotInitialized) {
		t.Fatal(e)
	}
	if _, e := s.CompleteBootstrap(ctx, "wrong bootstrap", "permanent admin password"); !errors.Is(e, ErrUnauthorized) {
		t.Fatal(e)
	}
	c := bootstrap(t, s)
	p, e := s.Authenticate(ctx, c.Token)
	if e != nil || !p.HasPermission(PermissionSecurityWrite) {
		t.Fatal(p, e)
	}
	if e = s.ValidateCSRF(p, c.CSRFToken); e != nil {
		t.Fatal(e)
	}
	if e = s.ValidateCSRF(p, strings.Repeat("x", 43)); e == nil {
		t.Fatal("wrong CSRF accepted")
	}
	if _, e = s.CompleteBootstrap(ctx, "one time bootstrap secret", "another admin password"); !errors.Is(e, ErrAlreadyInitialized) {
		t.Fatal(e)
	}
	persisted, _ := json.Marshal(r.state)
	for _, secret := range []string{c.Token, c.CSRFToken, "one time bootstrap secret", "permanent admin password"} {
		if strings.Contains(string(persisted), secret) {
			t.Fatal("plaintext secret persisted")
		}
	}
	s2, e := NewService(r, Options{PasswordParams: PasswordParams{19 * 1024, 2, 1, 16, 32}, BootstrapPassword: "changed bootstrap env"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s2.Authenticate(ctx, c.Token); e != nil {
		t.Fatal("session lost on restart", e)
	}
	if _, e = s2.CompleteBootstrap(ctx, "changed bootstrap env", "another admin password"); !errors.Is(e, ErrAlreadyInitialized) {
		t.Fatal(e)
	}
}
func TestPendingBootstrapRotatesOnRestartAndDelivery(t *testing.T) {
	r := newMemoryRepo()
	delivered := ""
	opts := Options{PasswordParams: PasswordParams{19 * 1024, 2, 1, 16, 32}, DeliverBootstrap: func(v string) error { delivered = v; return nil }}
	s, e := NewService(r, opts)
	if e != nil {
		t.Fatal(e)
	}
	first := delivered
	if first == "" {
		t.Fatal("secret not delivered")
	}
	s, e = NewService(r, opts)
	if e != nil {
		t.Fatal(e)
	}
	if delivered == first {
		t.Fatal("pending bootstrap did not rotate")
	}
	if _, e = s.CompleteBootstrap(context.Background(), first, "permanent admin password"); !errors.Is(e, ErrUnauthorized) {
		t.Fatal(e)
	}
	if _, e = NewService(newMemoryRepo(), Options{PasswordParams: opts.PasswordParams}); e == nil {
		t.Fatal("headless anonymous startup accepted")
	}
}
func TestSessionRevocationAndLockdown(t *testing.T) {
	s, r := testService(t)
	c := bootstrap(t, s)
	ctx := context.Background()
	c2, e := s.Login(ctx, " ADMIN ", "permanent admin password", "127.0.0.1")
	if e != nil {
		t.Fatal(e)
	}
	if e = ValidateCSRF(c2.Principal, c.CSRFToken); e == nil {
		t.Fatal("CSRF crosses session")
	}
	if e = s.Logout(ctx, c.Principal); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(ctx, c.Token); !errors.Is(e, ErrUnauthorized) {
		t.Fatal(e)
	}
	r.fail = true
	if e = s.Logout(ctx, c2.Principal); e == nil {
		t.Fatal("failed revoke acknowledged")
	}
	if _, e = s.Authenticate(ctx, c2.Token); !errors.Is(e, ErrDegraded) {
		t.Fatal("durable failure did not lock access", e)
	}
}
func TestSessionExpiryAndRoleRevision(t *testing.T) {
	s, _ := testService(t)
	c := bootstrap(t, s)
	ctx := context.Background()
	if e := s.Mutate(ctx, c.Principal, AuditRoleChange, BreakGlassID, func(st *State) error {
		u := st.Users[BreakGlassID]
		u.Roles = []Role{RoleViewer}
		st.Users[u.ID] = u
		return nil
	}); e == nil {
		t.Fatal("break-glass role removed")
	}
	if _, e := s.Authenticate(ctx, c.Token); e != nil {
		t.Fatal("rejected mutation revoked session")
	}
	s.now = func() time.Time { return c.ExpiresAt }
	if _, e := s.Authenticate(ctx, c.Token); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("expired session accepted", e)
	}
}
func TestLegacyRotationSentinelOrder(t *testing.T) {
	r := newMemoryRepo()
	salt := "old-salt"
	sum := sha256.Sum256([]byte(salt + "old"))
	legacy := LegacyCredential{Salt: salt, Hash: hex.EncodeToString(sum[:])}
	called := false
	s, e := NewService(r, Options{PasswordParams: PasswordParams{19 * 1024, 2, 1, 16, 32}, Legacy: &legacy, MarkLegacyMigrated: func() error {
		st, _ := r.Load(context.Background())
		if st.Users[BreakGlassID].Legacy != nil || st.Users[BreakGlassID].PasswordPHC == "" {
			t.Fatal("sentinel precedes durability")
		}
		called = true
		return nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Login(context.Background(), "admin", "old", "127.0.0.1"); !errors.Is(e, ErrPasswordRotationRequired) {
		t.Fatal(e)
	}
	if called {
		t.Fatal("sentinel before rotation")
	}
	c, e := s.CompleteLegacyRotation(context.Background(), "admin", "old", "new permanent password", "127.0.0.1")
	if e != nil {
		t.Fatal(e)
	}
	if !called {
		t.Fatal("sentinel not set")
	}
	if _, e = s.Authenticate(context.Background(), c.Token); e != nil {
		t.Fatal(e)
	}
	if VerifyLegacy(LegacyCredential{Salt: salt, Hash: LegacyMigrationSentinel}, "old") {
		t.Fatal("sentinel match")
	}
}
func TestPasswordRotationRevokesAllSessions(t *testing.T) {
	s, _ := testService(t)
	c := bootstrap(t, s)
	ctx := context.Background()
	other, e := s.Login(ctx, "admin", "permanent admin password", "127.0.0.1")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.RotatePassword(ctx, c.Principal, "permanent admin password", "rotated permanent password"); e != nil {
		t.Fatal(e)
	}
	for _, token := range []string{c.Token, other.Token} {
		if _, e = s.Authenticate(ctx, token); !errors.Is(e, ErrUnauthorized) {
			t.Fatal(e)
		}
	}
}
func TestRBACMatrix(t *testing.T) {
	for role, permissions := range rolePermissions {
		p := Principal{Roles: []Role{role}, Method: MethodLocal}
		for _, want := range []Permission{PermissionView, PermissionProviderWrite, PermissionCredentialWrite, PermissionCredentialExport, PermissionSecurityWrite, PermissionDangerousExecute} {
			expected := false
			for _, v := range permissions {
				if v == want {
					expected = true
				}
			}
			if p.HasPermission(want) != expected {
				t.Fatal(role, want)
			}
		}
		if p.HasPermission("") {
			t.Fatal("empty permission allowed")
		}
	}
	if (Principal{Roles: []Role{RoleBreakGlass}, Method: MethodOIDC}).HasPermission(PermissionSecurityWrite) {
		t.Fatal("SSO break-glass permissions")
	}
}
func TestForgedPrincipalPermissionRejected(t *testing.T) {
	s, _ := testService(t)
	c := bootstrap(t, s)
	p := c.Principal
	p.Roles = []Role{RoleViewer}
	if e := s.Mutate(context.Background(), p, AuditUserChange, "x", func(*State) error { t.Fatal("forged principal invoked mutation"); return nil }); e == nil {
		t.Fatal("forged roles accepted")
	}
}
func TestOfflineRecoveryRemainsInitializedAndRevokes(t *testing.T) {
	s, r := testService(t)
	c := bootstrap(t, s)
	secret := ""
	if e := RecoverOffline(context.Background(), r, Options{PasswordParams: PasswordParams{19 * 1024, 2, 1, 16, 32}, DeliverBootstrap: func(v string) error { secret = v; return nil }}); e != nil {
		t.Fatal(e)
	}
	if secret == "" {
		t.Fatal("missing local recovery delivery")
	}
	st, _ := r.Load(context.Background())
	if !st.Initialized || st.RecoveryPHC == "" || len(st.Sessions) != 0 {
		t.Fatal("invalid recovery state")
	}
	s, e := NewService(r, Options{PasswordParams: PasswordParams{19 * 1024, 2, 1, 16, 32}, BootstrapPassword: "ignored recovery env"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(context.Background(), c.Token); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("old session after recovery", e)
	}
	if _, e = s.CompleteBootstrap(context.Background(), "ignored recovery env", "recovered permanent password"); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("environment recreated recovery", e)
	}
	if _, e = s.CompleteBootstrap(context.Background(), secret, "recovered permanent password"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CompleteBootstrap(context.Background(), secret, "replayed permanent password"); !errors.Is(e, ErrAlreadyInitialized) {
		t.Fatal("one-time recovery reused", e)
	}
}
func TestOnlineMutationCannotCreateRecovery(t *testing.T) {
	s, _ := testService(t)
	c := bootstrap(t, s)
	if e := s.Mutate(context.Background(), c.Principal, AuditUserChange, BreakGlassID, func(st *State) error {
		st.RecoveryPHC = st.Users[BreakGlassID].PasswordPHC
		st.Sessions = map[string]Session{}
		return nil
	}); !errors.Is(e, ErrForbidden) {
		t.Fatal("online recovery allowed", e)
	}
}
func TestDenialAuditIsBoundedAndCoalesced(t *testing.T) {
	s, r := testService(t)
	for i := 0; i < 2000; i++ {
		s.RecordDenial(AuditLoginDenied, fmt.Sprintf("10.0.%d.%d", i/250, i%250))
	}
	if len(s.Denials()) > 131 {
		t.Fatal("unbounded denial buffer")
	}
	before := len(r.events)
	if e := s.FlushDenials(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := s.FlushDenials(context.Background()); e != nil {
		t.Fatal(e)
	}
	if len(r.events) != before+1 {
		t.Fatal("denial fsync amplification", len(r.events)-before)
	}
}
func TestUserViewRedactsCredentialAndSubject(t *testing.T) {
	u := User{ID: "u", Username: "user", PasswordPHC: "secret-hash", Legacy: &LegacyCredential{Salt: "salt", Hash: "legacy-secret"}, Subject: "private-claim"}
	b, _ := json.Marshal(u.Redacted())
	for _, v := range []string{"secret-hash", "salt", "legacy-secret", "private-claim"} {
		if strings.Contains(string(b), v) {
			t.Fatal("user response leaked", v)
		}
	}
}
func TestLegacySentinelRetriedAfterRestart(t *testing.T) {
	s, r := testService(t)
	bootstrap(t, s)
	sum := sha256.Sum256([]byte("saltold-password"))
	called := false
	_, e := NewService(r, Options{PasswordParams: PasswordParams{19 * 1024, 2, 1, 16, 32}, Legacy: &LegacyCredential{Salt: "salt", Hash: hex.EncodeToString(sum[:])}, MarkLegacyMigrated: func() error { called = true; return nil }})
	if e != nil || !called {
		t.Fatal("downgrade sentinel was not retried", e)
	}
}
func TestProviderDisableRevokesFlowAndSession(t *testing.T) {
	s, _ := testService(t)
	c := bootstrap(t, s)
	ctx := context.Background()
	if e := s.Mutate(ctx, c.Principal, AuditSSOChange, "idp", func(st *State) error {
		st.Providers["idp"] = ProviderState{ID: "idp", Type: MethodOIDC, Enabled: true}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	credentials, e := s.IssueFederatedSession(ctx, "idp", 1, "subject", "federated-user", []Role{RoleViewer}, MethodOIDC, "127.0.0.1")
	if e != nil {
		t.Fatal(e)
	}
	handle, e := s.Flows().Create(AuthFlow{Provider: "idp", ProviderRevision: 1, ProviderFingerprint: "fingerprint", Type: MethodOIDC, Callback: "https://admin.example/callback"})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Mutate(ctx, c.Principal, AuditSSOChange, "idp", func(st *State) error {
		p := st.Providers["idp"]
		p.Enabled = false
		st.Providers["idp"] = p
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(ctx, credentials.Token); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("SSO session survives provider disable", e)
	}
	if _, e = s.Flows().Consume(handle, "idp", MethodOIDC); !errors.Is(e, ErrReplay) {
		t.Fatal("SSO flow survives provider disable", e)
	}
	if _, e = s.IssueFederatedSession(ctx, "idp", 1, "subject", "federated-user", []Role{RoleViewer}, MethodOIDC, "127.0.0.1"); !errors.Is(e, ErrForbidden) {
		t.Fatal("disabled provider issued session", e)
	}
}

func TestFederatedSessionRejectsStaleProviderRevision(t *testing.T) {
	s, _ := testService(t)
	admin := bootstrap(t, s)
	ctx := context.Background()
	if err := s.Mutate(ctx, admin.Principal, AuditSSOChange, "idp", func(st *State) error {
		st.Providers["idp"] = ProviderState{ID: "idp", Type: MethodOIDC, Enabled: true, Configuration: json.RawMessage(`{"issuer":"https://issuer.example"}`)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IssueFederatedSession(ctx, "idp", 2, "subject", "federated-user", []Role{RoleViewer}, MethodOIDC, "127.0.0.1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("future provider revision accepted: %v", err)
	}
	if err := s.Mutate(ctx, admin.Principal, AuditSSOChange, "idp", func(st *State) error {
		provider := st.Providers["idp"]
		provider.Configuration = json.RawMessage(`{"issuer":"https://new-issuer.example"}`)
		st.Providers["idp"] = provider
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IssueFederatedSession(ctx, "idp", 1, "subject", "federated-user", []Role{RoleViewer}, MethodOIDC, "127.0.0.1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stale provider revision accepted: %v", err)
	}
}
func TestLocalUserRolesAndPasswordResetRevoke(t *testing.T) {
	s, _ := testService(t)
	admin := bootstrap(t, s)
	ctx := context.Background()
	u, e := s.CreateLocalUser(ctx, admin.Principal, "Viewer", "viewer permanent password", []Role{RoleViewer})
	if e != nil {
		t.Fatal(e)
	}
	viewer, e := s.Login(ctx, "viewer", "viewer permanent password", "192.0.2.1")
	if e != nil {
		t.Fatal(e)
	}
	if viewer.Principal.HasPermission(PermissionCredentialExport) {
		t.Fatal("viewer credential export")
	}
	if _, e = s.CreateLocalUser(ctx, viewer.Principal, "intruder", "intruder permanent password", []Role{RoleSecurityAdmin}); !errors.Is(e, ErrForbidden) {
		t.Fatal("viewer creates security admin", e)
	}
	if e = s.SetLocalPassword(ctx, admin.Principal, u.ID, "updated viewer password"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(ctx, viewer.Token); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("password reset failed to revoke", e)
	}
	if _, e = s.ListUsers(ctx, admin.Principal); e != nil {
		t.Fatal(e)
	}
	sessions, e := s.ListSessions(ctx, admin.Principal)
	if e != nil || len(sessions) != 1 {
		t.Fatal(sessions, e)
	}
	if e = s.SetLocalPassword(ctx, admin.Principal, BreakGlassID, "bypassed permanent password"); !errors.Is(e, ErrForbidden) {
		t.Fatal("break-glass password reset bypass", e)
	}
}

func TestUserLifecycleIsRevisionCheckedAndRevokesSessions(t *testing.T) {
	service, _ := testService(t)
	admin := bootstrap(t, service)
	ctx := context.Background()
	created, err := service.CreateLocalUser(ctx, admin.Principal, "operator", "operator permanent password", []Role{RoleViewer})
	if err != nil {
		t.Fatal(err)
	}
	operator, err := service.Login(ctx, created.Username, "operator permanent password", "192.0.2.40")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.UpdateUser(ctx, admin.Principal, created.ID, created.Revision, []Role{RoleProviderAdmin, RoleViewer}, false)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != created.Revision+1 || !hasRole(updated.Roles, RoleProviderAdmin) {
		t.Fatalf("updated user = %+v", updated)
	}
	if _, err := service.Authenticate(ctx, operator.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("role change retained old session: %v", err)
	}
	if _, err := service.UpdateUser(ctx, admin.Principal, created.ID, created.Revision, []Role{RoleViewer}, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale user update = %v", err)
	}
	if _, err := service.UpdateUser(ctx, admin.Principal, BreakGlassID, 1, []Role{RoleViewer}, true); !errors.Is(err, ErrForbidden) {
		t.Fatalf("break-glass mutation = %v", err)
	}
	if err := service.SetLocalPasswordAtRevision(ctx, admin.Principal, created.ID, updated.Revision, "rotated operator password"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Login(ctx, created.Username, "operator permanent password", "192.0.2.40"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old reset password accepted: %v", err)
	}
	if _, err := service.Login(ctx, created.Username, "rotated operator password", "192.0.2.40"); err != nil {
		t.Fatalf("reset password rejected: %v", err)
	}
	state, err := service.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	latest := state.Users[created.ID]
	if err := service.DeleteLocalUser(ctx, admin.Principal, created.ID, latest.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Login(ctx, created.Username, "rotated operator password", "192.0.2.40"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("deleted user logged in: %v", err)
	}
}
