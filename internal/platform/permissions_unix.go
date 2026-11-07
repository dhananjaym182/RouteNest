//go:build unix

package platform

import "os"

func secureDirectoryPermissions(path string) error {
	return os.Chmod(path, 0700)
}

func secureFilePermissions(path string, mode os.FileMode) error {
	return os.Chmod(path, mode)
}
