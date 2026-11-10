package adminauth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTrustedDeviceRevocationAndDiscard(t *testing.T) {
	service, repo, admin, now := trustedDeviceService(t)
	first := enrollDevice(t, service, admin.Principal)
	passwordSession := devicePasswordLogin(t, service)
	second := enrollDevice(t, service, passwordSession.Principal)
	manager := devicePasswordLogin(t, service)
	firstSession, err := service.AuthenticateTrustedDevice(t.Context(), first.ID, first.Secret, "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RevokeTrustedDevice(t.Context(), manager.Principal, "wrong password", first.ID, "192.0.2.10"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("device revocation did not require the current password", err)
	}
	if len(repo.state.TrustedDevices) != 2 {
		t.Fatal("denied revocation changed device state")
	}
	if err := service.RevokeTrustedDevice(t.Context(), manager.Principal, "permanent admin password", first.ID, "192.0.2.10"); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{admin.Token, firstSession.Token} {
		if _, err := service.Authenticate(t.Context(), token); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("revocation retained an associated session", err)
		}
	}
	if _, err := service.AuthenticateTrustedDevice(t.Context(), first.ID, first.Secret, "192.0.2.11"); !errors.Is(err, ErrTrustedDeviceUntrusted) {
		t.Fatal("revoked credential still authenticates", err)
	}
	if _, err := service.Authenticate(t.Context(), passwordSession.Token); err != nil {
		t.Fatal("revocation affected another device", err)
	}
	if _, err := service.Authenticate(t.Context(), manager.Token); err != nil {
		t.Fatal("revocation affected an unbound password session", err)
	}
	if err := service.RevokeAllTrustedDevices(t.Context(), manager.Principal, "wrong password", "192.0.2.12"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("bulk revocation did not require the current password", err)
	}
	*now = now.Add(12 * time.Second)
	if err := service.RevokeAllTrustedDevices(t.Context(), manager.Principal, "permanent admin password", "192.0.2.12"); err != nil {
		t.Fatal(err)
	}
	if len(repo.state.TrustedDevices) != 0 || repo.state.TrustedDevices == nil {
		t.Fatal("bulk revocation did not clear the device map safely")
	}
	if _, err := service.AuthenticateTrustedDevice(t.Context(), second.ID, second.Secret, "192.0.2.11"); !errors.Is(err, ErrTrustedDeviceUntrusted) {
		t.Fatal("bulk-revoked credential still authenticates", err)
	}
	if _, err := service.Authenticate(t.Context(), passwordSession.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("bulk revocation retained a device session", err)
	}
	if _, err := service.Authenticate(t.Context(), manager.Token); err != nil {
		t.Fatal("bulk revocation affected an unbound password session", err)
	}
	third := enrollDevice(t, service, manager.Principal)
	if err := service.DiscardTrustedDevice(t.Context(), manager.Principal, third.ID); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("storage-failure cleanup accepted a stale device binding", err)
	}
	if err := service.DiscardTrustedDevice(t.Context(), devicePrincipal(t, service, manager.Token), third.ID); err != nil {
		t.Fatal("storage-failure cleanup rejected the current enrolling principal", err)
	}
	if len(repo.state.TrustedDevices) != 0 || devicePrincipal(t, service, manager.Token).DeviceID != "" {
		t.Fatal("storage-failure cleanup did not restore the password session")
	}
	if _, err := service.AuthenticateTrustedDevice(t.Context(), third.ID, third.Secret, "192.0.2.13"); !errors.Is(err, ErrTrustedDeviceUntrusted) {
		t.Fatal("discarded credential still authenticates", err)
	}
	seen := map[AuditKind]bool{}
	for _, event := range repo.events {
		seen[event.Kind] = true
	}
	for _, kind := range []AuditKind{AuditTrustedDeviceRevoke, AuditTrustedDeviceRevokeAll, AuditTrustedDeviceDiscard} {
		if !seen[kind] {
			t.Fatalf("missing %s audit event", kind)
		}
	}
}

