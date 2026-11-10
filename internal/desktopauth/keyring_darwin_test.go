//go:build darwin && cgo

package desktopauth

import (
	"errors"
	"testing"
)

func TestNativeKeychainRejectsInvalidAddressWithoutOSAccess(t *testing.T) {
	for _, address := range []credentialSlot{
		{"", "account"},
		{"service", ""},
		{"service\x00suffix", "account"},
		{"service", "account\x00suffix"},
		{string([]byte{0xff}), "account"},
		{"service", string([]byte{0xff})},
	} {
		keyring := systemKeyring{}
		if err := keyring.Set(address.service, address.account, "credential"); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("invalid native address set = %v", err)
		}
		if value, err := keyring.Get(address.service, address.account); value != "" || !errors.Is(err, ErrUnavailable) {
			t.Fatal("invalid native address returned a credential")
		}
		if err := keyring.Delete(address.service, address.account); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("invalid native address removal = %v", err)
		}
	}
}
