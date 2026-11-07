package platform

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnsurePrivateDir creates dir and restricts it to the current user. Platform
// implementations use owner-only mode bits or a protected Windows ACL.
func EnsurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create private directory %s: %w", dir, err)
	}
	if err := secureDirectoryPermissions(dir); err != nil {
		return fmt.Errorf("secure private directory %s: %w", dir, err)
	}
	return nil
}

func ensureFileMode(path string, mode os.FileMode) error {
	if err := secureFilePermissions(path, mode); err != nil {
		return fmt.Errorf("set mode %04o on %s: %w", mode, path, err)
	}
	return nil
}

// SecureDataPermissions tightens the RouteNest data directory and known state
// files without changing unrelated files in the same directory.
func SecureDataPermissions(dir string) error {
	if err := EnsurePrivateDir(dir); err != nil {
		return err
	}
	names := append(CurrentDataFiles(), KnownDataFiles()...)
	for _, name := range names {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("inspect state file %s: %w", path, err)
		}
		if err := ensureFileMode(path, 0600); err != nil {
			return err
		}
	}
	return nil
}