func TestTrustedDeviceDiscardDoesNotUpgradeDeviceAuthentication(t *testing.T) {
	service, _, admin, _ := trustedDeviceService(t)
	credential := enrollDevice(t, service, admin.Principal)
	forged := devicePrincipal(t, service, admin.Token)
	forged.DeviceID = "forged-device-binding"
	if err := service.DiscardTrustedDevice(t.Context(), forged, credential.ID); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("discard accepted a forged device binding", err)
	}
	passwordSession := devicePasswordLogin(t, service)
	if err := service.DiscardTrustedDevice(t.Context(), passwordSession.Principal, credential.ID); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("unrelated session discarded a device without reauthentication", err)
	}
	deviceSession, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.14")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.DiscardTrustedDevice(t.Context(), deviceSession.Principal, credential.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(t.Context(), deviceSession.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("discard converted a device session to a password session", err)
	}
	if _, err := service.Authenticate(t.Context(), passwordSession.Token); err != nil {
		t.Fatal("discard revoked an unrelated password session", err)
	}
}

func TestTrustedDeviceManagementScopesReservedAndOtherAdministrators(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	first := enrollDevice(t, service, admin.Principal)
	principal := devicePrincipal(t, service, admin.Token)
	user, err := service.CreateLocalUser(t.Context(), principal, "security-operator", "operator password", []Role{RoleSecurityAdmin})
	if err != nil {
		t.Fatal(err)
	}
	login, err := service.Login(t.Context(), user.Username, "operator password", "192.0.2.15")
	if err != nil {
		t.Fatal(err)
	}
	second := enrollDevice(t, service, login.Principal)
	views, err := service.ListTrustedDevices(t.Context(), principal)
	if err != nil || len(views) != 2 {
		t.Fatal("reserved administrator could not list every user's device", err)
	}
	for _, view := range views {
		if view.Current != (view.ID == first.ID) {
			t.Fatal("global device list marked another user's device as current")
		}
	}
	operator := devicePrincipal(t, service, login.Token)
	views, err = service.ListTrustedDevices(t.Context(), operator)
	if err != nil || len(views) != 1 || views[0].ID != second.ID || !views[0].Current {
		t.Fatal("non-reserved administrator device list crossed user boundaries", err)
	}
	if err := service.RevokeTrustedDevice(t.Context(), operator, "operator password", first.ID, "192.0.2.16"); !errors.Is(err, ErrTrustedDeviceUntrusted) {
		t.Fatal("non-reserved administrator revocation crossed user boundaries", err)
	}
	if _, err := service.EnrollTrustedDevice(t.Context(), principal, "permanent admin password", TrustedDeviceMetadata{Name: "replacement"}, second.ID); !errors.Is(err, ErrTrustedDeviceUntrusted) {
		t.Fatal("replacement crossed user boundaries", err)
	}
	if err := service.RevokeAllTrustedDevices(t.Context(), operator, "operator password", "192.0.2.16"); err != nil {
		t.Fatal(err)
	}
	if len(repo.state.TrustedDevices) != 1 || repo.state.TrustedDevices[first.ID].UserID != BreakGlassID {
		t.Fatal("non-reserved administrator bulk revocation crossed user boundaries")
	}
	if event := repo.events[len(repo.events)-1]; event.Kind != AuditTrustedDeviceRevokeAll || event.TargetID != user.ID {
		t.Fatal("user-scoped revocation did not identify its scope in the audit")
	}
	if _, err := service.Authenticate(t.Context(), login.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("own bulk revocation retained the current device session", err)
	}
	login, err = service.Login(t.Context(), user.Username, "operator password", "192.0.2.15")
	if err != nil {
		t.Fatal(err)
	}
	second = enrollDevice(t, service, login.Principal)
	if err := service.RevokeAllTrustedDevices(t.Context(), principal, "operator password", "192.0.2.16"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("reserved administrator accepted another user's password", err)
	}
	if err := service.RevokeAllTrustedDevices(t.Context(), principal, "permanent admin password", "192.0.2.16"); err != nil {
		t.Fatal(err)
	}
	if len(repo.state.TrustedDevices) != 0 {
		t.Fatal("reserved administrator bulk revocation retained another user's device")
	}
	if event := repo.events[len(repo.events)-1]; event.Kind != AuditTrustedDeviceRevokeAll || event.TargetID != "all-trusted-devices" {
		t.Fatal("global revocation did not identify its scope in the audit")
	}
	for _, credential := range []TrustedDeviceCredential{first, second} {
		if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.17"); !errors.Is(err, ErrTrustedDeviceUntrusted) {
			t.Fatal("globally revoked device authenticated", err)
		}
	}
	for _, token := range []string{admin.Token, login.Token} {
		if _, err := service.Authenticate(t.Context(), token); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("globally revoked device retained its session", err)
		}
	}
}

