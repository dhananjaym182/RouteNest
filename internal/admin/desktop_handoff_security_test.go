package admin

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"routenest/internal/adminauth"
)

func TestCreateDesktopSessionHandoffRequiresLiveCredentials(t *testing.T) {
	_, runtime := newAdminAuthTestMux(t)
	credentials, err := runtime.service.CompleteBootstrap(t.Context(), "one time bootstrap secret", "DesktopHandoff8!")
	if err != nil {
		t.Fatal(err)
	}
	setDesktopSecurityRuntimeForTest(t, runtime)
	if _, err := CreateDesktopSessionHandoff(credentials); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"csrf hash", "session", "user", "csrf token", "missing csrf"} {
		forged := credentials
		switch field {
		case "csrf hash":
			forged.Principal.CSRFHash = "forged"
		case "session":
			forged.Principal.SessionHash = "forged"
		case "user":
			forged.Principal.UserID = "other-user"
		case "csrf token":
			forged.CSRFToken = strings.Repeat("x", 43)
		case "missing csrf":
			forged.CSRFToken = ""
		}
		if _, err := CreateDesktopSessionHandoff(forged); !errors.Is(err, adminauth.ErrUnauthorized) && !errors.Is(err, adminauth.ErrForbidden) {
			t.Fatalf("forged %s = %v", field, err)
		}
	}
	if err := runtime.service.Logout(t.Context(), credentials.Principal); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateDesktopSessionHandoff(credentials); !errors.Is(err, adminauth.ErrUnauthorized) {
		t.Fatalf("revoked credentials = %v", err)
	}
}

