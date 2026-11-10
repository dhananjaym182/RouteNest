package desktopauth

import "testing"

func TestSignOutPreferencePersistsUntilExplicitEnrollment(t *testing.T) {
	keyring := newFakeKeyring()
	manager := NewManagerWithKeyring(keyring)
	credential := testCredential(60)
	if err := manager.Store(credential); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetAutoSignIn(false); err != nil {
		t.Fatal(err)
	}
	for restart := 0; restart < 3; restart++ {
		manager = NewManagerWithKeyring(keyring)
		assertStatus(t, manager.Inspect(), "stored", false)
		got, status, err := manager.Credential()
		if err != nil {
			t.Fatal(err)
		}
		assertCredential(t, got, credential)
		assertStatus(t, status, "stored", false)
	}
	if keyring.count("set") != 2 || keyring.count("delete") != 0 {
		t.Fatal("sign-out or restart replaced the credential")
	}
	replacement := testCredential(61)
	if err := manager.Store(replacement); err != nil {
		t.Fatal(err)
	}
	manager = NewManagerWithKeyring(keyring)
	got, status, err := manager.Credential()
	if err != nil {
		t.Fatal(err)
	}
	assertCredential(t, got, replacement)
	assertStatus(t, status, "stored", true)
}

func TestExplicitEnrollmentReenablesSameCredential(t *testing.T) {
	keyring := newFakeKeyring()
	credential := testCredential(62)
	keyring.put(testRecord(t, credential, false))
	manager := NewManagerWithKeyring(keyring)
	if err := manager.Store(credential); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, manager.Status(), "stored", true)
	got, status, err := NewManagerWithKeyring(keyring).Credential()
	if err != nil {
		t.Fatal(err)
	}
	assertCredential(t, got, credential)
	assertStatus(t, status, "stored", true)
}
