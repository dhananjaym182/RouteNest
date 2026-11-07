//go:build !windows

package adminsso

import (
	"fmt"
	"os"
	"syscall"
)

func openSecretFile(root *os.Root, name string) (*os.File, error) {
	// A replaced FIFO must not block before the descriptor can be inspected.
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}

func validateSecretFile(path string, info os.FileInfo) error {
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("secret file %s must have mode 0600", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("secret file %s must be owned by the service user", path)
	}
	return nil
}
