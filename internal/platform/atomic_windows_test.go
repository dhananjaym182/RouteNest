//go:build windows

package platform

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// This test must execute on Windows. Cross-compilation verifies only that the
// supported Win32 calls and constants build; it cannot validate filesystem or
// handle behavior.
func TestWindowsDurableReplacementRuntime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	for _, value := range []string{"one", "two", "three"} {
		if err := WriteFileAtomicDurable(path, []byte(value), 0600); err != nil {
			t.Fatalf("write %q: %v", value, err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != value {
			t.Fatalf("persisted = %q, err = %v", data, err)
		}
	}
	assertNoAtomicTemps(t, dir, path)
}

func TestWindowsReplacementFailureLeavesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(pathUTF16, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)

	err = WriteFileAtomicDurable(path, []byte("new"), 0600)
	if err == nil || AtomicWriteMayHaveReplaced(err) {
		t.Fatalf("replacement error = %v, want pre-replacement failure", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || string(data) != "old" {
		t.Fatalf("persisted = %q, err = %v", data, readErr)
	}
	assertNoAtomicTemps(t, dir, path)
}
