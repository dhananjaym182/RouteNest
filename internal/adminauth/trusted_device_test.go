package adminauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func trustedDeviceService(t *testing.T) (*Service, *memoryRepo, Credentials, *time.Time) {
	t.Helper()
	service, repo := testService(t)
	now := time.Now().UTC()
	service.now = func() time.Time { return now }
	service.throttle.now = service.now
	return service, repo, bootstrap(t, service), &now
}

func enrollDevice(t *testing.T, service *Service, principal Principal) TrustedDeviceCredential {
	t.Helper()
	credential, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), principal, TrustedDeviceMetadata{Name: "Private workstation", Platform: "linux"}, "")
	if err != nil {
		t.Fatal(err)
	}
	principal.DeviceID = credential.ID
	if err := service.ActivateTrustedDevice(t.Context(), principal, credential.ID); err != nil {
		t.Fatal(err)
	}
	state, err := service.State(t.Context())
	if err != nil || state.TrustedDevices[credential.ID].ActivatedAt.IsZero() {
		t.Fatalf("device activation missing: %+v, %v", state.TrustedDevices[credential.ID], err)
	}
	return credential
}

func devicePrincipal(t *testing.T, service *Service, token string) Principal {
	t.Helper()
	principal, err := service.Authenticate(t.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	return principal
}

func devicePasswordLogin(t *testing.T, service *Service) Credentials {
	t.Helper()
	credentials, err := service.Login(t.Context(), BreakGlassUsername, "permanent admin password", "192.0.2.100")
	if err != nil {
		t.Fatal(err)
	}
	return credentials
}

func TestTrustedDeviceEnrollmentAndFreshSessions(t *testing.T) {
	service, repo, admin, now := trustedDeviceService(t)
	metadata := TrustedDeviceMetadata{Name: "Private workstation", Platform: "linux"}
	credential, err := service.EnrollTrustedDevice(t.Context(), admin.Principal, "permanent admin password", metadata, "")
	if err != nil {
		t.Fatal(err)
	}
	secret, err := base64.RawURLEncoding.Strict().DecodeString(credential.Secret)
	if err != nil || len(secret) != 32 || !validTrustedDeviceToken(credential.ID) {
		t.Fatal("device credential is not a pair of 256-bit values", err)
	}
	device := repo.state.TrustedDevices[credential.ID]
	verifier := sha256.Sum256([]byte(credential.Secret))
	if device.SecretHash != hex.EncodeToString(verifier[:]) || device.UserID != admin.Principal.UserID || device.UserRevision != repo.state.Users[device.UserID].Revision || device.Metadata != metadata {
		t.Fatal("incorrect device verifier or user binding")
	}
	if !device.CreatedAt.Equal(*now) || !device.ExpiresAt.Equal(now.Add(90*24*time.Hour)) || !credential.ExpiresAt.Equal(device.ExpiresAt) || !device.LastUsedAt.IsZero() {
		t.Fatal("incorrect enrollment timestamps")
	}
	principal := devicePrincipal(t, service, admin.Token)
	if principal.DeviceID != credential.ID {
		t.Fatal("enrolling session was not bound to the device")
	}
	if err := service.ValidateCSRF(principal, admin.CSRFToken); err != nil {
		t.Fatal(err)
	}
	if err := service.ActivateTrustedDevice(t.Context(), principal, credential.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListTrustedDevices(t.Context(), admin.Principal); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("stale device binding accepted: %v", err)
	}
	views, err := service.ListTrustedDevices(t.Context(), principal)
	if err != nil || len(views) != 1 || views[0].ID != credential.ID || !views[0].Current {
		t.Fatalf("device list = %v, %v", views, err)
	}
	*now = now.Add(25 * time.Hour)
	service.ttl = time.Hour
	first, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if first.Principal.Method != MethodLocal || first.Principal.Provider != "" || first.Principal.DeviceID != credential.ID || first.Principal.UserID != admin.Principal.UserID || !equalRoles(first.Principal.Roles, admin.Principal.Roles) || !first.ExpiresAt.Equal(now.Add(12*time.Hour)) {
		t.Fatal("trusted device did not issue an ordinary 12-hour local session")
	}
	if first.Token == admin.Token || first.CSRFToken == admin.CSRFToken || !repo.state.TrustedDevices[credential.ID].LastUsedAt.Equal(*now) {
		t.Fatal("session proof was reused or last use was not persisted")
	}
	if err := service.ValidateCSRF(first.Principal, first.CSRFToken); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Minute)
	second, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if first.Token == second.Token || first.CSRFToken == second.CSRFToken || !repo.state.TrustedDevices[credential.ID].LastUsedAt.Equal(*now) {
		t.Fatal("device login did not refresh session proof and last use")
	}
	if err := service.Logout(t.Context(), first.Principal); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(t.Context(), first.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("logout retained the device session", err)
	}
	if _, err := service.Authenticate(t.Context(), second.Token); err != nil {
		t.Fatal("logout revoked an unrelated session", err)
	}
	if len(repo.state.TrustedDevices) != 1 {
		t.Fatal("ordinary logout revoked the device credential")
	}
}

