package adminauth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTrustedDeviceActivationRequiresExactCurrentPrincipal(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	other := devicePasswordLogin(t, service)
	credential, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), admin.Principal, TrustedDeviceMetadata{Name: "Pending desktop"}, "")
	if err != nil {
		t.Fatal(err)
	}
	principal := devicePrincipal(t, service, admin.Token)
	before := repo.state.Revision
	for _, field := range []string{"stale binding", "other session", "roles", "method", "provider", "user", "username", "session", "csrf", "expiry"} {
		t.Run(field, func(t *testing.T) {
			forged := principal
			switch field {
			case "stale binding":
				forged = admin.Principal
			case "other session":
				forged = other.Principal
				forged.DeviceID = credential.ID
			case "roles":
				forged.Roles = []Role{RoleViewer}
			case "method":
				forged.Method = MethodOIDC
			case "provider":
				forged.Provider = "idp"
			case "user":
				forged.UserID = "other-user"
			case "username":
				forged.Username = "other-user"
			case "session":
				forged.SessionHash = hashToken("other session")
			case "csrf":
				forged.CSRFHash = hashToken("other csrf")
			case "expiry":
				forged.ExpiresAt = forged.ExpiresAt.Add(time.Hour)
			}
			if err := service.ActivateTrustedDevice(t.Context(), forged, credential.ID); !errors.Is(err, ErrUnauthorized) && !errors.Is(err, ErrForbidden) {
				t.Fatalf("forged principal activation = %v", err)
			}
			if repo.state.Revision != before || !repo.state.TrustedDevices[credential.ID].ActivatedAt.IsZero() {
				t.Fatal("denied activation changed durable authority")
			}
		})
	}
	if err := service.ActivateTrustedDevice(t.Context(), principal, credential.ID); err != nil {
		t.Fatal(err)
	}
	before = repo.state.Revision
	if err := service.ActivateTrustedDevice(t.Context(), principal, credential.ID); !errors.Is(err, ErrTrustedDeviceUntrusted) {
		t.Fatalf("activation replay = %v", err)
	}
	if repo.state.Revision != before {
		t.Fatal("activation replay changed durable authority")
	}
	if _, err := service.Authenticate(t.Context(), other.Token); err != nil {
		t.Fatal("denied activation locked down an unrelated session", err)
	}
}

func TestTrustedDeviceActivationRechecksAuthority(t *testing.T) {
	for _, action := range []string{"logout", "session revision", "user revision", "roles", "method", "binding", "recovery", "expired enrollment", "clock rollback"} {
		t.Run(action, func(t *testing.T) {
			service, repo, admin, now := trustedDeviceService(t)
			credential, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), admin.Principal, TrustedDeviceMetadata{Name: "Pending desktop"}, "")
			if err != nil {
				t.Fatal(err)
			}
			principal := devicePrincipal(t, service, admin.Token)
			before := len(repo.events)
			service.repo = &trustedDeviceRaceRepo{Repository: repo, before: func() {
				session := repo.state.Sessions[principal.SessionHash]
				user := repo.state.Users[principal.UserID]
				switch action {
				case "logout":
					delete(repo.state.Sessions, principal.SessionHash)
				case "session revision":
					session.UserRevision++
					repo.state.Sessions[principal.SessionHash] = session
				case "user revision":
					user.Revision++
					repo.state.Users[principal.UserID] = user
				case "roles":
					user.Roles = []Role{RoleViewer}
					repo.state.Users[principal.UserID] = user
				case "method":
					session.Method = MethodOIDC
					repo.state.Sessions[principal.SessionHash] = session
				case "binding":
					session.DeviceID = ""
					repo.state.Sessions[principal.SessionHash] = session
				case "recovery":
					repo.state.RecoveryPHC = user.PasswordPHC
				case "expired enrollment":
					*now = now.Add(trustedDeviceEnrollmentLifetime)
				case "clock rollback":
					*now = now.Add(-time.Second)
				}
			}}
			if err := service.ActivateTrustedDevice(t.Context(), principal, credential.ID); !errors.Is(err, ErrUnauthorized) && !errors.Is(err, ErrForbidden) {
				t.Fatalf("stale activation = %v", err)
			}
			if !repo.state.TrustedDevices[credential.ID].ActivatedAt.IsZero() || len(repo.events) != before {
				t.Fatal("stale activation was committed")
			}
			if err := service.check(); err != nil {
				t.Fatal("expected activation denial locked down the service", err)
			}
		})
	}
}

