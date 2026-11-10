package adminauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestReauthenticationEndpointsShareSessionBudget(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	before := repo.state.Revision
	attempts := []func() error{
		func() error {
			return service.RotatePassword(t.Context(), admin.Principal, "wrong password", "replacement password")
		},
		func() error {
			_, err := service.IssueRecoveryCode(t.Context(), admin.Principal, "wrong password", "192.0.2.101")
			return err
		},
		func() error {
			_, err := service.EnrollTrustedDevice(t.Context(), admin.Principal, "wrong password", TrustedDeviceMetadata{Name: "Desktop"}, "")
			return err
		},
		func() error {
			_, err := service.IssueRecoveryCode(t.Context(), admin.Principal, "wrong password", "192.0.2.102")
			return err
		},
		func() error {
			return service.RotatePassword(t.Context(), admin.Principal, "wrong password", "replacement password")
		},
	}
	for index, attempt := range attempts {
		if err := attempt(); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("attempt %d = %v", index, err)
		}
	}
	for _, attempt := range attempts[:3] {
		err := attempt()
		var limited *RateLimitError
		if !errors.As(err, &limited) || limited.RetryAfter <= 0 {
			t.Fatalf("endpoint bypassed shared session budget: %v", err)
		}
	}
	if repo.state.Revision != before {
		t.Fatal("throttled reauthentication changed durable authority")
	}
	if _, err := service.Authenticate(t.Context(), admin.Token); err != nil {
		t.Fatal("throttled reauthentication locked down the service", err)
	}
}

func TestDeviceManagementSharesReauthenticationBudget(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	credential := enrollDevice(t, service, admin.Principal)
	principal := devicePrincipal(t, service, admin.Token)
	attempts := []func() error{
		func() error {
			return service.RotatePassword(t.Context(), principal, "wrong password", "replacement password")
		},
		func() error {
			_, err := service.IssueRecoveryCode(t.Context(), principal, "wrong password", "192.0.2.103")
			return err
		},
		func() error {
			_, err := service.EnrollTrustedDevice(t.Context(), principal, "wrong password", TrustedDeviceMetadata{Name: "Replacement"}, credential.ID)
			return err
		},
		func() error {
			return service.RevokeTrustedDevice(t.Context(), principal, "wrong password", credential.ID, "192.0.2.104")
		},
		func() error {
			return service.RevokeAllTrustedDevices(t.Context(), principal, "wrong password", "192.0.2.105")
		},
	}
	for _, attempt := range attempts {
		if err := attempt(); !errors.Is(err, ErrUnauthorized) {
			t.Fatal(err)
		}
	}
	if err := service.SetTrustedDeviceAutoSignIn(t.Context(), principal, "permanent admin password", credential.ID, true, "192.0.2.106"); !errors.Is(err, ErrRateLimited) {
		t.Fatal("device reenabling bypassed shared password budget", err)
	}
	if err := service.SetTrustedDeviceAutoSignIn(t.Context(), principal, "", credential.ID, false, "192.0.2.106"); err != nil {
		t.Fatal("password budget prevented disabling future automatic sign-in", err)
	}
	if repo.state.TrustedDevices[credential.ID].AutomaticSignIn {
		t.Fatal("disabling automatic sign-in did not persist")
	}
	if err := service.LogoutWithDevicePause(t.Context(), principal); err != nil {
		t.Fatal("password budget prevented signing out", err)
	}
}

func TestReauthenticationUsesSharedCurrentSourceBudget(t *testing.T) {
	service, _, admin, _ := trustedDeviceService(t)
	other := devicePasswordLogin(t, service)
	third := devicePasswordLogin(t, service)
	for index := range 5 {
		principal := admin.Principal
		if index%2 != 0 {
			principal = other.Principal
		}
		if _, err := service.IssueRecoveryCode(t.Context(), principal, "wrong password", "192.0.2.107"); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("attempt %d = %v", index, err)
		}
	}
	if _, err := service.IssueRecoveryCode(t.Context(), third.Principal, "permanent admin password", "192.0.2.107"); !errors.Is(err, ErrRateLimited) {
		t.Fatal("new session bypassed the shared current-source budget", err)
	}
}