func TestReservedAdministratorRevokesAnotherUsersDeviceWithOwnPassword(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	ownDevice := enrollDevice(t, service, admin.Principal)
	principal := devicePrincipal(t, service, admin.Token)
	user, err := service.CreateLocalUser(t.Context(), principal, "security-operator", "operator password", []Role{RoleSecurityAdmin})
	if err != nil {
		t.Fatal(err)
	}
	login, err := service.Login(t.Context(), user.Username, "operator password", "192.0.2.136")
	if err != nil {
		t.Fatal(err)
	}
	target := enrollDevice(t, service, login.Principal)
	automatic, err := service.AuthenticateTrustedDevice(t.Context(), target.ID, target.Secret, "192.0.2.137")
	if err != nil {
		t.Fatal(err)
	}
	passwordSession, err := service.Login(t.Context(), user.Username, "operator password", "192.0.2.138")
	if err != nil {
		t.Fatal(err)
	}
	before := repo.state.Revision
	for _, password := range []string{"wrong password", "operator password"} {
		if err := service.RevokeTrustedDevice(t.Context(), principal, password, target.ID, "192.0.2.139"); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("reserved administrator revocation did not require its own password", err)
		}
	}
	if repo.state.Revision != before || len(repo.state.TrustedDevices) != 2 {
		t.Fatal("denied cross-user revocation changed durable authority")
	}
	if err := service.RevokeTrustedDevice(t.Context(), principal, "permanent admin password", target.ID, "192.0.2.139"); err != nil {
		t.Fatal(err)
	}
	if len(repo.state.TrustedDevices) != 1 || repo.state.TrustedDevices[ownDevice.ID].UserID != BreakGlassID {
		t.Fatal("reserved administrator revoked more than the selected device")
	}
	if event := repo.events[len(repo.events)-1]; event.Kind != AuditTrustedDeviceRevoke || event.ActorID != BreakGlassID || event.TargetID != target.ID {
		t.Fatal("cross-user device revocation audit has incorrect actor or target")
	}
	for _, token := range []string{login.Token, automatic.Token} {
		if _, err := service.Authenticate(t.Context(), token); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("cross-user device revocation retained a bound session", err)
		}
	}
	for _, token := range []string{admin.Token, passwordSession.Token} {
		if _, err := service.Authenticate(t.Context(), token); err != nil {
			t.Fatal("cross-user device revocation affected an unrelated session", err)
		}
	}
	if _, err := service.AuthenticateTrustedDevice(t.Context(), target.ID, target.Secret, "192.0.2.137"); !errors.Is(err, ErrTrustedDeviceUntrusted) {
		t.Fatal("revoked cross-user credential still authenticates", err)
	}
}

