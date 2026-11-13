//go:build unix

package adminauth

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"syscall"
)

func acquireLock(f *os.File) error {
	if e := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		return fmt.Errorf("%w: %v", ErrLocked, e)
	}
	return nil
}
func releaseLock(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_UN) }
func openPrivateFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
}
func checkSecurePath(path string, directory bool) error {
	v, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if v.Mode()&os.ModeSymlink != 0 || v.IsDir() != directory || (!directory && !v.Mode().IsRegular()) {
		return fmt.Errorf("unsafe administrator authority path")
	}
	if v.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("administrator authority requires owner-only permissions")
	}
	stat, ok := v.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("administrator authority owner mismatch")
	}
	return nil
}
func secureCreatedFile(path string) error { return checkSecurePath(path, false) }
func syncDirectory(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func replaceSnapshot(old, new string) error { return os.Rename(old, new) }

func secureCreatedDirectory(path string) error { return checkSecurePath(path, true) }
