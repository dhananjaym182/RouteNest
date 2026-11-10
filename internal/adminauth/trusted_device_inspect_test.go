package adminauth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestInspectTrustedDeviceIsReadOnlyAndAcceptsPausedTrust(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	credential := enrollDevice(t, service, admin.Principal)
	principal := devicePrincipal(t, service, admin.Token)
	for _, paused := range []bool{false, true} {
		if paused {
			if err := service.SetTrustedDeviceAutoSignIn(t.Context(), principal, "", credential.ID, false, ""); err != nil {
				t.Fatal(err)
			}
		}
		before, err := json.Marshal(repo.state)
		if err != nil {
			t.Fatal(err)
		}
		events := len(repo.events)
		denials := len(service.Denials())
		service.random = failingReader{}
		for range 8 {
			view, err := service.InspectTrustedDevice(t.Context(), credential.ID, credential.Secret)
			if err != nil || view.ID != credential.ID || view.UserID != principal.UserID || view.AutomaticSignIn == paused || view.LastUsedAt != nil || view.Current {
				t.Fatalf("credential inspection = %v, %v", view, err)
			}
			encoded, err := json.Marshal(view)
			if err != nil || strings.Contains(string(encoded), credential.Secret) || strings.Contains(string(encoded), repo.state.TrustedDevices[credential.ID].SecretHash) {
				t.Fatal("inspection exposed credential proof", err)
			}
		}
		service.random = service.hasher.random
		after, err := json.Marshal(repo.state)
		if err != nil || string(before) != string(after) || len(repo.events) != events || len(service.Denials()) != denials {
			t.Fatal("inspection mutated authority, sessions, timestamps, or audits", err)
		}
	}
}

func TestInspectTrustedDeviceRejectsInvalidProofAndStaleAuthority(t *testing.T) {
	service, repo, admin, now := trustedDeviceService(t)
	credential := enrollDevice(t, service, admin.Principal)
	baseline := CloneState(repo.state)
	for _, action := range []string{"wrong secret", "invalid secret", "unknown ID", "invalid ID", "revoked", "pending", "expired pending", "expired", "user revision", "disabled user", "recovery", "future activation", "future last use", "corrupt verifier"} {
		t.Run(action, func(t *testing.T) {
			repo.state = CloneState(baseline)
			id, secret := credential.ID, credential.Secret
			device := repo.state.TrustedDevices[id]
			expected := ErrTrustedDeviceUntrusted
			switch action {
			case "wrong secret":
				secret = strings.Repeat("A", 43)
			case "invalid secret":
				secret = "invalid"
			case "unknown ID":
				id = strings.Repeat("A", 43)
				expected = ErrTrustedDeviceNotFound
			case "invalid ID":
				id = "invalid"
			case "revoked":
				delete(repo.state.TrustedDevices, id)
				expected = ErrTrustedDeviceNotFound
			case "pending":
				device.ActivatedAt = time.Time{}
				repo.state.TrustedDevices[id] = device
			case "expired", "expired pending":
				device.CreatedAt = now.Add(-time.Hour)
				device.ActivatedAt = device.CreatedAt
				device.ExpiresAt = *now
				if action == "expired pending" {
					device.ActivatedAt = time.Time{}
				} else {
					expected = ErrTrustedDeviceExpired
				}
				repo.state.TrustedDevices[id] = device
			case "user revision":
				user := repo.state.Users[device.UserID]
				user.Revision++
				repo.state.Users[user.ID] = user
			case "disabled user":
				user := repo.state.Users[device.UserID]
				user.Disabled = true
				repo.state.Users[user.ID] = user
			case "recovery":
				repo.state.RecoveryPHC = service.hasher.dummy
			case "future activation":
				device.ActivatedAt = now.Add(time.Minute)
				repo.state.TrustedDevices[id] = device
			case "future last use":
				device.LastUsedAt = now.Add(time.Minute)
				repo.state.TrustedDevices[id] = device
			case "corrupt verifier":
				device.SecretHash = "invalid"
				repo.state.TrustedDevices[id] = device
			}
			before := repo.state.Revision
			view, err := service.InspectTrustedDevice(t.Context(), id, secret)
			if !errors.Is(err, expected) || repo.state.Revision != before {
				t.Fatalf("invalid credential inspection = %v, %v", view, err)
			}
			if errors.Is(err, ErrTrustedDeviceNotFound) != (action == "unknown ID" || action == "revoked") {
				t.Fatal("inspection confused an existing untrusted registration with an absent device", err)
			}
			if action == "expired" {
				if view.ID != credential.ID || view.UserID != admin.Principal.UserID || !view.ExpiresAt.Equal(*now) {
					t.Fatal("verified expired registration did not return its redacted view")
				}
			} else if view.ID != "" {
				t.Fatal("untrusted proof returned device details")
			}
			if action == "expired" {
				if invalid, err := service.InspectTrustedDevice(t.Context(), id, strings.Repeat("A", 43)); !errors.Is(err, ErrTrustedDeviceUntrusted) || invalid.ID != "" {
					t.Fatal("inspection disclosed expiry without proof", err)
				}
			}
		})
	}
}

