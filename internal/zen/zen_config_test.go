package zen

import (
	"testing"

	"routenest/internal/config"
)

// GetConfig returns a private snapshot: editing it cannot change published
// state, so request-path readers never race with an admin update.
func TestGetConfigReturnsSnapshot(t *testing.T) {
	ConfigMu.Lock()
	old := Config
	Config = &config.Zen{Enabled: true, MaxConcurrency: 4, Proxies: []string{"http://a:1"}, ZenHeaders: map[string]string{"X": "1"}}
	ConfigMu.Unlock()
	t.Cleanup(func() {
		ConfigMu.Lock()
		Config = old
		ConfigMu.Unlock()
	})

	cfg := GetConfig()
	cfg.Enabled = false
	cfg.Proxies[0] = "http://changed:2"
	cfg.ZenHeaders["X"] = "2"

	again := GetConfig()
	if !again.Enabled || again.Proxies[0] != "http://a:1" || again.ZenHeaders["X"] != "1" {
		t.Fatalf("published config changed through a snapshot: %+v", again)
	}
}

// SetConfig clones its argument before publishing.
func TestSetConfigClonesArgument(t *testing.T) {
	ConfigMu.Lock()
	old := Config
	ConfigMu.Unlock()
	t.Cleanup(func() {
		ConfigMu.Lock()
		Config = old
		ConfigMu.Unlock()
	})

	next := DefaultConfig()
	next.Enabled = false
	next.Proxies = []string{"http://a:1"}
	SetConfig(next)
	next.Enabled = true
	next.Proxies[0] = "http://changed:2"

	got := GetConfig()
	if got.Enabled {
		t.Fatal("editing the SetConfig argument changed the published value")
	}
	if got.Proxies[0] != "http://a:1" {
		t.Fatalf("proxy slice changed after publish: %v", got.Proxies)
	}
}