func TestTrustedDeviceActivationRequiresCurrentSecurityPermission(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	user, err := service.CreateLocalUser(t.Context(), admin.Principal, "operator", "operator password", []Role{RoleSecurityAdmin})
	if err != nil {
		t.Fatal(err)
	}
	login, err := service.Login(t.Context(), user.Username, "operator password", "192.0.2.80")
	if err != nil {
		t.Fatal(err)
	}
	credential, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), login.Principal, TrustedDeviceMetadata{Name: "Pending desktop"}, "")
	if err != nil {
		t.Fatal(err)
	}
	current := repo.state.Users[user.ID]
	current.Roles = []Role{RoleViewer}
	repo.state.Users[user.ID] = current
	principal := devicePrincipal(t, service, login.Token)
	if err := service.ActivateTrustedDevice(t.Context(), principal, credential.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("activation without security permission = %v", err)
	}
}

func TestTrustedDeviceActivationCannotResumeAfterEnrollmentSessionEnds(t *testing.T) {
	for _, action := range []string{"logout", "replace"} {
		t.Run(action, func(t *testing.T) {
			service, repo, admin, _ := trustedDeviceService(t)
			credential, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), admin.Principal, TrustedDeviceMetadata{Name: "Pending desktop"}, "")
			if err != nil {
				t.Fatal(err)
			}
			principal := devicePrincipal(t, service, admin.Token)
			if action == "logout" {
				err = service.Logout(t.Context(), principal)
			} else {
				_, err = service.EnrollTrustedDevice(t.Context(), principal, "permanent admin password", TrustedDeviceMetadata{Name: "Replacement"}, credential.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			before := repo.state.Revision
			if err := service.ActivateTrustedDevice(t.Context(), principal, credential.ID); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("stale activation after %s = %v", action, err)
			}
			fresh := devicePasswordLogin(t, service)
			fresh.Principal.DeviceID = credential.ID
			if err := service.ActivateTrustedDevice(t.Context(), fresh.Principal, credential.ID); !errors.Is(err, ErrUnauthorized) {
				t.Fatal("another password session activated an orphan enrollment", err)
			}
			if repo.state.Revision != before+1 {
				t.Fatal("stale activation changed durable authority")
			}
			if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.125"); !errors.Is(err, ErrTrustedDeviceUntrusted) {
				t.Fatal("orphan enrollment authenticated", err)
			}
		})
	}
}

func TestTrustedDevicePendingEnrollmentLifetime(t *testing.T) {
	for _, delay := range []time.Duration{trustedDeviceEnrollmentLifetime - time.Nanosecond, trustedDeviceEnrollmentLifetime, trustedDeviceEnrollmentLifetime + time.Second} {
		t.Run(delay.String(), func(t *testing.T) {
			service, repo, admin, now := trustedDeviceService(t)
			credential, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), admin.Principal, TrustedDeviceMetadata{Name: "Pending desktop"}, "")
			if err != nil {
				t.Fatal(err)
			}
			principal := devicePrincipal(t, service, admin.Token)
			*now = now.Add(delay)
			err = service.ActivateTrustedDevice(t.Context(), principal, credential.ID)
			if delay < trustedDeviceEnrollmentLifetime {
				if err != nil {
					t.Fatal("live enrollment was rejected", err)
				}
				return
			}
			if !errors.Is(err, ErrTrustedDeviceUntrusted) {
				t.Fatalf("expired activation = %v", err)
			}
			if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.81"); !errors.Is(err, ErrTrustedDeviceUntrusted) {
				t.Fatal("expired pending enrollment authenticated", err)
			}
			fresh := devicePasswordLogin(t, service)
			newDevice := enrollDevice(t, service, fresh.Principal)
			if len(repo.state.TrustedDevices) != 1 || repo.state.TrustedDevices[newDevice.ID].ID != newDevice.ID {
				t.Fatal("expired pending enrollment was not pruned")
			}
			if _, err := service.Authenticate(t.Context(), admin.Token); !errors.Is(err, ErrUnauthorized) {
				t.Fatal("pruned enrollment retained its bound session", err)
			}
		})
	}
}