func TestReservedCrossUserDeviceRevocationRechecksAdministrator(t *testing.T) {
	for _, action := range []string{"password", "logout"} {
		t.Run(action, func(t *testing.T) {
			service, repo, admin, _ := trustedDeviceService(t)
			user, err := service.CreateLocalUser(t.Context(), admin.Principal, "security-operator", "operator password", []Role{RoleSecurityAdmin})
			if err != nil {
				t.Fatal(err)
			}
			login, err := service.Login(t.Context(), user.Username, "operator password", "192.0.2.140")
			if err != nil {
				t.Fatal(err)
			}
			target := enrollDevice(t, service, login.Principal)
			before := len(repo.events)
			service.repo = &trustedDeviceRaceRepo{Repository: repo, before: func() {
				if action == "logout" {
					delete(repo.state.Sessions, admin.Principal.SessionHash)
				} else {
					current := repo.state.Users[BreakGlassID]
					current.PasswordPHC = service.hasher.dummy
					repo.state.Users[BreakGlassID] = current
				}
			}}
			if err := service.RevokeTrustedDevice(t.Context(), admin.Principal, "permanent admin password", target.ID, "192.0.2.141"); !errors.Is(err, ErrUnauthorized) {
				t.Fatal("stale reserved administrator revoked another user's device", err)
			}
			if len(repo.events) != before || repo.state.TrustedDevices[target.ID].ID != target.ID {
				t.Fatal("rejected cross-user revocation changed its target")
			}
			if err := service.check(); err != nil {
				t.Fatal("expected cross-user revocation conflict locked down the service", err)
			}
		})
	}
}

func TestTrustedDevicePasswordAndUserChangesRevoke(t *testing.T) {
	for _, action := range []string{"rotate", "reset", "roles", "disable", "delete"} {
		t.Run(action, func(t *testing.T) {
			service, repo, admin, _ := trustedDeviceService(t)
			adminDevice := enrollDevice(t, service, admin.Principal)
			adminPrincipal := devicePrincipal(t, service, admin.Token)
			user, err := service.CreateLocalUser(t.Context(), adminPrincipal, "operator", "operator password", []Role{RoleSecurityAdmin})
			if err != nil {
				t.Fatal(err)
			}
			login, err := service.Login(t.Context(), user.Username, "operator password", "192.0.2.18")
			if err != nil {
				t.Fatal(err)
			}
			credential := enrollDevice(t, service, login.Principal)
			deviceSession, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.27")
			if err != nil {
				t.Fatal(err)
			}
			passwordSession, err := service.Login(t.Context(), user.Username, "operator password", "192.0.2.28")
			if err != nil {
				t.Fatal(err)
			}
			pendingSession, err := service.Login(t.Context(), user.Username, "operator password", "192.0.2.29")
			if err != nil {
				t.Fatal(err)
			}
			pending, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), pendingSession.Principal, TrustedDeviceMetadata{Name: "Pending desktop"}, "")
			if err != nil {
				t.Fatal(err)
			}
			principal := devicePrincipal(t, service, login.Token)
			switch action {
			case "rotate":
				err = service.RotatePassword(t.Context(), principal, "operator password", "new operator password")
			case "reset":
				err = service.SetLocalPasswordAtRevision(t.Context(), adminPrincipal, user.ID, user.Revision, "new operator password")
			case "roles":
				_, err = service.UpdateUser(t.Context(), adminPrincipal, user.ID, user.Revision, []Role{RoleViewer}, false)
			case "disable":
				_, err = service.UpdateUser(t.Context(), adminPrincipal, user.ID, user.Revision, user.Roles, true)
			case "delete":
				err = service.DeleteLocalUser(t.Context(), adminPrincipal, user.ID, user.Revision)
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(repo.state.TrustedDevices) != 1 || repo.state.TrustedDevices[adminDevice.ID].UserID != BreakGlassID {
				t.Fatal("user change did not revoke only affected devices")
			}
			for _, device := range []TrustedDeviceCredential{credential, pending} {
				if _, err := service.AuthenticateTrustedDevice(t.Context(), device.ID, device.Secret, "192.0.2.19"); !errors.Is(err, ErrTrustedDeviceUntrusted) {
					t.Fatal("user change retained a device credential", err)
				}
			}
			for _, token := range []string{login.Token, deviceSession.Token, passwordSession.Token, pendingSession.Token} {
				if _, err := service.Authenticate(t.Context(), token); !errors.Is(err, ErrUnauthorized) {
					t.Fatal("user change retained an affected session", err)
				}
			}
			if _, err := service.AuthenticateTrustedDevice(t.Context(), adminDevice.ID, adminDevice.Secret, "192.0.2.19"); err != nil {
				t.Fatal("user change revoked another user's device", err)
			}
		})
	}
}

