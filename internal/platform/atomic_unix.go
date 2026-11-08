//go:build unix

package platform

import "os"

func syncDirectoryDurable(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func replaceFileAtomic(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}
