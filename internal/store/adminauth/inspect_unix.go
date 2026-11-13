//go:build unix

package adminauth

import (
	"os"
)

func openInspectionRoot(path string) (inspectionRoot, error) {
	return os.OpenRoot(path)
}

func inspectionPathInfo(path string) (os.FileInfo, error) {
	return os.Lstat(path)
}

func inspectionFileInfo(file *os.File) (os.FileInfo, error) {
	return file.Stat()
}
