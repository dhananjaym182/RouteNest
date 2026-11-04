package app

import (
	"testing"

	"routenest/internal/accounts"
	"routenest/internal/credentials"
	"routenest/internal/provideradapters"
	"routenest/internal/providerstate"
	"routenest/internal/routing"
)

// Configure must install hooks once, however often it is called.
func TestConfigureIsIdempotent(t *testing.T) {
	Configure()
	Configure()
	Configure()
	if configureCalls != 1 {
		t.Fatalf("configure ran %d times, want 1", configureCalls)
	}
	if routing.ConfigProvider == nil || !routing.HasComboCatalog() {
		t.Fatal("composition hooks were not installed")
	}
}

// Every required seam is installed, including the ones formerly wired by
// package init. ValidateComposition is the same check Configure runs.
func TestConfigureInstallsRequiredSeams(t *testing.T) {
	Configure()
	if err := ValidateComposition(); err != nil {
		t.Fatalf("composition incomplete: %v", err)
	}
	if accounts.ProxyConfigProvider() == nil {
		t.Fatal("accounts proxy config provider not installed")
	}
	if !provideradapters.HasDefault() {
		t.Fatal("provider adapter registry not installed")
	}
	if !providerstate.HasDefault() {
		t.Fatal("provider state store not installed")
	}
	if !credentials.HasDefault() {
		t.Fatal("credentials registry not installed")
	}
}