func TestPasswordEnrollmentCannotCommitAfterPendingDeadline(t *testing.T) {
	service, repo, admin, now := trustedDeviceService(t)
	service.repo = &trustedDeviceRaceRepo{Repository: repo, before: func() {
		*now = now.Add(trustedDeviceEnrollmentLifetime)
	}}
	credential, err := service.EnrollTrustedDevice(t.Context(), admin.Principal, "permanent admin password", TrustedDeviceMetadata{Name: "Desktop"}, "")
	if !errors.Is(err, ErrUnauthorized) || credential.ID != "" || credential.Secret != "" || len(repo.state.TrustedDevices) != 0 {
		t.Fatal("password enrollment published an already expired pending device", err)
	}
}

func TestTrustedDeviceConcurrentActivationHasOneWinner(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	credential, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), admin.Principal, TrustedDeviceMetadata{Name: "Pending desktop"}, "")
	if err != nil {
		t.Fatal(err)
	}
	principal := devicePrincipal(t, service, admin.Token)
	before := len(repo.events)
	start := make(chan struct{})
	results := make(chan error, 8)
	var wait sync.WaitGroup
	for range cap(results) {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			results <- service.ActivateTrustedDevice(context.Background(), principal, credential.ID)
		}()
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
	if successes != 1 || len(repo.events) != before+1 {
		t.Fatalf("activation successes = %d, audit events = %d", successes, len(repo.events)-before)
	}
}

func TestGenericMutationCannotForgeFreshPasswordAuthentication(t *testing.T) {
	service, repo, admin, now := trustedDeviceService(t)
	*now = now.Add(trustedDeviceAuthenticationWindow)
	before := repo.state.Revision
	for _, action := range []string{"refresh", "insert"} {
		err := service.Mutate(t.Context(), admin.Principal, AuditUserChange, admin.Principal.UserID, func(state *State) error {
			session := state.Sessions[admin.Principal.SessionHash]
			session.IssuedAt = *now
			if action == "insert" {
				session.TokenHash = hashToken("forged password session")
			}
			state.Sessions[session.TokenHash] = session
			return nil
		})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("generic %s created fresh password authority: %v", action, err)
		}
	}
	if repo.state.Revision != before {
		t.Fatal("generic session forgery changed durable state")
	}
	if _, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), admin.Principal, TrustedDeviceMetadata{Name: "Desktop"}, ""); !errors.Is(err, ErrForbidden) {
		t.Fatal("stale session gained password enrollment authority", err)
	}
}

func TestTrustedDeviceViewOmitsUnknownLastUse(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	credential := enrollDevice(t, service, admin.Principal)
	device := repo.state.TrustedDevices[credential.ID]
	view := device.Redacted()
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if view.LastUsedAt != nil || strings.Contains(string(encoded), "lastUsedAt") {
		t.Fatal("unused device exposed a zero last-use timestamp")
	}
	if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.82"); err != nil {
		t.Fatal(err)
	}
	device = repo.state.TrustedDevices[credential.ID]
	view = device.Redacted()
	encoded, err = json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if view.LastUsedAt == nil || !view.LastUsedAt.Equal(device.LastUsedAt) || !strings.Contains(string(encoded), "lastUsedAt") {
		t.Fatal("used device did not expose its recorded last use")
	}
	*view.LastUsedAt = time.Time{}
	if repo.state.TrustedDevices[credential.ID].LastUsedAt.IsZero() {
		t.Fatal("view timestamp aliases persisted authority")
	}
}
