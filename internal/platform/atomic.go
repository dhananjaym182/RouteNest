package platform

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// WriteFileAtomic writes via a temporary file and rename so a process kill
// cannot leave a truncated JSON file behind.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create parent directory for %s: %w", path, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := ensureFileMode(tmp, perm); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return ensureFileMode(path, perm)
}

// BackupCorruptFile renames an unparsable data file to a timestamped backup so
// a later save cannot silently overwrite the original content.
func BackupCorruptFile(path string, cause error) {
	backup := fmt.Sprintf("%s.corrupt-%d", path, time.Now().Unix())
	if err := os.Rename(path, backup); err != nil {
		log.Printf("CORRUPT data file %s (%v); rename to backup failed: %v", path, cause, err)
		return
	}
	log.Printf("CORRUPT data file %s (%v); backed up to %s", path, cause, backup)
}
