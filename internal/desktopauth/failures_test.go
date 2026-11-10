package desktopauth

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestManagerFailedAndPartialWrites(t *testing.T) {
	credential := testCredential(30)
	for _, tc := range []struct {
		name      string
		configure func(*fakeKeyring)
		wantErr   error
		state     string
		entries   int
	}{
		{"set denied", func(f *fakeKeyring) { f.setErr = errors.New("denied") }, ErrUnavailable, "unavailable", 0},
		{"set not found", func(f *fakeKeyring) { f.setErr = keyring.ErrNotFound }, ErrUnavailable, "unavailable", 0},
		{"write then error", func(f *fakeKeyring) {
			f.setErr, f.writeOnError = errors.New("write failed"), true
		}, ErrUnavailable, "unavailable", 1},
		{"partial write then error", func(f *fakeKeyring) {
			f.setErr, f.writeOnError = errors.New("partial write"), true
			f.transform = func(value string) string { return value[:len(value)/2] }
		}, ErrUnavailable, "unavailable", 1},
		{"silent partial write", func(f *fakeKeyring) {
			f.transform = func(value string) string { return value[:len(value)/2] }
		}, ErrCorrupt, "unavailable", 1},
		{"silent missing write", func(f *fakeKeyring) { f.skipSet = true }, ErrVerification, "unavailable", 0},
		{"read back unavailable", func(f *fakeKeyring) { f.afterSetGetErr = errors.New("locked") }, ErrUnavailable, "unavailable", 1},
		{"read back missing", func(f *fakeKeyring) { f.afterSetGetErr = fmt.Errorf("wrapped: %w", keyring.ErrNotFound) }, ErrVerification, "unavailable", 1},
		{"different ID", func(f *fakeKeyring) {
			f.transform = func(value string) string { return strings.Replace(value, credential.ID, testCredential(31).ID, 1) }
		}, ErrVerification, "unavailable", 1},
		{"different secret", func(f *fakeKeyring) {
			f.transform = func(value string) string {
				return strings.Replace(value, credential.Secret, testCredential(32).Secret, 1)
			}
		}, ErrVerification, "unavailable", 1},
		{"different expiry", func(f *fakeKeyring) {
			f.transform = func(value string) string { return strings.Replace(value, "2027-02-01", "2027-02-02", 1) }
		}, ErrVerification, "unavailable", 1},
		{"different preference", func(f *fakeKeyring) {
			f.transform = func(value string) string {
				return strings.Replace(value, `"automaticSignIn":true`, `"automaticSignIn":false`, 1)
			}
		}, ErrVerification, "unavailable", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyring := newFakeKeyring()
			tc.configure(keyring)
			manager := NewManagerWithKeyring(keyring)
			if err := manager.Store(credential); !errors.Is(err, tc.wantErr) {
				t.Fatalf("write error = %v, want %v", err, tc.wantErr)
			}
			assertStatus(t, manager.Status(), tc.state, false)
			if keyring.count("get") != 2 || keyring.count("set") != 1 || keyring.count("delete") != 0 {
				t.Fatal("failed write skipped verification or attempted destructive cleanup")
			}
			if len(keyring.snapshot()) != tc.entries {
				t.Fatal("failed write silently deleted or replaced remaining material")
			}
			before := keyring.snapshot()
			keyring.setErr, keyring.transform = nil, nil
			assertStatus(t, manager.Inspect(), "unavailable", false)
			if err := manager.Store(credential); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("retry did not require reconciliation: %v", err)
			}
			if !reflect.DeepEqual(before, keyring.snapshot()) || keyring.count("set") != 1 {
				t.Fatal("retry changed uncertain storage")
			}
			if strings.Contains(tc.name, "partial write") {
				restarted := NewManagerWithKeyring(keyring)
				if err := restarted.Store(credential); !errors.Is(err, ErrCorrupt) {
					t.Fatalf("fresh manager overwrote partial material: %v", err)
				}
				if !reflect.DeepEqual(before, keyring.snapshot()) || keyring.count("set") != 1 {
					t.Fatal("fresh manager changed corrupt storage")
				}
			}
		})
	}
}

func TestManagerVerificationRejectsOldCredential(t *testing.T) {
	keyring := newFakeKeyring()
	original := testCredential(33)
	keyring.put(testRecord(t, original, false))
	before := keyring.snapshot()
	keyring.skipSet = true
	manager := NewManagerWithKeyring(keyring)
	if err := manager.Store(testCredential(34)); !errors.Is(err, ErrVerification) {
		t.Fatalf("unchanged write = %v", err)
	}
	assertStatus(t, manager.Status(), "unavailable", false)
	if !reflect.DeepEqual(keyring.snapshot(), before) || keyring.count("delete") != 0 {
		t.Fatal("failed verification destroyed the previous credential")
	}
}

