package desktopauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"routenest/internal/adminauth"
)

func TestManagerRoundTrip(t *testing.T) {
	keyring := newFakeKeyring()
	manager := NewManagerWithKeyring(keyring)
	assertStatus(t, manager.Inspect(), "missing", false)
	credential := testCredential(1)
	if err := manager.Store(credential); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, manager.Status(), "stored", true)
	values := keyring.snapshot()
	if len(values) != 1 {
		t.Fatalf("stored entries = %d, want exactly one", len(values))
	}
	var document struct {
		Version    int `json:"version"`
		Credential struct {
			ID        string    `json:"id"`
			Secret    string    `json:"secret"`
			ExpiresAt time.Time `json:"expiresAt"`
		} `json:"credential"`
		AutomaticSignIn bool `json:"automaticSignIn"`
	}
	if err := json.Unmarshal([]byte(values[keyringAddress{serviceName, accountName}]), &document); err != nil {
		t.Fatal("keyring entry is not JSON")
	}
	if document.Version != 1 || !document.AutomaticSignIn || document.Credential.ID != credential.ID ||
		document.Credential.Secret != credential.Secret || !document.Credential.ExpiresAt.Equal(credential.ExpiresAt) {
		t.Fatal("keyring entry does not contain the complete versioned credential")
	}
	if keyring.count("set") != 1 || keyring.count("get") != 3 {
		t.Fatal("store did not inspect existing storage and verify the write")
	}
	restarted := NewManagerWithKeyring(keyring)
	got, status, err := restarted.Credential()
	if err != nil {
		t.Fatal(err)
	}
	assertCredential(t, got, credential)
	assertStatus(t, status, "stored", true)
	assertStatus(t, restarted.Status(), "stored", true)
	keyring.assertAddress(t)
}

func TestManagerEnableDisablePersistsAndPreservesCredential(t *testing.T) {
	keyring := newFakeKeyring()
	manager := NewManagerWithKeyring(keyring)
	credential := testCredential(2)
	if err := manager.Store(credential); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, false, true, true, false} {
		before := keyring.count("set")
		previous := manager.Status().AutomaticSignIn
		if err := manager.SetAutoSignIn(enabled); err != nil {
			t.Fatal(err)
		}
		if previous == enabled && keyring.count("set") != before {
			t.Fatal("unchanged preference caused a keyring write")
		}
		assertStatus(t, manager.Status(), "stored", enabled)
		restarted := NewManagerWithKeyring(keyring)
		got, status, err := restarted.Credential()
		if err != nil {
			t.Fatal(err)
		}
		assertCredential(t, got, credential)
		assertStatus(t, status, "stored", enabled)
	}
	replacement := testCredential(3)
	if err := manager.Store(replacement); err != nil {
		t.Fatal(err)
	}
	got, status, err := manager.Credential()
	if err != nil {
		t.Fatal(err)
	}
	assertCredential(t, got, replacement)
	assertStatus(t, status, "stored", true)
	if len(keyring.snapshot()) != 1 {
		t.Fatal("replacing a credential created multiple entries")
	}
	keyring.assertAddress(t)
}

func TestManagerMissingIsAvailableWithoutWriting(t *testing.T) {
	for _, getErr := range []error{nil, fmt.Errorf("wrapped: %w", keyring.ErrNotFound)} {
		keyring := newFakeKeyring()
		keyring.getErr = getErr
		manager := NewManagerWithKeyring(keyring)
		assertStatus(t, manager.Inspect(), "missing", false)
		credential, status, err := manager.Credential()
		if !errors.Is(err, ErrMissing) || credential != (adminauth.TrustedDeviceCredential{}) {
			t.Fatal("missing credential was returned as usable")
		}
		assertStatus(t, status, "missing", false)
		for _, enabled := range []bool{true, false} {
			if err := manager.SetAutoSignIn(enabled); !errors.Is(err, ErrMissing) {
				t.Fatalf("missing preference change = %v", err)
			}
		}
		if keyring.count("set") != 0 || keyring.count("delete") != 0 {
			t.Fatal("reading missing storage or changing its preference mutated the keyring")
		}
		if err := manager.Remove(); err != nil {
			t.Fatal(err)
		}
		assertStatus(t, manager.Status(), "missing", false)
		keyring.assertAddress(t)
	}
}

