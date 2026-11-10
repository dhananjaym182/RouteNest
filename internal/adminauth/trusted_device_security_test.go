package adminauth

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTrustedDeviceRejectsFederatedEnrollment(t *testing.T) {
	service, _, admin, _ := trustedDeviceService(t)
	if err := service.Mutate(t.Context(), admin.Principal, AuditSSOChange, "idp", func(state *State) error {
		state.Providers["idp"] = ProviderState{ID: "idp", Type: MethodOIDC, Enabled: true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	credentials, err := service.IssueFederatedSession(t.Context(), "idp", 1, "subject", "federated-admin", []Role{RoleSecurityAdmin}, MethodOIDC, "192.0.2.50")
	if err != nil {
		t.Fatal(err)
	}
	metadata := TrustedDeviceMetadata{Name: "Desktop"}
	if _, err := service.EnrollTrustedDevice(t.Context(), credentials.Principal, "permanent admin password", metadata, ""); !errors.Is(err, ErrForbidden) {
		t.Fatal("federated session enrolled a local device", err)
	}
	if _, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), credentials.Principal, metadata, ""); !errors.Is(err, ErrForbidden) {
		t.Fatal("federated session bypassed password enrollment", err)
	}
	if _, err := service.ListTrustedDevices(t.Context(), credentials.Principal); !errors.Is(err, ErrForbidden) {
		t.Fatal("federated session listed local devices", err)
	}
}

func TestTrustedDeviceEnrollmentRechecksCurrentPrincipal(t *testing.T) {
	for _, action := range []string{"logout", "password", "revision", "freshness"} {
		t.Run(action, func(t *testing.T) {
			service, repo, admin, now := trustedDeviceService(t)
			service.repo = &trustedDeviceRaceRepo{Repository: repo, before: func() {
				switch action {
				case "logout":
					delete(repo.state.Sessions, admin.Principal.SessionHash)
				case "password":
					user := repo.state.Users[BreakGlassID]
					user.PasswordPHC = service.hasher.dummy
					repo.state.Users[BreakGlassID] = user
				case "revision":
					user := repo.state.Users[BreakGlassID]
					user.Revision++
					repo.state.Users[BreakGlassID] = user
				case "freshness":
					*now = now.Add(trustedDeviceAuthenticationWindow)
				}
			}}
			credential, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), admin.Principal, TrustedDeviceMetadata{Name: "Desktop"}, "")
			if err == nil || credential.ID != "" || credential.Secret != "" || len(repo.state.TrustedDevices) != 0 {
				t.Fatal("enrollment committed after authentication authority changed")
			}
		})
	}
}

func TestTrustedDeviceCurrentSessionReplacementAndExpiry(t *testing.T) {
	service, repo, admin, now := trustedDeviceService(t)
	first := enrollDevice(t, service, admin.Principal)
	principal := devicePrincipal(t, service, admin.Token)
	replacement, err := service.EnrollTrustedDevice(t.Context(), principal, "permanent admin password", TrustedDeviceMetadata{Name: "Replacement"}, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	principal = devicePrincipal(t, service, admin.Token)
	if err := service.ActivateTrustedDevice(t.Context(), principal, replacement.ID); err != nil {
		t.Fatal(err)
	}
	if principal.DeviceID != replacement.ID || len(repo.state.TrustedDevices) != 1 {
		t.Fatal("replacing the current device did not rebind its session")
	}
	if _, err := service.AuthenticateTrustedDevice(t.Context(), first.ID, first.Secret, "192.0.2.51"); !errors.Is(err, ErrTrustedDeviceUntrusted) {
		t.Fatal("replaced current credential still authenticates", err)
	}
	*now = replacement.ExpiresAt.Add(-time.Hour)
	credentials, err := service.AuthenticateTrustedDevice(t.Context(), replacement.ID, replacement.Secret, "192.0.2.51")
	if err != nil {
		t.Fatal(err)
	}
	if !credentials.ExpiresAt.Equal(now.Add(12 * time.Hour)) {
		t.Fatal("device session did not have the ordinary lifetime")
	}
	*now = replacement.ExpiresAt
	if _, err := service.Authenticate(t.Context(), credentials.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("device-bound session outlived trust expiry", err)
	}
	if err := service.validatePrincipal(t.Context(), credentials.Principal); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("expired device principal remained valid", err)
	}
	if err := validatePrincipalState(repo.state, credentials.Principal, *now); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("atomic principal validation ignored device expiry", err)
	}
}