func TestTrustedDeviceWrongUnknownAndExpiredCredentials(t *testing.T) {
	service, repo, admin, now := trustedDeviceService(t)
	credential := enrollDevice(t, service, admin.Principal)
	before := len(repo.events)
	for _, tc := range []struct {
		name   string
		id     string
		secret string
	}{
		{"wrong secret", credential.ID, strings.Repeat("A", 43)},
		{"unknown ID", strings.Repeat("A", 43), credential.Secret},
		{"invalid ID", "unknown device", credential.Secret},
		{"empty secret", credential.ID, ""},
		{"noncanonical secret", credential.ID, strings.Repeat("B", 43)},
		{"oversized secret", credential.ID, strings.Repeat("x", 4096)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := service.AuthenticateTrustedDevice(t.Context(), tc.id, tc.secret, "192.0.2.2")
			if !errors.Is(err, ErrTrustedDeviceUntrusted) || !errors.Is(err, ErrUnauthorized) || got.Token != "" || got.CSRFToken != "" {
				t.Fatalf("invalid device credential result = %v, %v", got, err)
			}
		})
	}
	if len(repo.events) != before || !repo.state.TrustedDevices[credential.ID].LastUsedAt.IsZero() {
		t.Fatal("denied authentication mutated durable state")
	}
	*now = credential.ExpiresAt
	if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.3"); !errors.Is(err, ErrTrustedDeviceExpired) || !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired device credential result = %v", err)
	}
	if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, strings.Repeat("A", 43), "192.0.2.3"); !errors.Is(err, ErrTrustedDeviceUntrusted) {
		t.Fatal("expiry was disclosed without valid proof", err)
	}
	if len(service.Denials()) != 2 {
		t.Fatalf("denials were not coalesced by source: %v", service.Denials())
	}
}

func TestTrustedDeviceRejectsCorruptionAndStaleUsers(t *testing.T) {
	service, repo, admin, now := trustedDeviceService(t)
	credential := enrollDevice(t, service, admin.Principal)
	baseline := CloneState(repo.state)
	for _, tc := range []struct {
		name   string
		change func(*State)
	}{
		{"missing device", func(st *State) { delete(st.TrustedDevices, credential.ID) }},
		{"mismatched ID", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.ID = strings.Repeat("A", 43)
			st.TrustedDevices[credential.ID] = d
		}},
		{"corrupt verifier", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.SecretHash = "invalid"
			st.TrustedDevices[credential.ID] = d
		}},
		{"uppercase verifier", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.SecretHash = strings.ToUpper(d.SecretHash)
			st.TrustedDevices[credential.ID] = d
		}},
		{"corrupt metadata", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.Metadata.Name = "unsafe\nname"
			st.TrustedDevices[credential.ID] = d
		}},
		{"zero creation", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.CreatedAt = time.Time{}
			st.TrustedDevices[credential.ID] = d
		}},
		{"future creation", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.CreatedAt = now.Add(time.Minute)
			st.TrustedDevices[credential.ID] = d
		}},
		{"overlong lifetime", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.ExpiresAt = d.ExpiresAt.Add(time.Second)
			st.TrustedDevices[credential.ID] = d
		}},
		{"future activation", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.ActivatedAt = now.Add(time.Minute)
			st.TrustedDevices[credential.ID] = d
		}},
		{"last use without activation", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.ActivatedAt = time.Time{}
			d.LastUsedAt = *now
			st.TrustedDevices[credential.ID] = d
		}},
		{"future last use", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.LastUsedAt = now.Add(time.Minute)
			st.TrustedDevices[credential.ID] = d
		}},
		{"last use before creation", func(st *State) {
			d := st.TrustedDevices[credential.ID]
			d.LastUsedAt = d.CreatedAt.Add(-time.Second)
			st.TrustedDevices[credential.ID] = d
		}},
		{"missing user", func(st *State) { delete(st.Users, BreakGlassID) }},
		{"disabled user", func(st *State) { u := st.Users[BreakGlassID]; u.Disabled = true; st.Users[BreakGlassID] = u }},
		{"stale user revision", func(st *State) { u := st.Users[BreakGlassID]; u.Revision++; st.Users[BreakGlassID] = u }},
		{"federated user", func(st *State) { u := st.Users[BreakGlassID]; u.Provider = "idp"; st.Users[BreakGlassID] = u }},
		{"missing password", func(st *State) { u := st.Users[BreakGlassID]; u.PasswordPHC = ""; st.Users[BreakGlassID] = u }},
		{"corrupt password", func(st *State) { u := st.Users[BreakGlassID]; u.PasswordPHC = "invalid"; st.Users[BreakGlassID] = u }},
		{"legacy user", func(st *State) {
			u := st.Users[BreakGlassID]
			u.Legacy = &LegacyCredential{Salt: "salt", Hash: hashToken("password")}
			st.Users[BreakGlassID] = u
		}},
		{"offline recovery", func(st *State) { st.RecoveryPHC = st.Users[BreakGlassID].PasswordPHC }},
		{"uninitialized", func(st *State) { st.Initialized = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo.state = CloneState(baseline)
			tc.change(&repo.state)
			service.throttle = NewThrottler()
			got, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.4")
			if !errors.Is(err, ErrTrustedDeviceUntrusted) || got.Token != "" {
				t.Fatalf("corrupt device accepted: %v", err)
			}
			if _, err := service.Authenticate(t.Context(), admin.Token); err == nil {
				t.Fatal("corrupt device binding accepted for an existing session")
			}
		})
	}
}