func TestPasswordRotationAndEnrollmentUseStoredSourceBudget(t *testing.T) {
	service, repo, admin, now := trustedDeviceService(t)
	first, err := service.Login(t.Context(), BreakGlassUsername, "permanent admin password", "2001:0db8:0:0::1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Login(t.Context(), BreakGlassUsername, "permanent admin password", "2001:db8::1")
	if err != nil {
		t.Fatal(err)
	}
	if repo.state.Sessions[first.Principal.SessionHash].Source != "2001:db8::1" || repo.state.Sessions[second.Principal.SessionHash].Source != "2001:db8::1" {
		t.Fatal("login did not retain a canonical trusted source")
	}
	service.throttle = NewThrottler()
	service.throttle.now = service.now
	for index := range 5 {
		if index%2 == 0 {
			err = service.RotatePassword(t.Context(), first.Principal, "wrong password", "replacement password")
		} else {
			_, err = service.EnrollTrustedDevice(t.Context(), second.Principal, "wrong password", TrustedDeviceMetadata{Name: "Desktop"}, "")
		}
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("attempt %d = %v", index, err)
		}
	}
	for _, credentials := range []Credentials{first, second} {
		if err := service.RotatePassword(t.Context(), credentials.Principal, "permanent admin password", "replacement password"); !errors.Is(err, ErrRateLimited) {
			t.Fatal("rotation bypassed shared stored-source budget", err)
		}
		if _, err := service.EnrollTrustedDevice(t.Context(), credentials.Principal, "permanent admin password", TrustedDeviceMetadata{Name: "Desktop"}, ""); !errors.Is(err, ErrRateLimited) {
			t.Fatal("enrollment bypassed shared stored-source budget", err)
		}
	}
	if _, err := service.IssueRecoveryCode(t.Context(), admin.Principal, "permanent admin password", "2001:0db8::1"); !errors.Is(err, ErrRateLimited) {
		t.Fatal("recovery issuance bypassed canonical source budget", err)
	}
	if err := service.RotatePassword(t.Context(), admin.Principal, "wrong password", "replacement password"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("source budget blocked an unrelated session", err)
	}
	*now = now.Add(12 * time.Second)
	if err := service.RotatePassword(t.Context(), first.Principal, "wrong password", "replacement password"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("source budget did not refill", err)
	}
}

func TestReauthenticationCancellationDoesNotConsumeBudget(t *testing.T) {
	service, _, admin, _ := trustedDeviceService(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := service.RotatePassword(ctx, admin.Principal, "permanent admin password", "replacement password"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := service.IssueRecoveryCode(ctx, admin.Principal, "permanent admin password", "192.0.2.108"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := service.EnrollTrustedDevice(ctx, admin.Principal, "permanent admin password", TrustedDeviceMetadata{Name: "Desktop"}, ""); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for range 5 {
		if err := service.RotatePassword(t.Context(), admin.Principal, "wrong password", "replacement password"); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("canceled reauthentication consumed password budget", err)
		}
	}
}

func TestSessionSourceIsOptionalAndValidated(t *testing.T) {
	service, repo, admin, _ := trustedDeviceService(t)
	session := repo.state.Sessions[admin.Principal.SessionHash]
	encoded, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"source"`) {
		t.Fatal("optional source changed legacy session representation")
	}
	for _, source := range []string{"127.000.000.001", "2001:0db8::1", "not-an-address", strings.Repeat("x", 1024)} {
		state := CloneState(repo.state)
		session.Source = source
		state.Sessions[session.TokenHash] = session
		if err := ValidateState(state); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("invalid session source %q accepted: %v", source, err)
		}
	}
	for index, source := range []string{"192.0.2.109", "2001:db8::1", "untrusted-source-text"} {
		login, err := service.Login(t.Context(), BreakGlassUsername, "permanent admin password", source)
		if err != nil {
			t.Fatal(err)
		}
		stored := repo.state.Sessions[login.Principal.SessionHash]
		if stored.Source != authenticationSource(source) {
			t.Fatalf("session %d stored unvalidated source", index)
		}
	}
}

func TestThrottleMultipleBudgetsAreAtomicAndBounded(t *testing.T) {
	throttle := NewThrottler()
	now := time.Now()
	throttle.now = func() time.Time { return now }
	for range 5 {
		if ok, _ := throttle.allow("source", "session", "source"); !ok {
			t.Fatal("duplicate source was charged multiple times")
		}
	}
	if throttle.global.tokens != 15 {
		t.Fatalf("global budget charged per key: %v", throttle.global.tokens)
	}
	if ok, _ := throttle.allow("fresh-session", "source"); ok {
		t.Fatal("depleted source accepted")
	}
	if throttle.sources["fresh-session"].tokens != throttle.capacity || throttle.global.tokens != 15 {
		t.Fatal("denied composite attempt consumed an unrelated budget")
	}
	throttle.maxSources = len(throttle.sources)
	for index := range 20 {
		if ok, _ := throttle.allow(fmt.Sprint(index), "source"); ok {
			t.Fatal("full source table was reset")
		}
	}
	if len(throttle.sources) != throttle.maxSources {
		t.Fatal("composite source table was unbounded")
	}
}
