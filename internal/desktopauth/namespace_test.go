package desktopauth

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"routenest/internal/adminauth"
)

func TestInstallationNamespaceIsStableHashedAndBounded(t *testing.T) {
	seen := make(map[credentialSlot]bool)
	for _, installation := range []string{
		"installation-one",
		"installation-two",
		"Installation-one",
		" installation-one ",
		"/home/private user/RouteNest/data",
		"C:\\Users\\private user\\RouteNest\\data",
		"installation\x00with\nseparators",
		strings.Repeat("long-installation", 1000),
	} {
		manager := NewManagerForInstallation(installation)
		other := NewManagerForInstallation(installation)
		if _, ok := manager.keyring.(systemKeyring); !ok {
			t.Fatal("installation constructor did not select protected OS storage")
		}
		assertStatus(t, manager.Status(), "unavailable", false)
		if manager.slot != other.slot || seen[manager.slot] {
			t.Fatal("installation namespaces were unstable or collided")
		}
		seen[manager.slot] = true
		digest := sha256.Sum256([]byte(installation))
		if manager.slot.service != serviceName || manager.slot.account != accountName+":"+hex.EncodeToString(digest[:]) {
			t.Fatal("installation identifier was not hashed into the account namespace")
		}
		if len(manager.slot.account) != len(accountName)+1+64 || strings.Contains(manager.slot.account, installation) {
			t.Fatal("namespace exposed the installation identifier or grew with its length")
		}
		if manager.executor != systemExecutor || other.executor != systemExecutor {
			t.Fatal("installation constructors did not share the OS operation bound")
		}
	}
	if NewManager().executor != systemExecutor || NewManagerWithKeyring(systemKeyring{}).executor != systemExecutor {
		t.Fatal("default OS managers bypassed the shared executor")
	}
	if NewManagerWithKeyring(newFakeKeyring()).slot != (credentialSlot{serviceName, accountName}) {
		t.Fatal("test constructor changed its default keyring address")
	}
}

func TestMissingInstallationIdentifierFailsClosed(t *testing.T) {
	manager := NewManagerForInstallation("")
	if manager.keyring != nil {
		t.Fatal("missing installation identifier selected a shared OS slot")
	}
	assertStatus(t, manager.Inspect(), "unavailable", false)
	if _, _, err := manager.Credential(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing installation credential = %v", err)
	}
	if err := manager.Store(testCredential(80)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing installation store = %v", err)
	}
}

func TestInstallationCredentialsAndPreferencesAreIsolated(t *testing.T) {
	keyring := newFakeKeyring()
	executor := newKeyringExecutor()
	newInstallation := func(installation string) *Manager {
		return newManager(keyring, installationSlot(installation), defaultKeyringTimeout, executor)
	}
	first, second := newInstallation("installation-first"), newInstallation("installation-second")
	firstCredential, secondCredential := testCredential(81), testCredential(82)
	legacy := testRecord(t, testCredential(83), true)
	keyring.put(legacy)
	assertStatus(t, first.Inspect(), "missing", false)
	assertStatus(t, second.Inspect(), "missing", false)
	if err := first.Store(firstCredential); err != nil {
		t.Fatal(err)
	}
	if err := second.Store(secondCredential); err != nil {
		t.Fatal(err)
	}
	if err := first.SetAutoSignIn(false); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		installation string
		credential   adminauth.TrustedDeviceCredential
		enabled      bool
	}{
		{"installation-first", firstCredential, false},
		{"installation-second", secondCredential, true},
	} {
		credential, status, err := newInstallation(tc.installation).Credential()
		if err != nil {
			t.Fatal(err)
		}
		assertCredential(t, credential, tc.credential)
		assertStatus(t, status, "stored", tc.enabled)
	}
	if len(keyring.snapshot()) != 3 {
		t.Fatal("separate installations overwrote each other or the legacy slot")
	}
	if err := first.Remove(); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, first.Inspect(), "missing", false)
	credential, status, err := second.Credential()
	if err != nil {
		t.Fatal(err)
	}
	assertCredential(t, credential, secondCredential)
	assertStatus(t, status, "stored", true)
	if len(keyring.snapshot()) != 2 || keyring.snapshot()[keyringAddress{serviceName, accountName}] != legacy {
		t.Fatal("installation removal affected another namespace")
	}
}

func TestSharedExecutorRetainsQuarantineAcrossManagerRecreation(t *testing.T) {
	keyring := newBlockingKeyring(t, "set", 1)
	executor := newKeyringExecutor()
	slot := installationSlot("quarantined-installation")
	manager := newManager(keyring, slot, 40*time.Millisecond, executor)
	result := make(chan error, 1)
	go func() { result <- manager.Store(testCredential(84)) }()
	awaitSignal(t, keyring.started)
	if err := awaitError(t, result); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("blocked store = %v", err)
	}
	for attempt := 0; attempt < 10; attempt++ {
		recreated := newManager(keyring, slot, 40*time.Millisecond, executor)
		other := newManager(keyring, installationSlot("other-installation"), 40*time.Millisecond, executor)
		assertStatus(t, recreated.Inspect(), "unavailable", false)
		assertStatus(t, other.Inspect(), "unavailable", false)
	}
	if count, maximum := keyring.callCount(); count != 2 || maximum != 1 {
		t.Fatal("manager recreation bypassed the single blocked OS operation")
	}
	releaseLateCall(t, manager, keyring)
	recreated := newManager(keyring, slot, 40*time.Millisecond, executor)
	assertStatus(t, recreated.Inspect(), "unavailable", false)
	credential, status, err := recreated.Credential()
	if !errors.Is(err, ErrUnavailable) || credential != (adminauth.TrustedDeviceCredential{}) {
		t.Fatal("recreated manager authorized an unresolved late write")
	}
	assertStatus(t, status, "unavailable", false)
	other := newManager(keyring, installationSlot("other-installation"), 40*time.Millisecond, executor)
	if err := other.Store(testCredential(85)); err != nil {
		t.Fatalf("settled operation quarantined an unrelated installation: %v", err)
	}
	if err := recreated.Remove(); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, manager.Inspect(), "missing", false)
	assertStatus(t, other.Inspect(), "stored", true)
}