func TestTrustedDeviceEnrollmentAuthorizationAndFreshPasswordProof(t *testing.T) {
	service, repo, admin, now := trustedDeviceService(t)
	metadata := TrustedDeviceMetadata{Name: "Workstation", Platform: "linux"}
	if _, err := service.EnrollTrustedDevice(t.Context(), admin.Principal, "wrong password", metadata, ""); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("enrollment without current password = %v", err)
	}
	for _, field := range []string{"roles", "method", "provider", "device", "session", "csrf", "username", "expiry"} {
		principal := admin.Principal
		switch field {
		case "roles":
			principal.Roles = []Role{RoleViewer}
		case "method":
			principal.Method = MethodOIDC
		case "provider":
			principal.Provider = "idp"
		case "username":
			principal.Username = "forged-user"
		case "expiry":
			principal.ExpiresAt = principal.ExpiresAt.Add(time.Hour)
		case "device":
			principal.DeviceID = strings.Repeat("A", 43)
		case "session":
			principal.SessionHash = hashToken("unknown")
		case "csrf":
			principal.CSRFHash = hashToken("unknown")
		}
		if _, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), principal, metadata, ""); err == nil {
			t.Fatalf("forged %s accepted", field)
		}
	}
	viewer, err := service.CreateLocalUser(t.Context(), admin.Principal, "viewer", "viewer password", []Role{RoleViewer})
	if err != nil {
		t.Fatal(err)
	}
	viewerSession, err := service.Login(t.Context(), viewer.Username, "viewer password", "192.0.2.5")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), viewerSession.Principal, metadata, ""); !errors.Is(err, ErrForbidden) {
		t.Fatal("viewer enrolled a trusted device", err)
	}
	*now = now.Add(trustedDeviceAuthenticationWindow)
	if _, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), admin.Principal, metadata, ""); !errors.Is(err, ErrForbidden) {
		t.Fatal("old session skipped password reauthentication", err)
	}
	fresh := devicePasswordLogin(t, service)
	credential := enrollDevice(t, service, fresh.Principal)
	principal := devicePrincipal(t, service, fresh.Token)
	if _, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), principal, metadata, ""); !errors.Is(err, ErrForbidden) {
		t.Fatal("bound session reenrolled without password proof", err)
	}
	deviceSession, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.6")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), deviceSession.Principal, metadata, ""); !errors.Is(err, ErrForbidden) {
		t.Fatal("device authentication substituted for password proof", err)
	}
	if len(repo.state.TrustedDevices) != 1 {
		t.Fatal("denied enrollment changed the device set")
	}
}

