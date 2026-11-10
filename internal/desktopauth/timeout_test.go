package desktopauth

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"routenest/internal/adminauth"
)

type blockingKeyring struct {
	base       *fakeKeyring
	operation  string
	occurrence int
	started    chan struct{}
	unblock    chan struct{}
	finished   chan struct{}
	once       sync.Once
	mu         sync.Mutex
	counts     map[string]int
	active     int
	maximum    int
}

func newBlockingKeyring(t *testing.T, operation string, occurrence int) *blockingKeyring {
	t.Helper()
	keyring := &blockingKeyring{
		base:       newFakeKeyring(),
		operation:  operation,
		occurrence: occurrence,
		started:    make(chan struct{}),
		unblock:    make(chan struct{}),
		finished:   make(chan struct{}),
		counts:     make(map[string]int),
	}
	t.Cleanup(func() {
		keyring.release()
		select {
		case <-keyring.started:
			awaitSignal(t, keyring.finished)
		default:
		}
	})
	return keyring
}

func (keyring *blockingKeyring) enter(operation string) func() {
	keyring.mu.Lock()
	keyring.active++
	if keyring.active > keyring.maximum {
		keyring.maximum = keyring.active
	}
	keyring.counts[operation]++
	blocked := operation == keyring.operation && keyring.counts[operation] == keyring.occurrence
	keyring.mu.Unlock()
	if blocked {
		close(keyring.started)
		<-keyring.unblock
	}
	return func() {
		keyring.mu.Lock()
		keyring.active--
		keyring.mu.Unlock()
		if blocked {
			close(keyring.finished)
		}
	}
}

func (keyring *blockingKeyring) Get(service, account string) (string, error) {
	leave := keyring.enter("get")
	defer leave()
	return keyring.base.Get(service, account)
}

func (keyring *blockingKeyring) Set(service, account, value string) error {
	leave := keyring.enter("set")
	defer leave()
	return keyring.base.Set(service, account, value)
}

func (keyring *blockingKeyring) Delete(service, account string) error {
	leave := keyring.enter("delete")
	defer leave()
	return keyring.base.Delete(service, account)
}

func (keyring *blockingKeyring) release() {
	keyring.once.Do(func() { close(keyring.unblock) })
}

func (keyring *blockingKeyring) callCount() (int, int) {
	keyring.mu.Lock()
	defer keyring.mu.Unlock()
	count := 0
	for _, calls := range keyring.counts {
		count += calls
	}
	return count, keyring.maximum
}

func awaitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal("keyring operation did not reach the expected state")
	}
}

func awaitError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("credential operation exceeded its deadline")
		return nil
	}
}

func releaseLateCall(t *testing.T, manager *Manager, keyring *blockingKeyring) {
	t.Helper()
	<-manager.executor.gate
	flight := manager.executor.flight
	manager.executor.release()
	keyring.release()
	awaitSignal(t, keyring.finished)
	if flight != nil {
		awaitSignal(t, flight.done)
	}
}