func TestManagerFailedPreferenceWriteDoesNotReportSuccess(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		keyring := newFakeKeyring()
		keyring.put(testRecord(t, testCredential(35), !enabled))
		keyring.skipSet = true
		before := keyring.snapshot()
		manager := NewManagerWithKeyring(keyring)
		if err := manager.SetAutoSignIn(enabled); !errors.Is(err, ErrVerification) {
			t.Fatalf("unchanged preference write = %v", err)
		}
		assertStatus(t, manager.Status(), "unavailable", false)
		if !reflect.DeepEqual(before, keyring.snapshot()) || keyring.count("delete") != 0 {
			t.Fatal("failed preference write changed the remaining credential")
		}
	}
}

func TestManagerRemovalIsExplicitAndVerified(t *testing.T) {
	for _, value := range []string{testRecord(t, testCredential(36), true), "corrupt credential material"} {
		keyring := newFakeKeyring()
		keyring.put(value)
		unrelated := keyringAddress{"OtherService", "unrelated-account"}
		keyring.values[unrelated] = "unrelated secret"
		manager := NewManagerWithKeyring(keyring)
		if err := manager.Remove(); err != nil {
			t.Fatal(err)
		}
		assertStatus(t, manager.Status(), "missing", false)
		values := keyring.snapshot()
		if len(values) != 1 || values[unrelated] != "unrelated secret" {
			t.Fatal("removal affected an unrelated credential")
		}
		if keyring.count("delete") != 1 || keyring.count("get") != 1 {
			t.Fatal("removal was not followed by read-back verification")
		}
		if err := manager.Remove(); err != nil {
			t.Fatal("removal of missing credential was not idempotent")
		}
		keyring.assertAddress(t)
	}
}

func TestManagerFailedRemovalRetainsEvidence(t *testing.T) {
	for _, tc := range []struct {
		name      string
		value     string
		configure func(*fakeKeyring)
		wantErr   error
		state     string
	}{
		{"denied", testRecord(t, testCredential(37), true), func(f *fakeKeyring) { f.deleteErr = errors.New("denied") }, ErrUnavailable, "unavailable"},
		{"silent unchanged", testRecord(t, testCredential(37), true), func(f *fakeKeyring) { f.skipDelete = true }, ErrVerification, "unavailable"},
		{"unchanged corrupt", "corrupt", func(f *fakeKeyring) { f.skipDelete = true }, ErrCorrupt, "unavailable"},
		{"false missing", testRecord(t, testCredential(37), true), func(f *fakeKeyring) { f.deleteErr = fmt.Errorf("wrapped: %w", keyring.ErrNotFound) }, ErrVerification, "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyring := newFakeKeyring()
			keyring.put(tc.value)
			before := keyring.snapshot()
			tc.configure(keyring)
			manager := NewManagerWithKeyring(keyring)
			if err := manager.Remove(); !errors.Is(err, tc.wantErr) {
				t.Fatalf("remove = %v, want %v", err, tc.wantErr)
			}
			assertStatus(t, manager.Status(), tc.state, false)
			if !reflect.DeepEqual(before, keyring.snapshot()) || keyring.count("delete") != 1 || keyring.count("set") != 0 {
				t.Fatal("failed removal retried or overwrote remaining material")
			}
		})
	}
}

func TestManagerRemovalWithAmbiguousFailureIsUnavailable(t *testing.T) {
	for _, reportOnDelete := range []bool{true, false} {
		keyring := newFakeKeyring()
		keyring.put(testRecord(t, testCredential(38), true))
		if reportOnDelete {
			keyring.deleteOnError, keyring.deleteErr = true, errors.New("failed after delete")
		} else {
			keyring.afterDeleteGetErr = errors.New("verification denied")
		}
		manager := NewManagerWithKeyring(keyring)
		if err := manager.Remove(); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("ambiguous removal = %v", err)
		}
		assertStatus(t, manager.Status(), "unavailable", false)
		if len(keyring.snapshot()) != 0 {
			t.Fatal("fixture did not simulate removal before the error")
		}
		keyring.getErr, keyring.deleteErr = nil, nil
		assertStatus(t, manager.Inspect(), "unavailable", false)
		if err := manager.Remove(); err != nil {
			t.Fatalf("explicit reconciliation failed: %v", err)
		}
		assertStatus(t, manager.Inspect(), "missing", false)
	}
}
