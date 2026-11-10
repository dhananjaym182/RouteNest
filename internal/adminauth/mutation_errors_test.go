package adminauth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type mutationErrorRepo struct {
	Repository
	err error
}

func (repo mutationErrorRepo) Apply(context.Context, AuditEvent, func(*State) error) error {
	return repo.err
}

func TestExpectedMutationErrorsDoNotLockDown(t *testing.T) {
	for _, action := range []string{"logout", "pause logout", "session revoke", "device revoke", "all device revoke", "device discard", "disable automatic sign-in"} {
		t.Run(action, func(t *testing.T) {
			service, repo, admin, _ := trustedDeviceService(t)
			credential := enrollDevice(t, service, admin.Principal)
			principal := devicePrincipal(t, service, admin.Token)
			before := repo.state.Revision
			for _, expected := range []error{ErrUnauthorized, ErrForbidden, ErrConflict, &RateLimitError{RetryAfter: time.Minute}, ErrReplay, ErrInvalidState, context.Canceled, context.DeadlineExceeded} {
				service.repo = mutationErrorRepo{Repository: repo, err: fmt.Errorf("expected mutation failure: %w", expected)}
				service.throttle = NewThrottler()
				var err error
				switch action {
				case "logout":
					err = service.Logout(t.Context(), principal)
				case "pause logout":
					err = service.LogoutWithDevicePause(t.Context(), principal)
				case "session revoke":
					err = service.RevokeSession(t.Context(), principal, principal.SessionHash)
				case "device revoke":
					err = service.RevokeTrustedDevice(t.Context(), principal, "permanent admin password", credential.ID, "192.0.2.91")
				case "all device revoke":
					err = service.RevokeAllTrustedDevices(t.Context(), principal, "permanent admin password", "192.0.2.91")
				case "device discard":
					err = service.DiscardTrustedDevice(t.Context(), principal, credential.ID)
				case "disable automatic sign-in":
					err = service.SetTrustedDeviceAutoSignIn(t.Context(), principal, "", credential.ID, false, "192.0.2.91")
				}
				if !errors.Is(err, expected) {
					t.Fatalf("mutation error = %v, want %v", err, expected)
				}
				if repo.state.Revision != before {
					t.Fatal("rejected mutation changed durable state")
				}
				if _, err := service.Authenticate(t.Context(), admin.Token); err != nil {
					t.Fatal("expected mutation failure locked down a valid session", err)
				}
			}
		})
	}
}

func TestConcurrentLogoutDoesNotLockDownOtherSessions(t *testing.T) {
	for _, pause := range []bool{true, false} {
		t.Run(fmt.Sprint(pause), func(t *testing.T) {
			service, repo, admin, _ := trustedDeviceService(t)
			credential := enrollDevice(t, service, admin.Principal)
			principal := devicePrincipal(t, service, admin.Token)
			other := devicePasswordLogin(t, service)
			before := len(repo.events)
			start := make(chan struct{})
			results := make(chan error, 8)
			var wait sync.WaitGroup
			for range cap(results) {
				wait.Add(1)
				go func() {
					defer wait.Done()
					<-start
					if pause {
						results <- service.LogoutWithDevicePause(context.Background(), principal)
					} else {
						results <- service.Logout(context.Background(), principal)
					}
				}()
			}
			close(start)
			wait.Wait()
			close(results)
			successes := 0
			for err := range results {
				if err == nil {
					successes++
				} else if !errors.Is(err, ErrUnauthorized) {
					t.Fatal("concurrent logout failed unexpectedly", err)
				}
			}
			if successes != 1 || len(repo.events) != before+1 || repo.state.TrustedDevices[credential.ID].AutomaticSignIn == pause {
				t.Fatal("concurrent logout was not a single atomic mutation")
			}
			if _, err := service.Authenticate(t.Context(), other.Token); err != nil {
				t.Fatal("concurrent logout locked down an unrelated session", err)
			}
		})
	}
}