func TestTrustedDeviceReplacementIsAtomic(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	first := enrollDevice(t, service, admin.Principal)
	login := devicePasswordLogin(t, service)
	metadata := TrustedDeviceMetadata{Name: "Replacement", Platform: "linux"}
	for _, failure := range []string{"password", "persistence", "random"} {
		password := "permanent admin password"
		random := service.random
		switch failure {
		case "password":
			password = "wrong password"
		case "persistence":
			repo.fail = true
		case "random":
			service.random = failingReader{}
		}
		got, err := service.EnrollTrustedDevice(t.Context(), login.Principal, password, metadata, first.ID)
		repo.fail = false
		service.random = random
		if err == nil || got.Secret != "" || got.ID != "" || len(repo.state.TrustedDevices) != 1 {
			t.Fatalf("failed %s replacement changed the credential: %v", failure, err)
		}
		if _, exists := repo.state.TrustedDevices[first.ID]; !exists {
			t.Fatal("failed replacement removed the prior device")
		}
		if failure == "persistence" {
			if _, err := service.Authenticate(t.Context(), admin.Token); !errors.Is(err, ErrDegraded) {
				t.Fatal("failed replacement persistence did not fail closed", err)
			}
			service, err = NewService(repo, Options{PasswordParams: service.hasher.params, Now: service.now})
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, err := service.Authenticate(t.Context(), admin.Token); err != nil {
			t.Fatal("failed replacement revoked the prior session", err)
		}
	}
	replacement, err := service.EnrollTrustedDevice(t.Context(), login.Principal, "permanent admin password", metadata, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	principal := devicePrincipal(t, service, login.Token)
	if err := service.ActivateTrustedDevice(t.Context(), principal, replacement.ID); err != nil {
		t.Fatal(err)
	}
	if replacement.ID == first.ID || replacement.Secret == first.Secret || len(repo.state.TrustedDevices) != 1 || principal.DeviceID != replacement.ID {
		t.Fatal("replacement failed to rotate and bind the device")
	}
	if _, err := service.AuthenticateTrustedDevice(t.Context(), first.ID, first.Secret, "192.0.2.7"); !errors.Is(err, ErrTrustedDeviceUntrusted) {
		t.Fatal("replaced credential still authenticates", err)
	}
	if _, err := service.Authenticate(t.Context(), admin.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("replacement retained a prior device session", err)
	}
	seenReplacement := false
	for _, event := range repo.events {
		if event.Kind == AuditTrustedDeviceReplace {
			seenReplacement = true
		}
	}
	if !seenReplacement || repo.events[len(repo.events)-1].Kind != AuditTrustedDeviceActivate {
		t.Fatal("replacement and activation were not audited")
	}
}

func TestTrustedDeviceConcurrentReplacementHasOneWinner(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	first := enrollDevice(t, service, admin.Principal)
	logins := []Credentials{devicePasswordLogin(t, service), devicePasswordLogin(t, service)}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, login := range logins {
		wait.Add(1)
		go func(principal Principal) {
			defer wait.Done()
			<-start
			_, err := service.EnrollTrustedDeviceAfterAuthentication(context.Background(), principal, TrustedDeviceMetadata{Name: "Replacement"}, first.ID)
			results <- err
		}(login.Principal)
	}
	close(start)
	wait.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrTrustedDeviceUntrusted) {
			t.Fatal(err)
		}
	}
	if successes != 1 || len(repo.state.TrustedDevices) != 1 {
		t.Fatalf("replacement winners = %d, devices = %d", successes, len(repo.state.TrustedDevices))
	}
}

func TestTrustedDeviceFormattingAndAuditRedaction(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	credential := enrollDevice(t, service, admin.Principal)
	device := repo.state.TrustedDevices[credential.ID]
	for _, value := range []any{credential, device, device.Metadata, device.Redacted()} {
		for _, format := range []string{"%s", "%v", "%+v", "%#v", "%q"} {
			formatted := fmt.Sprintf(format, value)
			for _, sensitive := range []string{credential.Secret, device.SecretHash, device.Metadata.Name, credential.ID} {
				if strings.Contains(formatted, sensitive) {
					t.Fatalf("%T formatting leaked sensitive data", value)
				}
			}
		}
	}
	for _, value := range []any{credential, device.Redacted()} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), credential.Secret) || strings.Contains(string(encoded), device.SecretHash) || strings.Contains(string(encoded), "secretHash") {
			t.Fatal("redacted JSON exposed device proof")
		}
	}
	if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, credential.Secret); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.Secret, "wrong-secret", credential.Secret); !errors.Is(err, ErrTrustedDeviceUntrusted) {
		t.Fatal(err)
	}
	if err := service.FlushDenials(t.Context()); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(repo.events)
	if err != nil {
		t.Fatal(err)
	}
	for _, sensitive := range []string{credential.Secret, device.SecretHash, device.Metadata.Name, "permanent admin password", admin.Token, admin.CSRFToken} {
		if strings.Contains(string(encoded), sensitive) {
			t.Fatal("audit exposed sensitive device or authentication data")
		}
	}
	seen := map[AuditKind]bool{}
	for _, event := range repo.events {
		if !ValidAuditKind(event.Kind) {
			t.Fatal("unknown audit kind")
		}
		seen[event.Kind] = true
	}
	for _, kind := range []AuditKind{AuditTrustedDeviceEnroll, AuditTrustedDeviceLogin, AuditTrustedDeviceDenied} {
		if !seen[kind] {
			t.Fatalf("missing %s audit event", kind)
		}
	}
	for i := 0; i < 2000; i++ {
		service.RecordDenial(AuditTrustedDeviceDenied, fmt.Sprintf("10.0.%d.%d", i/250, i%250))
	}
	if len(service.Denials()) > 129 {
		t.Fatal("device denial work is unbounded")
	}
	before := len(repo.events)
	if err := service.FlushDenials(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(repo.events) != before {
		t.Fatal("device denials bypassed audit flush rate limiting")
	}
}
