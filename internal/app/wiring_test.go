package app

import (
	"testing"

	"routenest/internal/proxycore"
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
	if routing.ConfigProvider == nil || routing.FindCombo == nil || proxycore.RegisterAdminRoutes == nil {
		t.Fatal("composition hooks were not installed")
	}
}