func TestManagerStatusIsRedactedCachedSnapshot(t *testing.T) {
	keyring := newFakeKeyring()
	manager := NewManagerWithKeyring(keyring)
	assertStatus(t, manager.Status(), "unavailable", false)
	if keyring.count("get") != 0 {
		t.Fatal("constructor or cached status read the keyring")
	}
	if err := manager.Store(testCredential(4)); err != nil {
		t.Fatal(err)
	}
	reads := keyring.count("get")
	status := manager.Status()
	status.AutomaticSignIn = false
	status.State = "mutated"
	assertStatus(t, manager.Status(), "stored", true)
	if keyring.count("get") != reads {
		t.Fatal("cached status prompted another keyring access")
	}
	keyring.put("corrupt")
	assertStatus(t, manager.Status(), "stored", true)
	assertStatus(t, manager.Inspect(), "corrupt", false)
	assertStatus(t, manager.Status(), "corrupt", false)
}

func TestManagerRejectsInvalidCredentialsWithoutMutatingStorage(t *testing.T) {
	good := testCredential(5)
	for _, tc := range []struct {
		name   string
		mutate func(*adminauth.TrustedDeviceCredential)
	}{
		{"empty ID", func(c *adminauth.TrustedDeviceCredential) { c.ID = "" }},
		{"short ID", func(c *adminauth.TrustedDeviceCredential) { c.ID = "device-id" }},
		{"invalid ID", func(c *adminauth.TrustedDeviceCredential) { c.ID = strings.Repeat("!", 43) }},
		{"noncanonical ID", func(c *adminauth.TrustedDeviceCredential) { c.ID = strings.Repeat("A", 42) + "B" }},
		{"empty secret", func(c *adminauth.TrustedDeviceCredential) { c.Secret = "" }},
		{"short secret", func(c *adminauth.TrustedDeviceCredential) { c.Secret = "secret" }},
		{"invalid secret", func(c *adminauth.TrustedDeviceCredential) { c.Secret = strings.Repeat("!", 43) }},
		{"noncanonical secret", func(c *adminauth.TrustedDeviceCredential) { c.Secret = strings.Repeat("A", 42) + "B" }},
		{"padded secret", func(c *adminauth.TrustedDeviceCredential) { c.Secret += "=" }},
		{"empty expiry", func(c *adminauth.TrustedDeviceCredential) { c.ExpiresAt = time.Time{} }},
		{"invalid expiry", func(c *adminauth.TrustedDeviceCredential) { c.ExpiresAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyring := newFakeKeyring()
			manager := NewManagerWithKeyring(keyring)
			if err := manager.Store(good); err != nil {
				t.Fatal(err)
			}
			before, writes := keyring.snapshot(), keyring.count("set")
			invalid := good
			tc.mutate(&invalid)
			if err := manager.Store(invalid); !errors.Is(err, ErrInvalidCredential) {
				t.Fatalf("invalid credential error = %v", err)
			}
			if !reflect.DeepEqual(keyring.snapshot(), before) || keyring.count("set") != writes {
				t.Fatal("invalid credential changed existing storage")
			}
			assertStatus(t, manager.Status(), "stored", true)
		})
	}
}

func TestManagerExpiredCredentialIsStoredNotCorrupt(t *testing.T) {
	keyring := newFakeKeyring()
	credential := testCredential(6)
	credential.ExpiresAt = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	manager := NewManagerWithKeyring(keyring)
	if err := manager.Store(credential); err != nil {
		t.Fatal(err)
	}
	got, status, err := manager.Credential()
	if err != nil {
		t.Fatal(err)
	}
	assertCredential(t, got, credential)
	assertStatus(t, status, "stored", true)
}

