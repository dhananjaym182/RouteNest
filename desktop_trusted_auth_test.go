//go:build desktop

package main

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"routenest/internal/adminauth"
	"routenest/internal/desktopauth"
)

type desktopCredentialKeyring struct {
	mu             sync.Mutex
	value          string
	err            error
	setErr         error
	deleteErr      error
	ignoreSet      bool
	ignoreDelete   bool
	getCalls       int
	setCalls       int
	deleteCalls    int
	beforeSet      func()
	getStarted     chan struct{}
	getRelease     chan struct{}
	getStartedOnce sync.Once
}

func (keyringStore *desktopCredentialKeyring) Set(_, _, value string) error {
	keyringStore.mu.Lock()
	defer keyringStore.mu.Unlock()
	keyringStore.setCalls++
	if keyringStore.beforeSet != nil {
		keyringStore.beforeSet()
	}
	if keyringStore.err != nil {
		return keyringStore.err
	}
	if keyringStore.setErr != nil {
		return keyringStore.setErr
	}
	if !keyringStore.ignoreSet {
		keyringStore.value = value
	}
	return nil
}

func (keyringStore *desktopCredentialKeyring) Get(_, _ string) (string, error) {
	keyringStore.mu.Lock()
	defer keyringStore.mu.Unlock()
	keyringStore.getCalls++
	if keyringStore.getStarted != nil {
		keyringStore.getStartedOnce.Do(func() { close(keyringStore.getStarted) })
	}
	if keyringStore.getRelease != nil {
		<-keyringStore.getRelease
	}
	if keyringStore.err != nil {
		return "", keyringStore.err
	}
	if keyringStore.value == "" {
		return "", keyring.ErrNotFound
	}
	return keyringStore.value, nil
}

func (keyringStore *desktopCredentialKeyring) Delete(_, _ string) error {
	keyringStore.mu.Lock()
	defer keyringStore.mu.Unlock()
	keyringStore.deleteCalls++
	if keyringStore.err != nil {
		return keyringStore.err
	}
	if keyringStore.deleteErr != nil {
		return keyringStore.deleteErr
	}
	if keyringStore.value == "" {
		return keyring.ErrNotFound
	}
	if !keyringStore.ignoreDelete {
		keyringStore.value = ""
	}
	return nil
}

func TestDesktopStringPreservesPasswordBytes(t *testing.T) {
	password := "  password with spaces  "
	if got := desktopString(map[string]any{"password": password}, "password"); got != password {
		t.Fatalf("password = %q, want %q", got, password)
	}
}

func TestActivateStoredDesktopCredentialFailsClosed(t *testing.T) {
	keyringStore := &desktopCredentialKeyring{}
	manager := desktopauth.NewManagerWithKeyring(keyringStore)
	credential := adminauth.TrustedDeviceCredential{ID: token43("device"), Secret: token43("secret"), ExpiresAt: time.Now().Add(time.Hour)}
	activationErr := errors.New("activation failed")
	if err := activateStoredDesktopCredential(t.Context(), manager, credential, func(context.Context, string) error { return activationErr }); !errors.Is(err, activationErr) {
		t.Fatalf("activation error = %v", err)
	}
	if status := manager.Inspect(); status.Stored {
		t.Fatal("failed activation retained a usable OS credential")
	}
	if err := activateStoredDesktopCredential(t.Context(), manager, credential, func(context.Context, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if status := manager.Inspect(); !status.Stored || !status.AutomaticSignIn {
		t.Fatalf("successful activation status = %+v", status)
	}
}

func TestDesktopBridgeStatusDistinguishesCredentialStorageStates(t *testing.T) {
	store := &desktopCredentialKeyring{}
	runtime := &desktopRuntime{credentialStore: desktopauth.NewManagerWithKeyring(store)}
	runtime.onWindowStartup(desktopWindowTestContext())
	status, err := runtime.desktopStatus(t.Context(), nil)
	if err != nil || status["storage"] != "missing" || status["automaticSignIn"] != false {
		t.Fatalf("missing status = %#v, %v", status, err)
	}
	credential := adminauth.TrustedDeviceCredential{ID: token43("device"), Secret: token43("secret"), ExpiresAt: time.Now().Add(time.Hour)}
	if err := runtime.credentialStore.Store(credential); err != nil {
		t.Fatal(err)
	}
	status, err = runtime.desktopStatus(t.Context(), nil)
	if err != nil || status["storage"] != "stored" || status["automaticSignIn"] != false || status["deviceID"] != credential.ID || status["deviceStatus"] != "unavailable" {
		t.Fatalf("stored status = %#v, %v", status, err)
	}
	store.mu.Lock()
	store.value = "not json"
	store.mu.Unlock()
	status, err = runtime.desktopStatus(t.Context(), nil)
	if err != nil || status["storage"] != "corrupt" || status["deviceID"] != "" {
		t.Fatalf("corrupt status = %#v, %v", status, err)
	}
	store.mu.Lock()
	store.err = errors.New("vault unavailable")
	store.mu.Unlock()
	status, err = runtime.desktopStatus(t.Context(), nil)
	if err != nil || status["storage"] != "unavailable" {
		t.Fatalf("unavailable status = %#v, %v", status, err)
	}
}

func TestDesktopStateDoesNotTouchKeyringUntilTrustedWindowAndBoundsInspection(t *testing.T) {
	store := &desktopCredentialKeyring{getStarted: make(chan struct{}), getRelease: make(chan struct{})}
	defer close(store.getRelease)
	runtime := &desktopRuntime{credentialStore: desktopauth.NewManagerWithKeyringTimeout(store, 25*time.Millisecond)}
	runtime.bridge = runtime.newBridge(desktopSetupState{Mode: "login"}, nil)
	_ = runtime.bridge.State()
	store.mu.Lock()
	calls := store.getCalls
	store.mu.Unlock()
	if calls != 0 {
		t.Fatal("bridge construction or pre-window state touched keyring")
	}
	runtime.onWindowStartup(desktopWindowTestContext())
	start := time.Now()
	state := runtime.bridge.State()
	if state.CredentialStorage.State != "unavailable" || time.Since(start) > time.Second {
		t.Fatalf("blocked keyring state was not bounded: %+v", state.CredentialStorage)
	}
	select {
	case <-store.getStarted:
	default:
		t.Fatal("post-window state did not inspect keyring lazily")
	}
}

func token43(seed string) string {
	value := make([]byte, 32)
	copy(value, []byte(seed))
	return base64.RawURLEncoding.EncodeToString(value)
}