func TestExpectedMutationConflictPreservesServiceAndFlows(t *testing.T) {
	service, _, admin, _ := trustedDeviceService(t)
	user, err := service.CreateLocalUser(t.Context(), admin.Principal, "viewer", "viewer password", []Role{RoleViewer})
	if err != nil {
		t.Fatal(err)
	}
	handle, err := service.Flows().Create(AuthFlow{Provider: "idp", ProviderRevision: 1, ProviderFingerprint: "fingerprint", Type: MethodOIDC, Callback: "https://admin.example/callback"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateUser(t.Context(), admin.Principal, user.ID, user.Revision+1, user.Roles, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale user update = %v", err)
	}
	if _, err := service.CreateLocalUser(t.Context(), admin.Principal, user.Username, "another password", user.Roles); err == nil {
		t.Fatal("duplicate user was accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := service.LogoutWithDevicePause(ctx, admin.Principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled logout = %v", err)
	}
	if _, err := service.Authenticate(t.Context(), admin.Token); err != nil {
		t.Fatal("expected mutation failure locked down the administrator", err)
	}
	if _, err := service.Flows().Consume(handle, "idp", MethodOIDC); err != nil {
		t.Fatal("expected mutation failure cleared unrelated authentication flows", err)
	}
}

func TestPersistenceFailuresLockDownAllSecurityMutations(t *testing.T) {
	for _, action := range []string{"password rotate", "recovery issue", "recovery use", "device enroll", "device activate", "disable automatic sign-in", "login", "denial audit"} {
		t.Run(action, func(t *testing.T) {
			service, repo, admin, _ := trustedDeviceService(t)
			code, err := service.IssueRecoveryCode(t.Context(), admin.Principal, "permanent admin password", "192.0.2.92")
			if err != nil {
				t.Fatal(err)
			}
			credential, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), admin.Principal, TrustedDeviceMetadata{Name: "Desktop"}, "")
			if err != nil {
				t.Fatal(err)
			}
			principal := devicePrincipal(t, service, admin.Token)
			before := repo.state.Revision
			repo.fail = true
			switch action {
			case "password rotate":
				err = service.RotatePassword(t.Context(), principal, "permanent admin password", "replacement password")
			case "recovery issue":
				_, err = service.IssueRecoveryCode(t.Context(), principal, "permanent admin password", "192.0.2.93")
			case "recovery use":
				_, err = service.RecoverWithCode(t.Context(), code, "replacement password", "192.0.2.93")
			case "device enroll":
				_, err = service.EnrollTrustedDevice(t.Context(), principal, "permanent admin password", TrustedDeviceMetadata{Name: "Replacement"}, credential.ID)
			case "device activate":
				err = service.ActivateTrustedDevice(t.Context(), principal, credential.ID)
			case "disable automatic sign-in":
				err = service.SetTrustedDeviceAutoSignIn(t.Context(), principal, "", credential.ID, false, "192.0.2.93")
			case "login":
				_, err = service.Login(t.Context(), BreakGlassUsername, "permanent admin password", "192.0.2.93")
			case "denial audit":
				service.RecordDenial(AuditTrustedDeviceDenied, "192.0.2.93")
				err = service.FlushDenials(t.Context())
			}
			repo.fail = false
			if err == nil || repo.state.Revision != before {
				t.Fatal("persistence failure was acknowledged or partially committed")
			}
			if _, err := service.Authenticate(t.Context(), admin.Token); !errors.Is(err, ErrDegraded) {
				t.Fatal("persistence failure did not lock down the service", err)
			}
		})
	}
}

func TestPreparedMutationFailureOverridesExpectedError(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	service.repo = mutationErrorRepo{Repository: repo, err: errors.Join(ErrMutationPrepared, context.Canceled)}
	if err := service.LogoutWithDevicePause(t.Context(), admin.Principal); !errors.Is(err, ErrMutationPrepared) {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(t.Context(), admin.Token); !errors.Is(err, ErrDegraded) {
		t.Fatal("prepared cancellation did not fail closed", err)
	}
}