func TestInspectTrustedDeviceDistinguishesAbsentRegistrationWithoutAuthenticationEnumeration(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	credential := enrollDevice(t, service, admin.Principal)
	before, err := json.Marshal(repo.state)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{strings.Repeat("A", 43), "invalid", ""} {
		view, err := service.InspectTrustedDevice(t.Context(), credential.ID, secret)
		if !errors.Is(err, ErrTrustedDeviceUntrusted) || errors.Is(err, ErrTrustedDeviceNotFound) || view.ID != "" || view.UserID != "" {
			t.Fatal("corrupt local proof was reported as a missing registration", err)
		}
	}
	after, err := json.Marshal(repo.state)
	if err != nil || string(before) != string(after) {
		t.Fatal("failed proof inspection changed the registration or its sessions", err)
	}
	if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.134"); err != nil {
		t.Fatal("inspection lost the valid server credential", err)
	}
	unknown := strings.Repeat("A", 43)
	view, err := service.InspectTrustedDevice(t.Context(), unknown, credential.Secret)
	if !errors.Is(err, ErrTrustedDeviceNotFound) || !errors.Is(err, ErrTrustedDeviceUntrusted) || !errors.Is(err, ErrUnauthorized) || view.ID != "" {
		t.Fatal("inspection did not distinguish an absent registration safely", err)
	}
	for _, id := range []string{unknown, credential.ID} {
		credentials, err := service.AuthenticateTrustedDevice(t.Context(), id, strings.Repeat("A", 43), "192.0.2.135")
		if err != ErrTrustedDeviceUntrusted || errors.Is(err, ErrTrustedDeviceNotFound) || credentials.Token != "" {
			t.Fatal("automatic authentication disclosed registration existence", err)
		}
	}
}

type inspectionErrorRepo struct {
	Repository
	err error
}

func (repo inspectionErrorRepo) Load(context.Context) (State, error) {
	return State{}, repo.err
}

func TestInspectTrustedDeviceFailsClosedOnUnavailableAuthority(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	credential := enrollDevice(t, service, admin.Principal)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.InspectTrustedDevice(ctx, credential.ID, credential.Secret); !errors.Is(err, context.Canceled) {
		t.Fatal("inspection ignored cancellation", err)
	}
	failure := errors.New("authority unavailable")
	service.repo = inspectionErrorRepo{Repository: repo, err: failure}
	if view, err := service.InspectTrustedDevice(t.Context(), credential.ID, credential.Secret); !errors.Is(err, failure) || view.ID != "" {
		t.Fatal("inspection ignored unavailable authority", err)
	}
	service.repo = repo
	service.lockdown()
	if _, err := service.InspectTrustedDevice(t.Context(), credential.ID, credential.Secret); !errors.Is(err, ErrDegraded) {
		t.Fatal("inspection ignored service lockdown", err)
	}
}
