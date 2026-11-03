package config

import (
	"os"
	"path/filepath"
	"testing"
)

// resetLive clears the process-wide live value and restores it afterwards.
func resetLive(t *testing.T) {
	t.Helper()
	Mu.Lock()
	old := live.Load()
	live.Store(nil)
	Mu.Unlock()
	t.Cleanup(func() {
		Mu.Lock()
		live.Store(old)
		Mu.Unlock()
	})
}

// A snapshot is a private copy: editing it does not change the live value.
func TestSnapshotIsPrivateCopy(t *testing.T) {
	t.Setenv("ROUTENEST_DATA_DIR", t.TempDir())
	resetLive(t)

	base := DefaultProxy()
	base.OnlyFree = false
	Publish(base)

	snap := Snapshot()
	snap.OnlyFree = true
	if Live().OnlyFree {
		t.Fatal("editing a snapshot must not change the live configuration")
	}
}

// Publish stores the new value and writes it to the data directory.
func TestPublishPersistsToDataDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ROUTENEST_DATA_DIR", dir)
	resetLive(t)

	next := DefaultProxy()
	next.OnlyFree = true
	Publish(next)

	if !Live().OnlyFree {
		t.Fatal("published value not visible through Live")
	}
	data, err := os.ReadFile(filepath.Join(dir, ProxyPath))
	if err != nil {
		t.Fatalf("published config not written: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("published config file is empty")
	}
}

// Held returns the same pointer as Live without taking the lock, so it is safe to
// call while the caller holds Mu.
func TestHeldMatchesLiveUnderLock(t *testing.T) {
	t.Setenv("ROUTENEST_DATA_DIR", t.TempDir())
	resetLive(t)

	Mu.Lock()
	held := Held()
	Mu.Unlock()
	if held != Live() {
		t.Fatal("Held and Live must return the same live pointer")
	}
}

// Publish clones its argument: a caller that keeps editing the value it passed
// cannot change what readers see.
func TestPublishClonesArgument(t *testing.T) {
	t.Setenv("ROUTENEST_DATA_DIR", t.TempDir())
	resetLive(t)

	next := DefaultProxy()
	next.OnlyFree = false
	Publish(next)
	next.OnlyFree = true
	next.Headers["X-Mutated"] = "yes"

	if Live().OnlyFree {
		t.Fatal("editing the published argument changed the live value")
	}
	if _, ok := Live().Headers["X-Mutated"]; ok {
		t.Fatal("editing the published map changed the live value")
	}
}

// Readers take no lock and must never observe a partially edited value while
// publishers swap configurations. Run with -race.
func TestConcurrentPublishAndRead(t *testing.T) {
	t.Setenv("ROUTENEST_DATA_DIR", t.TempDir())
	resetLive(t)

	mk := func(free bool) *Proxy {
		p := DefaultProxy()
		p.OnlyFree = free
		return p
	}
	Publish(mk(false))

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			Publish(mk(i%2 == 0))
		}
	}()
	for i := 0; i < 2000; i++ {
		cfg := Live()
		if cfg == nil {
			t.Fatal("Live returned nil")
		}
		_ = cfg.OnlyFree
		_ = cfg.Headers
		_ = cfg.ModelChain
	}
	<-done
}
