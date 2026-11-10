package desktopauth

import (
	"bytes"
	"encoding/base64"
	"sync"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"routenest/internal/adminauth"
)

type keyringAddress struct {
	service string
	account string
}

type keyringCall struct {
	operation string
	address   keyringAddress
}

type fakeKeyring struct {
	mu                sync.Mutex
	values            map[keyringAddress]string
	calls             []keyringCall
	getErr            error
	setErr            error
	deleteErr         error
	transform         func(string) string
	skipSet           bool
	writeOnError      bool
	skipDelete        bool
	deleteOnError     bool
	afterSetGetErr    error
	afterDeleteGetErr error
}

func newFakeKeyring() *fakeKeyring {
	return &fakeKeyring{values: make(map[keyringAddress]string)}
}

func (f *fakeKeyring) Set(service, account, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	address := keyringAddress{service, account}
	f.calls = append(f.calls, keyringCall{"set", address})
	if f.setErr != nil && !f.writeOnError {
		return f.setErr
	}
	if !f.skipSet {
		if f.transform != nil {
			value = f.transform(value)
		}
		f.values[address] = value
	}
	f.getErr = f.afterSetGetErr
	return f.setErr
}

func (f *fakeKeyring) Get(service, account string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	address := keyringAddress{service, account}
	f.calls = append(f.calls, keyringCall{"get", address})
	value, ok := f.values[address]
	if f.getErr != nil {
		return value, f.getErr
	}
	if !ok {
		return "", keyring.ErrNotFound
	}
	return value, nil
}

func (f *fakeKeyring) Delete(service, account string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	address := keyringAddress{service, account}
	f.calls = append(f.calls, keyringCall{"delete", address})
	if f.deleteErr != nil && !f.deleteOnError {
		return f.deleteErr
	}
	if _, ok := f.values[address]; !ok {
		return keyring.ErrNotFound
	}
	if !f.skipDelete {
		delete(f.values, address)
	}
	f.getErr = f.afterDeleteGetErr
	return f.deleteErr
}

func (f *fakeKeyring) put(value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[keyringAddress{serviceName, accountName}] = value
}

func (f *fakeKeyring) snapshot() map[keyringAddress]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	values := make(map[keyringAddress]string, len(f.values))
	for address, value := range f.values {
		values[address] = value
	}
	return values
}

func (f *fakeKeyring) count(operation string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, call := range f.calls {
		if call.operation == operation {
			count++
		}
	}
	return count
}

func (f *fakeKeyring) assertAddress(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if call.address != (keyringAddress{"RouteNest", "desktop-trusted-device"}) {
			t.Fatal("keyring operation used an unexpected service or account")
		}
	}
}

func testCredential(seed byte) adminauth.TrustedDeviceCredential {
	return adminauth.TrustedDeviceCredential{
		ID:        base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{seed}, 32)),
		Secret:    base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{seed + 1}, 32)),
		ExpiresAt: time.Date(2027, 2, 1, 12, 30, 0, 123456789, time.UTC),
	}
}

func testRecord(t *testing.T, credential adminauth.TrustedDeviceCredential, enabled bool) string {
	t.Helper()
	value, err := encodeRecord(credentialRecord{recordVersion, credential, enabled})
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return value
}

func assertCredential(t *testing.T, got, want adminauth.TrustedDeviceCredential) {
	t.Helper()
	if got.ID != want.ID || got.Secret != want.Secret || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatal("credential did not round trip unchanged")
	}
}

func assertStatus(t *testing.T, status adminauth.DesktopCredentialStorageStatus, state string, enabled bool) {
	t.Helper()
	if status.State != state || status.Available != (state != "unavailable") ||
		status.Stored != (state == "stored") || status.AutomaticSignIn != enabled {
		t.Fatalf("status = %+v, want state %s, automatic sign-in %v", status, state, enabled)
	}
}