func TestRecoveryCodeRotationPreservesTrustedDevices(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	credential := enrollDevice(t, service, admin.Principal)
	principal := devicePrincipal(t, service, admin.Token)
	if _, err := service.IssueRecoveryCode(t.Context(), principal, "permanent admin password", "192.0.2.20"); err != nil {
		t.Fatal(err)
	}
	if len(repo.state.TrustedDevices) != 1 {
		t.Fatal("recovery-code rotation revoked an otherwise valid trusted device")
	}
	if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.20"); err != nil {
		t.Fatalf("trusted device failed after recovery-code rotation: %v", err)
	}
}

func TestTrustedDeviceRecoveryRevokesEveryUser(t *testing.T) {
	for _, action := range []string{"use", "offline"} {
		t.Run(action, func(t *testing.T) {
			service, repo, admin, _ := trustedDeviceService(t)
			code, err := service.IssueRecoveryCode(t.Context(), admin.Principal, "permanent admin password", "192.0.2.20")
			if err != nil {
				t.Fatal(err)
			}
			user, err := service.CreateLocalUser(t.Context(), admin.Principal, "operator", "operator password", []Role{RoleSecurityAdmin})
			if err != nil {
				t.Fatal(err)
			}
			login, err := service.Login(t.Context(), user.Username, "operator password", "192.0.2.21")
			if err != nil {
				t.Fatal(err)
			}
			credentials := []TrustedDeviceCredential{enrollDevice(t, service, admin.Principal), enrollDevice(t, service, login.Principal)}
			tokens := []string{admin.Token, login.Token}
			for _, credential := range credentials {
				deviceSession, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.31")
				if err != nil {
					t.Fatal(err)
				}
				tokens = append(tokens, deviceSession.Token)
			}
			passwordSession := devicePasswordLogin(t, service)
			tokens = append(tokens, passwordSession.Token)
			pendingSession, err := service.Login(t.Context(), user.Username, "operator password", "192.0.2.32")
			if err != nil {
				t.Fatal(err)
			}
			pending, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), pendingSession.Principal, TrustedDeviceMetadata{Name: "Pending desktop"}, "")
			if err != nil {
				t.Fatal(err)
			}
			tokens = append(tokens, pendingSession.Token)
			credentials = append(credentials, pending)
			switch action {
			case "use":
				_, err = service.RecoverWithCode(t.Context(), code, "recovered password", "192.0.2.20")
			case "offline":
				var secret string
				err = RecoverOffline(t.Context(), repo, Options{PasswordParams: service.hasher.params, Now: service.now, DeliverBootstrap: func(value string) error { secret = value; return nil }})
				if err == nil {
					_, err = service.CompleteBootstrap(t.Context(), secret, "recovered password", "192.0.2.22")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(repo.state.TrustedDevices) != 0 || repo.state.TrustedDevices == nil {
				t.Fatal("recovery retained trusted devices")
			}
			for _, credential := range credentials {
				if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.23"); !errors.Is(err, ErrTrustedDeviceUntrusted) {
					t.Fatal("recovery retained device proof", err)
				}
			}
			for _, token := range tokens {
				if _, err := service.Authenticate(t.Context(), token); !errors.Is(err, ErrUnauthorized) {
					t.Fatal("recovery retained an administrator session", err)
				}
			}
		})
	}
}

