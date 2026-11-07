//go:build unix

package adminauth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRejectUnsafePermissionsAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	if e := os.Chmod(dir, 0755); e != nil {
		t.Fatal(e)
	}
	if _, e := Open(dir); e == nil {
		t.Fatal("unsafe directory accepted")
	}
	os.Chmod(dir, 0700)
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = applyFixture(s, 1); e != nil {
		t.Fatal(e)
	}
	s.Close()
	path := filepath.Join(dir, SnapshotName)
	os.Chmod(path, 0644)
	if _, e = Open(dir); e == nil {
		t.Fatal("world-readable snapshot accepted")
	}
	os.Chmod(path, 0600)
	target := filepath.Join(dir, "saved.json")
	os.Rename(path, target)
	os.Symlink(target, path)
	if _, e = Open(dir); e == nil {
		t.Fatal("snapshot symlink accepted")
	}
}
