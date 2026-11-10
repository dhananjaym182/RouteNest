package adminauth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestLogoutWithTrustedDevicePauseSupportsUnboundAndBoundSessions(t *testing.T) {
	for _, action := range []string{"unbound password", "another bound device", "empty ID", "already paused"} {
		t.Run(action, func(t *testing.T) {
			service, repo, admin, _ := trustedDeviceService(t)
			first := enrollDevice(t, service, admin.Principal)
			passwordSession := devicePasswordLogin(t, service)
			principal := passwordSession.Principal
			id := first.ID
			var second TrustedDeviceCredential
			if action == "another bound device" {
				second = enrollDevice(t, service, passwordSession.Principal)
				principal = devicePrincipal(t, service, passwordSession.Token)
			}
			if action == "empty ID" {
				principal = devicePrincipal(t, service, admin.Token)
				id = ""
			}
			if action == "already paused" {
				if err := service.SetTrustedDeviceAutoSignIn(t.Context(), principal, "", id, false, ""); err != nil {
					t.Fatal(err)
				}
			}
			before := CloneState(repo.state)
			events := len(repo.events)
			if err := service.LogoutWithTrustedDevicePause(t.Context(), principal, id); err != nil {
				t.Fatal(err)
			}
			if repo.state.Revision != before.Revision+1 || len(repo.events) != events+1 || repo.events[events].Kind != AuditLogout {
				t.Fatal("explicit-device sign-out was not one logout mutation")
			}
			if _, exists := repo.state.Sessions[principal.SessionHash]; exists || len(repo.state.Sessions) != len(before.Sessions)-1 {
				t.Fatal("explicit-device sign-out did not revoke only its own session")
			}
			for _, device := range []TrustedDeviceCredential{first, second} {
				if device.ID == "" {
					continue
				}
				stored := repo.state.TrustedDevices[device.ID]
				expected := before.TrustedDevices[device.ID]
				expected.AutomaticSignIn = false
				if stored != expected {
					t.Fatal("sign-out changed trust beyond pausing automatic sign-in")
				}
				if _, err := service.AuthenticateTrustedDevice(t.Context(), device.ID, device.Secret, "192.0.2.130"); !errors.Is(err, ErrTrustedDeviceAutoSignInDisabled) {
					t.Fatal("signed-out installation automatically authenticated", err)
				}
			}
			for hash, session := range repo.state.Sessions {
				if session != before.Sessions[hash] {
					t.Fatal("explicit-device sign-out changed an unrelated session")
				}
			}
		})
	}
}

func TestLogoutWithTrustedDevicePauseRejectsInvalidOwnershipAndAuthority(t *testing.T) {
	service, repo, admin, now := trustedDeviceService(t)
	first := enrollDevice(t, service, admin.Principal)
	manager := devicePasswordLogin(t, service)
	user, err := service.CreateLocalUser(t.Context(), manager.Principal, "operator", "operator password", []Role{RoleSecurityAdmin})
	if err != nil {
		t.Fatal(err)
	}
	operator, err := service.Login(t.Context(), user.Username, "operator password", "192.0.2.131")
	if err != nil {
		t.Fatal(err)
	}
	other := enrollDevice(t, service, operator.Principal)
	baseline := CloneState(repo.state)
	for _, action := range []string{"other user", "unknown", "malformed", "pending", "expired", "revision", "future activation", "forged csrf", "forged role"} {
		t.Run(action, func(t *testing.T) {
			repo.state = CloneState(baseline)
			principal := manager.Principal
			id := first.ID
			device := repo.state.TrustedDevices[id]
			expected := ErrTrustedDeviceUntrusted
			switch action {
			case "other user":
				id = other.ID
			case "unknown":
				id = strings.Repeat("A", 43)
			case "malformed":
				id = "invalid"
			case "pending":
				device.ActivatedAt = time.Time{}
				repo.state.TrustedDevices[id] = device
			case "expired":
				device.CreatedAt = now.Add(-time.Hour)
				device.ActivatedAt = device.CreatedAt
				device.ExpiresAt = *now
				repo.state.TrustedDevices[id] = device
				expected = ErrTrustedDeviceExpired
			case "revision":
				device.UserRevision++
				repo.state.TrustedDevices[id] = device
			case "future activation":
				device.ActivatedAt = now.Add(time.Minute)
				repo.state.TrustedDevices[id] = device
			case "forged csrf":
				principal.CSRFHash = hashToken("forged")
				expected = ErrUnauthorized
			case "forged role":
				principal.Roles = []Role{RoleViewer}
				expected = ErrUnauthorized
			}
			before, err := json.Marshal(repo.state)
			if err != nil {
				t.Fatal(err)
			}
			if err := service.LogoutWithTrustedDevicePause(t.Context(), principal, id); !errors.Is(err, expected) {
				t.Fatalf("invalid explicit-device sign-out = %v", err)
			}
			after, err := json.Marshal(repo.state)
			if err != nil || string(before) != string(after) {
				t.Fatal("denied explicit-device sign-out changed state", err)
			}
			if _, err := service.Authenticate(t.Context(), manager.Token); err != nil {
				t.Fatal("expected sign-out denial locked down its valid session", err)
			}
		})
	}
}

func TestLogoutWithTrustedDevicePauseRechecksTargetAndFailsClosed(t *testing.T) {
	for _, action := range []string{"revoke race", "expire race", "session race", "disk failure", "canceled"} {
		t.Run(action, func(t *testing.T) {
			service, repo, admin, now := trustedDeviceService(t)
			credential := enrollDevice(t, service, admin.Principal)
			manager := devicePasswordLogin(t, service)
			before := len(repo.events)
			ctx := t.Context()
			if action == "disk failure" {
				repo.fail = true
			} else if action == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			} else {
				service.repo = &trustedDeviceRaceRepo{Repository: repo, before: func() {
					switch action {
					case "revoke race":
						revokeTrustedDevice(&repo.state, credential.ID)
					case "expire race":
						device := repo.state.TrustedDevices[credential.ID]
						device.CreatedAt = now.Add(-time.Hour)
						device.ActivatedAt = device.CreatedAt
						device.ExpiresAt = *now
						repo.state.TrustedDevices[credential.ID] = device
					case "session race":
						delete(repo.state.Sessions, manager.Principal.SessionHash)
					}
				}}
			}
			if err := service.LogoutWithTrustedDevicePause(ctx, manager.Principal, credential.ID); err == nil {
				t.Fatal("invalidated sign-out was acknowledged")
			}
			repo.fail = false
			if len(repo.events) != before {
				t.Fatal("failed sign-out emitted a successful audit")
			}
			if action == "disk failure" {
				if !repo.state.TrustedDevices[credential.ID].AutomaticSignIn || repo.state.Sessions[manager.Principal.SessionHash].UserID == "" {
					t.Fatal("failed persistence partially applied explicit-device sign-out")
				}
				if _, err := service.Authenticate(t.Context(), manager.Token); !errors.Is(err, ErrDegraded) {
					t.Fatal("failed persistence did not fail closed", err)
				}
			} else if err := service.check(); err != nil {
				t.Fatal("expected denial locked down the service", err)
			}
		})
	}
}