func TestTrustedDeviceAuthenticationRequiresDurableLastUse(t *testing.T) {
	service, repo, admin, now := trustedDeviceService(t)
	credential := enrollDevice(t, service, admin.Principal)
	*now = now.Add(time.Hour)
	before := CloneState(repo.state)
	repo.fail = true
	got, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.24")
	repo.fail = false
	if err == nil || got.Token != "" || got.CSRFToken != "" || !equalTrustedDevices(before.TrustedDevices, repo.state.TrustedDevices) || len(before.Sessions) != len(repo.state.Sessions) {
		t.Fatal("failed last-use persistence issued a session or mutated authority")
	}
	if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.24"); !errors.Is(err, ErrDegraded) {
		t.Fatal("failed last-use persistence did not fail closed", err)
	}
	service, err = NewService(repo, Options{PasswordParams: service.hasher.params, Now: service.now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.24"); err != nil {
		t.Fatal(err)
	}
	if !repo.state.TrustedDevices[credential.ID].LastUsedAt.Equal(*now) {
		t.Fatal("successful device authentication did not persist last use")
	}
}

func TestTrustedDeviceRevocationFailureLocksDown(t *testing.T) {
	for _, action := range []string{"one", "all", "discard"} {
		t.Run(action, func(t *testing.T) {
			service, repo, admin, _ := trustedDeviceService(t)
			credential := enrollDevice(t, service, admin.Principal)
			principal := devicePrincipal(t, service, admin.Token)
			repo.fail = true
			var err error
			switch action {
			case "one":
				err = service.RevokeTrustedDevice(t.Context(), principal, "permanent admin password", credential.ID, "192.0.2.25")
			case "all":
				err = service.RevokeAllTrustedDevices(t.Context(), principal, "permanent admin password", "192.0.2.25")
			case "discard":
				err = service.DiscardTrustedDevice(t.Context(), principal, credential.ID)
			}
			repo.fail = false
			if err == nil || len(repo.state.TrustedDevices) != 1 {
				t.Fatal("failed revocation was acknowledged or partially persisted")
			}
			if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.25"); !errors.Is(err, ErrDegraded) {
				t.Fatal("revocation failure did not lock device authentication", err)
			}
		})
	}
}

type trustedDeviceRaceRepo struct {
	Repository
	before func()
}

func (repo *trustedDeviceRaceRepo) Apply(ctx context.Context, event AuditEvent, mutate func(*State) error) error {
	if repo.before != nil {
		before := repo.before
		repo.before = nil
		before()
	}
	return repo.Repository.Apply(ctx, event, mutate)
}

func TestTrustedDeviceAuthenticationRechecksRevocationAndExpiry(t *testing.T) {
	for _, action := range []string{"revoke", "expire", "user revision", "pause logout"} {
		t.Run(action, func(t *testing.T) {
			service, repo, admin, now := trustedDeviceService(t)
			credential := enrollDevice(t, service, admin.Principal)
			principal := devicePrincipal(t, service, admin.Token)
			service.repo = &trustedDeviceRaceRepo{Repository: repo, before: func() {
				switch action {
				case "revoke":
					revokeTrustedDevice(&repo.state, credential.ID)
				case "expire":
					*now = credential.ExpiresAt
				case "pause logout":
					if err := service.LogoutWithDevicePause(t.Context(), principal); err != nil {
						t.Fatal(err)
					}
				case "user revision":
					u := repo.state.Users[BreakGlassID]
					u.Revision++
					repo.state.Users[BreakGlassID] = u
				}
			}}
			got, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.26")
			if err == nil || got.Token != "" || got.CSRFToken != "" {
				t.Fatal("device authentication committed with stale authority")
			}
			want := ErrTrustedDeviceUntrusted
			if action == "expire" {
				want = ErrTrustedDeviceExpired
			} else if action == "pause logout" {
				want = ErrTrustedDeviceAutoSignInDisabled
			}
			if !errors.Is(err, want) {
				t.Fatalf("authentication race = %v, want %v", err, want)
			}
		})
	}
}
