package platform

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

type AtomicWriteStage string

const (
	AtomicWritePermission    AtomicWriteStage = "permission"
	AtomicWriteData          AtomicWriteStage = "write"
	AtomicWriteFileSync      AtomicWriteStage = "file_sync"
	AtomicWriteRename        AtomicWriteStage = "rename"
	AtomicWriteDirectorySync AtomicWriteStage = "directory_sync"
)

type AtomicWriteOptions struct {
	Fault func(AtomicWriteStage) error
}

type AtomicWriteError struct {
	Stage    AtomicWriteStage
	Replaced bool
	Err      error
}

func (e *AtomicWriteError) Error() string {
	return fmt.Sprintf("atomic write %s: %v", e.Stage, e.Err)
}

func (e *AtomicWriteError) Unwrap() error {
	return e.Err
}

func AtomicWriteMayHaveReplaced(err error) bool {
	var writeErr *AtomicWriteError
	return errors.As(err, &writeErr) && writeErr.Replaced
}

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

func WriteFileAtomicDurable(path string, data []byte, perm os.FileMode) error {
	return WriteFileAtomicDurableWithOptions(path, data, perm, AtomicWriteOptions{})
}

func WriteFileAtomicDurableWithOptions(path string, data []byte, perm os.FileMode, options AtomicWriteOptions) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create parent directory for %s: %w", path, err)
	}
	file, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return &AtomicWriteError{Stage: AtomicWriteData, Err: err}
	}
	tmp := file.Name()
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
		_ = os.Remove(tmp)
	}()
	hit := func(stage AtomicWriteStage, replaced bool) error {
		if options.Fault == nil {
			return nil
		}
		if err := options.Fault(stage); err != nil {
			return &AtomicWriteError{Stage: stage, Replaced: replaced, Err: err}
		}
		return nil
	}
	if err := hit(AtomicWritePermission, false); err != nil {
		return err
	}
	if err := ensureFileMode(tmp, perm); err != nil {
		return &AtomicWriteError{Stage: AtomicWritePermission, Err: err}
	}
	if err := hit(AtomicWriteData, false); err != nil {
		return err
	}
	written, err := file.Write(data)
	if err != nil {
		return &AtomicWriteError{Stage: AtomicWriteData, Err: err}
	}
	if written != len(data) {
		return &AtomicWriteError{Stage: AtomicWriteData, Err: fmt.Errorf("short write: wrote %d of %d bytes", written, len(data))}
	}
	if err := hit(AtomicWriteFileSync, false); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return &AtomicWriteError{Stage: AtomicWriteFileSync, Err: err}
	}
	if err := file.Close(); err != nil {
		return &AtomicWriteError{Stage: AtomicWriteFileSync, Err: err}
	}
	closed = true
	if err := hit(AtomicWriteRename, false); err != nil {
		return err
	}
	if err := replaceFileAtomic(tmp, path); err != nil {
		return &AtomicWriteError{Stage: AtomicWriteRename, Err: err}
	}
	if err := hit(AtomicWriteDirectorySync, true); err != nil {
		return err
	}
	if err := syncDirectoryDurable(dir); err != nil {
		return &AtomicWriteError{Stage: AtomicWriteDirectorySync, Replaced: true, Err: err}
	}
	return nil
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
