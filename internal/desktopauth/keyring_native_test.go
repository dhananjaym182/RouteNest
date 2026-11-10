package desktopauth

import (
	"errors"
	"math"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestNativeKeychainStatusFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		status int32
		err    error
	}{
		{0, nil},
		{-25300, keyring.ErrNotFound},
		{-26275, ErrCorrupt},
		{-25293, ErrUnavailable},
		{-25308, ErrUnavailable},
		{-128, ErrUnavailable},
		{-25291, ErrUnavailable},
		{-25299, ErrUnavailable},
		{math.MaxInt32, ErrUnavailable},
		{math.MinInt32, ErrUnavailable},
	} {
		err := nativeKeychainError(tc.status)
		if !errors.Is(err, tc.err) {
			t.Fatalf("native status %d = %v, want %v", tc.status, err, tc.err)
		}
		if err != nil && errors.Unwrap(err) != nil {
			t.Fatal("native keychain status retained backend material")
		}
	}
}

func TestManagerNativeCorruptMaterialCannotBeOverwritten(t *testing.T) {
	keyring := newFakeKeyring()
	keyring.put("oversized native record")
	keyring.getErr = nativeKeychainError(-26275)
	manager := NewManagerWithKeyring(keyring)
	assertStatus(t, manager.Inspect(), "corrupt", false)
	if err := manager.Store(testCredential(90)); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("native corrupt material = %v", err)
	}
	if keyring.count("set") != 0 || keyring.count("delete") != 0 {
		t.Fatal("native corrupt storage was implicitly replaced")
	}
}