func TestDesktopHandoffRefreshesDeviceBindingBeforeCookieDelivery(t *testing.T) {
	_, runtime := newAdminAuthTestMux(t)
	setDesktopSecurityRuntimeForTest(t, runtime)
	credentials, err := runtime.service.CompleteBootstrap(t.Context(), "one time bootstrap secret", "DesktopHandoff8!")
	if err != nil {
		t.Fatal(err)
	}
	token, err := CreateDesktopSessionHandoff(credentials)
	if err != nil {
		t.Fatal(err)
	}
	device, err := EnrollDesktopDeviceAfterAuthentication(t.Context(), credentials, adminauth.TrustedDeviceMetadata{Name: "Desktop"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := ActivateDesktopDevice(t.Context(), credentials, device.ID); err != nil {
		t.Fatal(err)
	}
	console := desktopConsoleForTest(t, runtime)
	response := console.Request(t.Context(), DesktopConsoleRequest{Method: http.MethodPost, Path: "/admin/api/auth/desktop-handoff", Body: `{"token":"` + token + `"}`})
	if response.Status != http.StatusOK || response.CSRF != credentials.CSRFToken {
		t.Fatalf("stale handoff principal was not refreshed: %d %s", response.Status, response.Body)
	}
	refreshed, err := console.Credentials(t.Context())
	if err != nil || refreshed.Principal.DeviceID != device.ID {
		t.Fatalf("redeemed principal lost the device binding: %v", err)
	}
}

func TestDesktopDeviceActivationAndDiscardNeverForgeSessionBinding(t *testing.T) {
	for _, operation := range []string{"activate", "discard"} {
		t.Run(operation, func(t *testing.T) {
			_, runtime := newAdminAuthTestMux(t)
			setDesktopSecurityRuntimeForTest(t, runtime)
			credentials, err := runtime.service.CompleteBootstrap(t.Context(), "one time bootstrap secret", "DesktopHandoff8!")
			if err != nil {
				t.Fatal(err)
			}
			other, err := AuthenticateDesktopPassword(t.Context(), "DesktopHandoff8!")
			if err != nil {
				t.Fatal(err)
			}
			device, err := EnrollDesktopDeviceAfterAuthentication(t.Context(), credentials, adminauth.TrustedDeviceMetadata{Name: "Enrolling desktop"}, "")
			if err != nil {
				t.Fatal(err)
			}
			call := ActivateDesktopDevice
			if operation == "discard" {
				call = DiscardDesktopDevice
			}
			before, err := runtime.service.State(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, attempted := range []adminauth.Credentials{other, credentials} {
				if attempted.Token == other.Token {
					attempted.Principal.DeviceID = device.ID
				} else {
					attempted.CSRFToken = strings.Repeat("x", 43)
				}
				if err := call(t.Context(), attempted, device.ID); !errors.Is(err, adminauth.ErrUnauthorized) && !errors.Is(err, adminauth.ErrForbidden) {
					t.Fatalf("invalid %s credentials = %v", operation, err)
				}
			}
			after, err := runtime.service.State(t.Context())
			if err != nil || after.Revision != before.Revision {
				t.Fatalf("denied %s changed durable authority: %v", operation, err)
			}
			if err := call(t.Context(), credentials, device.ID); err != nil {
				t.Fatalf("valid %s with refreshed credentials = %v", operation, err)
			}
			refreshed, err := runtime.service.Authenticate(t.Context(), credentials.Token)
			if err != nil {
				t.Fatal(err)
			}
			if operation == "discard" && refreshed.DeviceID != "" || operation == "activate" && refreshed.DeviceID != device.ID {
				t.Fatalf("%s did not preserve the authoritative binding", operation)
			}
		})
	}
}

func TestDesktopHandoffConcurrentNativeRedemptionHasOneWinner(t *testing.T) {
	_, runtime := newAdminAuthTestMux(t)
	credentials, err := runtime.service.CompleteBootstrap(t.Context(), "one time bootstrap secret", "DesktopHandoff8!")
	if err != nil {
		t.Fatal(err)
	}
	token, err := runtime.desktopHandoffs.create(credentials)
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	results := make(chan DesktopConsoleResponse, 8)
	start := make(chan struct{})
	for range cap(results) {
		console := desktopConsoleForTest(t, runtime)
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			results <- console.Request(t.Context(), DesktopConsoleRequest{Method: http.MethodPost, Path: "/admin/api/auth/desktop-handoff", Body: `{"token":"` + token + `"}`})
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	winners := 0
	for response := range results {
		if response.Status == http.StatusOK && response.CSRF == credentials.CSRFToken {
			winners++
		} else if response.Status != http.StatusUnauthorized || response.CSRF != "" {
			t.Fatalf("unexpected concurrent handoff = %d %s", response.Status, response.Body)
		}
	}
	if winners != 1 {
		t.Fatalf("native handoff winners = %d", winners)
	}
}

func TestDesktopDeviceHandoffFailureRevokesOnlyTheNewSession(t *testing.T) {
	for _, failure := range []string{"entropy", "capacity", "cancelled caller", "missing store"} {
		t.Run(failure, func(t *testing.T) {
			_, runtime := newAdminAuthTestMux(t)
			setDesktopSecurityRuntimeForTest(t, runtime)
			credentials, err := runtime.service.CompleteBootstrap(t.Context(), "one time bootstrap secret", "DesktopHandoff8!")
			if err != nil {
				t.Fatal(err)
			}
			device, err := EnrollDesktopDeviceAfterAuthentication(t.Context(), credentials, adminauth.TrustedDeviceMetadata{Name: "Desktop"}, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := ActivateDesktopDevice(t.Context(), credentials, device.ID); err != nil {
				t.Fatal(err)
			}
			before, err := runtime.service.State(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			injected := errors.New("handoff entropy failed")
			want := injected
			switch failure {
			case "capacity":
				for i := range maxDesktopHandoffs {
					if _, err := runtime.desktopHandoffs.create(adminauth.Credentials{Token: fmt.Sprintf("pending-session-%d", i)}); err != nil {
						t.Fatal(err)
					}
				}
				want = adminauth.ErrRateLimited
			case "cancelled caller":
				runtime.desktopHandoffs.random = desktopHandoffCancellingReader{cancel: cancel, err: injected}
			case "missing store":
				runtime.desktopHandoffs = nil
				want = ErrDesktopHandoffUnavailable
			default:
				runtime.desktopHandoffs.random = iotest.ErrReader(injected)
			}
			returned, handoff, err := AuthenticateDesktopDevice(ctx, device.ID, device.Secret)
			if !errors.Is(err, want) || returned.Token != "" || returned.CSRFToken != "" || handoff != "" {
				t.Fatalf("failed handoff exposed credentials or lost its error: %v", err)
			}
			after, err := runtime.service.State(t.Context())
			if err != nil || len(after.Sessions) != len(before.Sessions) {
				t.Fatalf("failed handoff retained a new session: %v", err)
			}
			for hash, session := range before.Sessions {
				if after.Sessions[hash] != session {
					t.Fatal("failed handoff changed an existing session")
				}
			}
			if !after.TrustedDevices[device.ID].AutomaticSignIn {
				t.Fatal("handoff cleanup paused the trusted device")
			}
			events, err := runtime.store.AuditEvents(t.Context())
			if err != nil || len(events) < 2 || events[len(events)-2].Kind != adminauth.AuditTrustedDeviceLogin || events[len(events)-1].Kind != adminauth.AuditLogout {
				t.Fatalf("failed handoff did not audit login and cleanup: %v", err)
			}
		})
	}
}

type desktopHandoffCancellingReader struct {
	cancel context.CancelFunc
	err    error
}

func (reader desktopHandoffCancellingReader) Read([]byte) (int, error) {
	reader.cancel()
	return 0, reader.err
}

func TestDesktopDeviceHandoffFailureReportsCleanupFailure(t *testing.T) {
	_, runtime := newAdminAuthTestMux(t)
	setDesktopSecurityRuntimeForTest(t, runtime)
	credentials, err := runtime.service.CompleteBootstrap(t.Context(), "one time bootstrap secret", "DesktopHandoff8!")
	if err != nil {
		t.Fatal(err)
	}
	device, err := EnrollDesktopDeviceAfterAuthentication(t.Context(), credentials, adminauth.TrustedDeviceMetadata{Name: "Desktop"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := ActivateDesktopDevice(t.Context(), credentials, device.ID); err != nil {
		t.Fatal(err)
	}
	cleanupFailure := errors.New("logout persistence failed")
	repository := desktopHandoffLogoutFailureRepository{Repository: runtime.store, err: cleanupFailure}
	runtime.service, err = adminauth.NewService(repository, adminauth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	handoffFailure := errors.New("handoff entropy failed")
	runtime.desktopHandoffs.random = iotest.ErrReader(handoffFailure)
	returned, handoff, err := AuthenticateDesktopDevice(t.Context(), device.ID, device.Secret)
	if !errors.Is(err, handoffFailure) || !errors.Is(err, cleanupFailure) || returned.Token != "" || returned.CSRFToken != "" || handoff != "" {
		t.Fatalf("handoff cleanup failure was hidden: %v", err)
	}
	if _, err := runtime.service.Authenticate(t.Context(), credentials.Token); !errors.Is(err, adminauth.ErrDegraded) {
		t.Fatalf("cleanup persistence failure did not fail closed: %v", err)
	}
}

type desktopHandoffLogoutFailureRepository struct {
	adminauth.Repository
	err error
}

func (repository desktopHandoffLogoutFailureRepository) Apply(ctx context.Context, event adminauth.AuditEvent, mutate func(*adminauth.State) error) error {
	if event.Kind == adminauth.AuditLogout {
		return repository.err
	}
	return repository.Repository.Apply(ctx, event, mutate)
}

func TestDesktopHandoffStoreBoundsReplacesAndExpiresEntries(t *testing.T) {
	store := newDesktopHandoffStore()
	now := time.Now()
	store.now = func() time.Time { return now }
	first := adminauth.Credentials{Token: "first-session"}
	original, err := store.create(first)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := store.create(first)
	if err != nil || replacement == original || len(store.entries) != 1 {
		t.Fatalf("repeated handoff did not replace its session entry: %v", err)
	}
	if _, ok := store.consume(original); ok {
		t.Fatal("superseded handoff remained redeemable")
	}
	for i := range maxDesktopHandoffs - 1 {
		if _, err := store.create(adminauth.Credentials{Token: fmt.Sprintf("other-session-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if token, err := store.create(adminauth.Credentials{Token: "overflow"}); !errors.Is(err, adminauth.ErrRateLimited) || token != "" || len(store.entries) != maxDesktopHandoffs {
		t.Fatalf("handoff capacity was not enforced: %v", err)
	}
	atCapacity, err := store.create(first)
	if err != nil || len(store.entries) != maxDesktopHandoffs {
		t.Fatalf("session replacement was denied at capacity: %v", err)
	}
	if _, ok := store.consume(replacement); ok {
		t.Fatal("replacement at capacity retained the previous handoff")
	}
	injected := errors.New("handoff replacement entropy failed")
	store.random = iotest.ErrReader(injected)
	if token, err := store.create(first); !errors.Is(err, injected) || token != "" {
		t.Fatalf("replacement entropy failure = %v", err)
	}
	if _, ok := store.consume(atCapacity); !ok {
		t.Fatal("failed replacement discarded the valid pending handoff")
	}
	store.random = rand.Reader
	now = now.Add(desktopHandoffLifetime)
	if _, err := store.create(adminauth.Credentials{Token: "after-expiry"}); err != nil || len(store.entries) != 1 {
		t.Fatalf("expired handoffs did not release capacity: %v", err)
	}
}

func TestDesktopHandoffStoreRejectsTokenCollision(t *testing.T) {
	store := newDesktopHandoffStore()
	entropy := bytes.Repeat([]byte{1}, 64)
	store.random = bytes.NewReader(entropy)
	first := adminauth.Credentials{Token: "first-session"}
	token, err := store.create(first)
	if err != nil {
		t.Fatal(err)
	}
	if collision, err := store.create(adminauth.Credentials{Token: "other-session"}); !errors.Is(err, adminauth.ErrConflict) || collision != "" {
		t.Fatalf("token collision replaced a pending handoff: %v", err)
	}
	credentials, ok := store.consume(token)
	if !ok || credentials.Token != first.Token {
		t.Fatal("token collision changed the original handoff")
	}
}

func TestDesktopHandoffStoreConcurrentCreatesStayBounded(t *testing.T) {
	store := newDesktopHandoffStore()
	entropy := make([]byte, 32*maxDesktopHandoffs*2)
	for i := range maxDesktopHandoffs * 2 {
		entropy[i*32] = byte(i)
		entropy[i*32+1] = byte(i >> 8)
	}
	store.random = bytes.NewReader(entropy)
	var wait sync.WaitGroup
	results := make(chan error, maxDesktopHandoffs*2)
	start := make(chan struct{})
	for i := range cap(results) {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := store.create(adminauth.Credentials{Token: fmt.Sprintf("concurrent-session-%d", i)})
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	created := 0
	for err := range results {
		if err == nil {
			created++
		} else if !errors.Is(err, adminauth.ErrRateLimited) {
			t.Fatal(err)
		}
	}
	if len(store.entries) != maxDesktopHandoffs || created != maxDesktopHandoffs {
		t.Fatalf("concurrent handoff count = %d, stored = %d", created, len(store.entries))
	}
}

func TestDesktopSignOutWrapperPausesDeviceWithoutPassword(t *testing.T) {
	console, _, runtime := initializedDesktopConsoleForTest(t)
	setDesktopSecurityRuntimeForTest(t, runtime)
	desktopConsoleLoginForTest(t, console, "admin", desktopConsoleTestPassword)
	credentials, err := console.Credentials(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	device, err := EnrollDesktopDeviceAfterAuthentication(t.Context(), credentials, adminauth.TrustedDeviceMetadata{Name: "Desktop"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := ActivateDesktopDevice(t.Context(), credentials, device.ID); err != nil {
		t.Fatal(err)
	}
	if err := SignOutDesktopSession(t.Context(), credentials); err != nil {
		t.Fatal(err)
	}
	requireDesktopConsoleSignedOut(t, console)
	if _, err := runtime.service.AuthenticateTrustedDevice(t.Context(), device.ID, device.Secret, "127.0.0.1"); !errors.Is(err, adminauth.ErrTrustedDeviceAutoSignInDisabled) {
		t.Fatalf("signed-out desktop still authenticates automatically: %v", err)
	}
}