func TestManagerUnavailableNeverUsesCachedCredential(t *testing.T) {
	keyring := newFakeKeyring()
	manager := NewManagerWithKeyring(keyring)
	credential := testCredential(7)
	if err := manager.Store(credential); err != nil {
		t.Fatal(err)
	}
	before := keyring.snapshot()
	keyring.getErr = errors.New("locked keyring " + credential.Secret)
	assertStatus(t, manager.Inspect(), "unavailable", false)
	got, status, err := manager.Credential()
	if !errors.Is(err, ErrUnavailable) || got != (adminauth.TrustedDeviceCredential{}) {
		t.Fatal("unavailable storage returned a credential")
	}
	assertStatus(t, status, "unavailable", false)
	if err := manager.Store(testCredential(8)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("store in unavailable keyring = %v", err)
	}
	if err := manager.SetAutoSignIn(false); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("preference in unavailable keyring = %v", err)
	}
	if !reflect.DeepEqual(keyring.snapshot(), before) || keyring.count("set") != 1 || keyring.count("delete") != 0 {
		t.Fatal("unavailable storage was overwritten or deleted")
	}
	keyring.getErr = nil
	got, status, err = manager.Credential()
	if err != nil {
		t.Fatal(err)
	}
	assertCredential(t, got, credential)
	assertStatus(t, status, "stored", true)
}

func TestManagerNilKeyringFailsClosed(t *testing.T) {
	manager := NewManagerWithKeyring(nil)
	assertStatus(t, manager.Inspect(), "unavailable", false)
	got, status, err := manager.Credential()
	if !errors.Is(err, ErrUnavailable) || got != (adminauth.TrustedDeviceCredential{}) {
		t.Fatal("nil keyring returned a credential")
	}
	assertStatus(t, status, "unavailable", false)
	for _, operation := range []func() error{
		func() error { return manager.Store(testCredential(9)) },
		func() error { return manager.SetAutoSignIn(true) },
		manager.Remove,
	} {
		if err := operation(); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("nil keyring operation = %v", err)
		}
	}
}

func TestProductionConstructorDoesNotAccessKeyring(t *testing.T) {
	manager := NewManager()
	if _, ok := manager.keyring.(systemKeyring); !ok {
		t.Fatal("production constructor did not use the OS keyring")
	}
	assertStatus(t, manager.Status(), "unavailable", false)
}

func TestManagerConcurrentOperations(t *testing.T) {
	keyring := newFakeKeyring()
	manager := NewManagerWithKeyring(keyring)
	credential := testCredential(10)
	if err := manager.Store(credential); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for iteration := 0; iteration < 40; iteration++ {
				switch (worker + iteration) % 7 {
				case 0:
					if err := manager.Store(credential); err != nil {
						t.Errorf("concurrent store: %v", err)
					}
				case 1:
					if err := manager.SetAutoSignIn(iteration%2 == 0); err != nil && !errors.Is(err, ErrMissing) {
						t.Errorf("concurrent preference: %v", err)
					}
				case 2:
					got, status, err := manager.Credential()
					if err == nil {
						if got.ID != credential.ID || got.Secret != credential.Secret || status.State != "stored" {
							t.Error("concurrent credential read returned inconsistent state")
						}
					} else if !errors.Is(err, ErrMissing) || status.State != "missing" || got != (adminauth.TrustedDeviceCredential{}) {
						t.Error("concurrent missing read returned inconsistent state")
					}
				case 3:
					status := manager.Inspect()
					if status.State != "stored" && status.State != "missing" {
						t.Errorf("concurrent inspect: %+v", status)
					}
				case 4:
					status := manager.Status()
					if status.State != "stored" && status.State != "missing" && status.State != "unavailable" {
						t.Errorf("concurrent status: %+v", status)
					}
					if status.State == "unavailable" && (status.Available || status.Stored || status.AutomaticSignIn) {
						t.Error("in-flight mutation returned an authorizing status")
					}
				case 5:
					if err := manager.Remove(); err != nil {
						t.Errorf("concurrent remove: %v", err)
					}
				case 6:
					_ = manager.Metadata()
					_ = manager.String()
				}
			}
		}(worker)
	}
	workers.Wait()
	if err := manager.Store(credential); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetAutoSignIn(true); err != nil {
		t.Fatal(err)
	}
	got, status, err := manager.Credential()
	if err != nil {
		t.Fatal(err)
	}
	assertCredential(t, got, credential)
	assertStatus(t, status, "stored", true)
	if len(keyring.snapshot()) != 1 {
		t.Fatal("concurrent operations did not retain exactly one credential")
	}
	keyring.assertAddress(t)
}