func TestTrustedDeviceAuthenticationAndReauthenticationAreThrottled(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	credential := enrollDevice(t, service, admin.Principal)
	for i := 0; i < 5; i++ {
		if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, strings.Repeat("A", 43), "192.0.2.52"); !errors.Is(err, ErrTrustedDeviceUntrusted) {
			t.Fatal("device authentication throttled before its budget", err)
		}
	}
	if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.52"); !errors.Is(err, ErrRateLimited) {
		t.Fatal("device authentication exceeded its work budget", err)
	}
	principal := devicePrincipal(t, service, admin.Token)
	for i := 0; i < 5; i++ {
		if _, err := service.EnrollTrustedDevice(t.Context(), principal, "wrong password", TrustedDeviceMetadata{Name: "Desktop"}, credential.ID); !errors.Is(err, ErrUnauthorized) {
			t.Fatal(err)
		}
	}
	if _, err := service.EnrollTrustedDevice(t.Context(), principal, "permanent admin password", TrustedDeviceMetadata{Name: "Desktop"}, credential.ID); !errors.Is(err, ErrRateLimited) {
		t.Fatal("device password reauthentication exceeded its work budget", err)
	}
	if len(repo.state.TrustedDevices) != 1 || !repo.state.TrustedDevices[credential.ID].LastUsedAt.IsZero() {
		t.Fatal("throttled device authentication changed authority")
	}
}

func TestUnactivatedTrustedDeviceCannotAuthenticate(t *testing.T) {
	service, _, admin, _ := trustedDeviceService(t)
	credential, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), admin.Principal, TrustedDeviceMetadata{Name: "Uncommitted desktop"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.54"); !errors.Is(err, ErrTrustedDeviceUntrusted) {
		t.Fatalf("unactivated credential authenticated: %v", err)
	}
	principal := devicePrincipal(t, service, admin.Token)
	if err := service.ActivateTrustedDevice(t.Context(), principal, credential.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.54"); err != nil {
		t.Fatalf("activated credential rejected: %v", err)
	}
}

func TestTrustedDeviceAutomaticSignInRequiresPasswordOnlyToEnable(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	credential := enrollDevice(t, service, admin.Principal)
	principal := devicePrincipal(t, service, admin.Token)
	if !repo.state.TrustedDevices[credential.ID].AutomaticSignIn {
		t.Fatal("fresh device did not enable automatic sign-in")
	}
	if err := service.SetTrustedDeviceAutoSignIn(t.Context(), principal, "", credential.ID, false, "192.0.2.53"); err != nil {
		t.Fatal(err)
	}
	if repo.state.TrustedDevices[credential.ID].AutomaticSignIn {
		t.Fatal("automatic sign-in setting was not persisted")
	}
	if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.54"); !errors.Is(err, ErrTrustedDeviceAutoSignInDisabled) {
		t.Fatal("disabled automatic sign-in still authenticates", err)
	}
	if _, err := service.Authenticate(t.Context(), admin.Token); err != nil {
		t.Fatal("disabling future automatic sign-in revoked the current session", err)
	}
	if err := service.SetTrustedDeviceAutoSignIn(t.Context(), principal, "wrong password", credential.ID, true, "192.0.2.53"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("automatic sign-in was reenabled without the password", err)
	}
	if repo.state.TrustedDevices[credential.ID].AutomaticSignIn {
		t.Fatal("denied reenabling changed automatic sign-in")
	}
	if err := service.SetTrustedDeviceAutoSignIn(t.Context(), principal, "permanent admin password", credential.ID, true, "192.0.2.53"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.54"); err != nil {
		t.Fatal("reenabled automatic sign-in did not authenticate", err)
	}
	seen := false
	for _, event := range repo.events {
		if event.Kind == AuditTrustedDeviceAutoSignIn {
			seen = true
		}
	}
	if !seen {
		t.Fatal("automatic sign-in settings were not audited")
	}
}