func TestManagerReadHangIsBoundedAndDoesNotQueueOSCalls(t *testing.T) {
	keyring := newBlockingKeyring(t, "get", 1)
	keyring.base.put(testRecord(t, testCredential(70), true))
	manager := NewManagerWithKeyringTimeout(keyring, 40*time.Millisecond)
	result := make(chan error, 1)
	go func() {
		credential, status, err := manager.Credential()
		if credential != (adminauth.TrustedDeviceCredential{}) || status.State != "unavailable" || status.AutomaticSignIn {
			result <- errors.New("blocked read returned an authorizing result")
			return
		}
		result <- err
	}()
	awaitSignal(t, keyring.started)
	statusResult := make(chan struct{})
	go func() {
		_ = manager.Status()
		_ = manager.Metadata()
		close(statusResult)
	}()
	awaitSignal(t, statusResult)
	if err := awaitError(t, result); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("blocked read = %v", err)
	}
	goroutines := runtime.NumGoroutine()
	for attempt := 0; attempt < 80; attempt++ {
		assertStatus(t, manager.Inspect(), "unavailable", false)
		credential, status, err := manager.Credential()
		if !errors.Is(err, ErrUnavailable) || credential != (adminauth.TrustedDeviceCredential{}) {
			t.Fatal("blocked keyring returned credential material")
		}
		assertStatus(t, status, "unavailable", false)
		for _, operation := range []func() error{
			func() error { return manager.Store(testCredential(71)) },
			func() error { return manager.SetAutoSignIn(false) },
			manager.Remove,
		} {
			if err := operation(); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("operation bypassed blocked keyring: %v", err)
			}
		}
	}
	if count, maximum := keyring.callCount(); count != 1 || maximum != 1 {
		t.Fatal("retries started more than one blocked OS operation")
	}
	if runtime.NumGoroutine() > goroutines+2 {
		t.Fatal("blocked keyring retries accumulated goroutines")
	}
	releaseLateCall(t, manager, keyring)
	assertStatus(t, manager.Status(), "unavailable", false)
	fresh := testCredential(72)
	keyring.base.put(testRecord(t, fresh, false))
	credential, status, err := manager.Credential()
	if err != nil {
		t.Fatal(err)
	}
	assertCredential(t, credential, fresh)
	assertStatus(t, status, "stored", false)
	if keyring.base.count("get") != 2 {
		t.Fatal("late read was reused without a fresh OS read")
	}
}

func TestManagerGateWaitHasDeadlineWithoutStartingKeyringCall(t *testing.T) {
	keyring := newFakeKeyring()
	manager := NewManagerWithKeyringTimeout(keyring, 30*time.Millisecond)
	if !manager.executor.acquire(context.Background()) {
		t.Fatal("could not hold transaction gate")
	}
	defer manager.executor.release()
	result := make(chan error, 1)
	go func() { result <- manager.Store(testCredential(73)) }()
	if err := awaitError(t, result); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("queued operation = %v", err)
	}
	assertStatus(t, manager.Status(), "unavailable", false)
	if len(keyring.calls) != 0 {
		t.Fatal("expired queued operation accessed the keyring")
	}
}

func TestManagerConcurrentCallersCannotMultiplyBlockedOSCalls(t *testing.T) {
	keyring := newBlockingKeyring(t, "get", 1)
	manager := NewManagerWithKeyringTimeout(keyring, 40*time.Millisecond)
	const callers = 48
	results := make(chan error, callers)
	for caller := 0; caller < callers; caller++ {
		go func() {
			_, _, err := manager.Credential()
			results <- err
		}()
	}
	awaitSignal(t, keyring.started)
	for caller := 0; caller < callers; caller++ {
		if err := awaitError(t, results); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("blocked concurrent read = %v", err)
		}
	}
	if count, maximum := keyring.callCount(); count != 1 || maximum != 1 {
		t.Fatal("concurrent callers multiplied OS operations")
	}
	releaseLateCall(t, manager, keyring)
}

func TestManagerLatePreflightReadCannotStartWrite(t *testing.T) {
	keyring := newBlockingKeyring(t, "get", 1)
	manager := NewManagerWithKeyringTimeout(keyring, 30*time.Millisecond)
	result := make(chan error, 1)
	go func() { result <- manager.Store(testCredential(74)) }()
	awaitSignal(t, keyring.started)
	if err := awaitError(t, result); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("store preflight = %v", err)
	}
	releaseLateCall(t, manager, keyring)
	if keyring.base.count("set") != 0 || len(keyring.base.snapshot()) != 0 {
		t.Fatal("expired store resumed after a late preflight read")
	}
	assertStatus(t, manager.Status(), "unavailable", false)
	assertStatus(t, manager.Inspect(), "missing", false)
}

