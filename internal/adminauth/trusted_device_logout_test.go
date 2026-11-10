package adminauth

import (
	"errors"
	"testing"
)

func TestLogoutWithDevicePauseIsOneAtomicMutation(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	credential := enrollDevice(t, service, admin.Principal)
	deviceSession, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.83")
	if err != nil {
		t.Fatal(err)
	}
	passwordSession := devicePasswordLogin(t, service)
	before := CloneState(repo.state)
	events := len(repo.events)
	service.throttle.capacity = 0
	if err := service.LogoutWithDevicePause(t.Context(), deviceSession.Principal); err != nil {
		t.Fatal(err)
	}
	if repo.state.Revision != before.Revision+1 || len(repo.events) != events+1 || repo.events[events].Kind != AuditLogout {
		t.Fatal("sign-out did not use exactly one logout mutation and audit")
	}
	device := repo.state.TrustedDevices[credential.ID]
	if device.AutomaticSignIn || device.SecretHash != before.TrustedDevices[credential.ID].SecretHash || len(repo.state.Sessions) != len(before.Sessions)-1 {
		t.Fatal("sign-out did not pause trust and revoke only its current session")
	}
	if _, err := service.Authenticate(t.Context(), deviceSession.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("signed-out session remains usable", err)
	}
	for _, token := range []string{admin.Token, passwordSession.Token} {
		if _, err := service.Authenticate(t.Context(), token); err != nil {
			t.Fatal("sign-out revoked another session", err)
		}
	}
	service.throttle = NewThrottler()
	if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.83"); !errors.Is(err, ErrTrustedDeviceAutoSignInDisabled) {
		t.Fatal("sign-out allowed automatic sign-in", err)
	}
}

func TestLogoutWithDevicePauseDoesNotRequireSecurityPermission(t *testing.T) {
	for _, method := range []AuthMethod{MethodLocal, MethodOIDC} {
		t.Run(string(method), func(t *testing.T) {
			service, repo, admin, _ := trustedDeviceService(t)
			var credentials Credentials
			var err error
			if method == MethodLocal {
				user, err := service.CreateLocalUser(t.Context(), admin.Principal, "viewer", "viewer password", []Role{RoleViewer})
				if err != nil {
					t.Fatal(err)
				}
				credentials, err = service.Login(t.Context(), user.Username, "viewer password", "192.0.2.84")
			} else {
				if err := service.Mutate(t.Context(), admin.Principal, AuditSSOChange, "idp", func(state *State) error {
					state.Providers["idp"] = ProviderState{ID: "idp", Type: MethodOIDC, Enabled: true}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				credentials, err = service.IssueFederatedSession(t.Context(), "idp", 1, "viewer", "federated-viewer", []Role{RoleViewer}, MethodOIDC, "192.0.2.84")
			}
			if err != nil {
				t.Fatal(err)
			}
			before := len(repo.events)
			if err := service.LogoutWithDevicePause(t.Context(), credentials.Principal); err != nil {
				t.Fatal(err)
			}
			if len(repo.events) != before+1 || repo.events[before].Kind != AuditLogout || len(repo.state.TrustedDevices) != 0 {
				t.Fatal("unbound sign-out changed trust or used multiple audits")
			}
			if _, err := service.Authenticate(t.Context(), credentials.Token); !errors.Is(err, ErrUnauthorized) {
				t.Fatal("unbound sign-out did not revoke its session", err)
			}
		})
	}
}

func TestLogoutWithDevicePauseFailureIsAtomicAndFailClosed(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	credential := enrollDevice(t, service, admin.Principal)
	principal := devicePrincipal(t, service, admin.Token)
	before := CloneState(repo.state)
	events := len(repo.events)
	repo.fail = true
	err := service.LogoutWithDevicePause(t.Context(), principal)
	repo.fail = false
	if err == nil || !equalTrustedDevices(before.TrustedDevices, repo.state.TrustedDevices) || len(repo.state.Sessions) != len(before.Sessions) || repo.state.Revision != before.Revision || len(repo.events) != events {
		t.Fatal("failed sign-out partially changed durable authority")
	}
	if _, err := service.Authenticate(t.Context(), admin.Token); !errors.Is(err, ErrDegraded) {
		t.Fatal("failed durable sign-out did not lock down sessions", err)
	}
	if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.85"); !errors.Is(err, ErrDegraded) {
		t.Fatal("failed durable sign-out did not lock down automatic sign-in", err)
	}
}

func TestLogoutWithDevicePauseRejectsForgedPrincipal(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	credential := enrollDevice(t, service, admin.Principal)
	principal := devicePrincipal(t, service, admin.Token)
	before := repo.state.Revision
	for _, field := range []string{"binding", "csrf", "roles"} {
		forged := principal
		switch field {
		case "binding":
			forged.DeviceID = ""
		case "csrf":
			forged.CSRFHash = hashToken("wrong csrf")
		case "roles":
			forged.Roles = []Role{RoleViewer}
		}
		if err := service.LogoutWithDevicePause(t.Context(), forged); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("forged %s signed out a device: %v", field, err)
		}
	}
	if repo.state.Revision != before || !repo.state.TrustedDevices[credential.ID].AutomaticSignIn {
		t.Fatal("denied sign-out changed durable authority")
	}
	if _, err := service.Authenticate(t.Context(), admin.Token); err != nil {
		t.Fatal("denied sign-out ended the valid session", err)
	}
}

func TestLogoutLocksWithoutPausingRegisteredDevice(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	credential := enrollDevice(t, service, admin.Principal)
	principal := devicePrincipal(t, service, admin.Token)
	before := CloneState(repo.state)
	if err := service.Logout(t.Context(), principal); err != nil {
		t.Fatal(err)
	}
	if !equalTrustedDevices(before.TrustedDevices, repo.state.TrustedDevices) {
		t.Fatal("ordinary lock changed registered device trust")
	}
	if _, err := service.Authenticate(t.Context(), admin.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("ordinary lock retained its session", err)
	}
	if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.86"); err != nil {
		t.Fatal("ordinary lock disabled automatic sign-in", err)
	}
}

func TestRevokeCurrentTrustedDeviceEndsItsSessions(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	credential := enrollDevice(t, service, admin.Principal)
	credentials, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.87")
	if err != nil {
		t.Fatal(err)
	}
	other := devicePasswordLogin(t, service)
	if err := service.RevokeTrustedDevice(t.Context(), credentials.Principal, "permanent admin password", credential.ID, "192.0.2.87"); err != nil {
		t.Fatal(err)
	}
	if len(repo.state.TrustedDevices) != 0 {
		t.Fatal("current device revocation retained trust")
	}
	for _, token := range []string{admin.Token, credentials.Token} {
		if _, err := service.Authenticate(t.Context(), token); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("current device revocation retained a bound session", err)
		}
	}
	if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.88"); !errors.Is(err, ErrTrustedDeviceUntrusted) {
		t.Fatal("current device revocation allowed automatic sign-in", err)
	}
	if _, err := service.Authenticate(t.Context(), other.Token); err != nil {
		t.Fatal("current device revocation affected an unrelated session", err)
	}
}
