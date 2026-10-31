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
	old := live
	live = nil
	Mu.Unlock()
	t.Cleanup(func() {
		Mu.Lock()
		live = old
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