func TestManagerLateMutationsRequireExplicitVerifiedRemoval(t *testing.T) {
	for _, tc := range []struct {
		name       string
		block      string
		occurrence int
		seed       bool
		enabled    bool
		operation  func(*Manager) error
	}{
		{"enrollment", "set", 1, false, false, func(m *Manager) error { return m.Store(testCredential(76)) }},
		{"reenrollment", "set", 1, true, false, func(m *Manager) error { return m.Store(testCredential(76)) }},
		{"enable", "set", 1, true, false, func(m *Manager) error { return m.SetAutoSignIn(true) }},
		{"sign out", "set", 1, true, true, func(m *Manager) error { return m.SetAutoSignIn(false) }},
		{"delete", "delete", 1, true, true, (*Manager).Remove},
		{"write verification", "get", 2, false, false, func(m *Manager) error { return m.Store(testCredential(76)) }},
		{"sign-out verification", "get", 2, true, true, func(m *Manager) error { return m.SetAutoSignIn(false) }},
		{"delete verification", "get", 1, true, true, (*Manager).Remove},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyring := newBlockingKeyring(t, tc.block, tc.occurrence)
			if tc.seed {
				keyring.base.put(testRecord(t, testCredential(75), tc.enabled))
			}
			manager := NewManagerWithKeyringTimeout(keyring, 40*time.Millisecond)
			result := make(chan error, 1)
			go func() { result <- tc.operation(manager) }()
			awaitSignal(t, keyring.started)
			if err := awaitError(t, result); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("late mutation = %v", err)
			}
			assertStatus(t, manager.Status(), "unavailable", false)
			if err := manager.Remove(); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("cleanup raced the late mutation: %v", err)
			}
			releaseLateCall(t, manager, keyring)
			calls, maximum := keyring.callCount()
			if maximum != 1 {
				t.Fatal("mutation and cleanup overlapped")
			}
			before := keyring.base.snapshot()
			for attempt := 0; attempt < 3; attempt++ {
				assertStatus(t, manager.Inspect(), "unavailable", false)
				credential, status, err := manager.Credential()
				if !errors.Is(err, ErrUnavailable) || credential != (adminauth.TrustedDeviceCredential{}) {
					t.Fatal("late mutation silently authorized a credential")
				}
				assertStatus(t, status, "unavailable", false)
				for _, operation := range []func() error{
					func() error { return manager.Store(testCredential(77)) },
					func() error { return manager.SetAutoSignIn(true) },
					func() error { return manager.SetAutoSignIn(false) },
				} {
					if err := operation(); !errors.Is(err, ErrUnavailable) {
						t.Fatalf("mutation bypassed reconciliation: %v", err)
					}
				}
			}
			if count, _ := keyring.callCount(); count != calls || len(keyring.base.snapshot()) != len(before) {
				t.Fatal("unresolved mutation prompted implicit reconciliation")
			}
			if err := manager.Remove(); err != nil {
				t.Fatalf("explicit reconciliation failed: %v", err)
			}
			assertStatus(t, manager.Status(), "missing", false)
			if len(keyring.base.snapshot()) != 0 {
				t.Fatal("reconciliation did not verify absence")
			}
			if err := manager.Store(testCredential(78)); err != nil {
				t.Fatal(err)
			}
			assertStatus(t, manager.Status(), "stored", true)
		})
	}
}

func TestManagerTimeoutConfiguration(t *testing.T) {
	for _, timeout := range []time.Duration{0, -time.Second} {
		manager := NewManagerWithKeyringTimeout(nil, timeout)
		if manager.timeout != defaultKeyringTimeout {
			t.Fatal("invalid timeout removed the production time bound")
		}
	}
	manager := NewManagerWithKeyringTimeout(nil, 17*time.Millisecond)
	if manager.timeout != 17*time.Millisecond {
		t.Fatal("test timeout was not retained")
	}
}
