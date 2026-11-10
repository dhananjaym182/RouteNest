//go:build darwin && !cgo

package desktopauth

import (
	"errors"
	"testing"
)

func TestDarwinWithoutCgoHasNoCredentialFallback(t *testing.T) {
	keyring := systemKeyring{}
	if err := keyring.Set(serviceName, accountName, "credential"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unsupported native set = %v", err)
	}
	if value, err := keyring.Get(serviceName, accountName); value != "" || !errors.Is(err, ErrUnavailable) {
		t.Fatal("unsupported native read returned a credential")
	}
	if err := keyring.Delete(serviceName, accountName); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unsupported native removal = %v", err)
	}
	assertStatus(t, NewManagerForInstallation("no-cgo-installation").Inspect(), "unavailable", false)
}
