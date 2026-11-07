//go:build unix

package adminauth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecoveryOpenRejectsUnsafeOrMissingAuthorityWithoutRepair(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		dir := privateDir(t)
		if _, err := OpenForRecovery(dir); err == nil {
			t.Fatal("missing authority accepted")
		}
		if _, err := os.Stat(filepath.Join(dir, "admin-auth")); !os.IsNotExist(err) {
			t.Fatalf("recovery created an authority: %v", err)
		}
	})
	for _, target := range []string{"data", "authority", "snapshot", "journal", "lock", "symlink"} {
		t.Run(target, func(t *testing.T) {
			dir := privateDir(t)
			authDir := filepath.Join(dir, "admin-auth")
			repo, err := Open(authDir)
			if err != nil {
				t.Fatal(err)
			}
			if err := applyFixture(repo, 1); err != nil {
				t.Fatal(err)
			}
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			path, mode := dir, os.FileMode(0755)
			recoveryDir := dir
			switch target {
			case "authority":
				path = authDir
			case "snapshot":
				path, mode = filepath.Join(authDir, SnapshotName), 0644
			case "journal":
				path, mode = filepath.Join(authDir, JournalName), 0644
			case "lock":
				path, mode = filepath.Join(authDir, LockName), 0644
			case "symlink":
				path = filepath.Join(t.TempDir(), "linked-data")
				recoveryDir = path
				if err := os.Symlink(dir, path); err != nil {
					t.Fatal(err)
				}
			}
			if target != "symlink" {
				if err := os.Chmod(path, mode); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := OpenForRecovery(recoveryDir); err == nil {
				t.Fatal("unsafe recovery path accepted")
			}
			if target != "symlink" {
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != mode {
					t.Fatalf("recovery changed unsafe permissions: %v, %v", info, err)
				}
			}
		})
	}
}
