//go:build windows

package adminauth

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

type windowsInspectionRoot struct {
	path string
}

func openInspectionRoot(path string) (inspectionRoot, error) {
	info, err := inspectionPathInfo(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, &os.PathError{Op: "open", Path: path, Err: errors.New("not a directory")}
	}
	return &windowsInspectionRoot{path: path}, nil
}

func (root *windowsInspectionRoot) Close() error { return nil }

func (root *windowsInspectionRoot) Stat(name string) (os.FileInfo, error) {
	path, err := root.resolve(name)
	if err != nil {
		return nil, err
	}
	return inspectionPathInfo(path)
}

func (root *windowsInspectionRoot) OpenFile(name string, flag int, _ os.FileMode) (*os.File, error) {
	if flag != os.O_RDONLY && flag != os.O_RDONLY|syscall.O_NONBLOCK {
		return nil, &os.PathError{Op: "open", Path: name, Err: errors.New("unsupported file mode")}
	}
	path, err := root.resolve(name)
	if err != nil {
		return nil, err
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(pointer, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}

func inspectionFileInfo(file *os.File) (os.FileInfo, error) {
	return windowsInspectionInfoForHandle(file.Name(), windows.Handle(file.Fd()))
}

func (root *windowsInspectionRoot) resolve(name string) (string, error) {
	if name == "." {
		return root.path, nil
	}
	if filepath.IsAbs(name) || name == "" || name == ".." || filepath.Base(name) != name {
		return "", &os.PathError{Op: "open", Path: name, Err: errors.New("path escapes authority directory")}
	}
	return filepath.Join(root.path, name), nil
}

type windowsInspectionInfo struct {
	name       string
	size       int64
	mode       os.FileMode
	modified   time.Time
	volume     uint32
	fileIndexH uint32
	fileIndexL uint32
}

func inspectionPathInfo(path string) (os.FileInfo, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(pointer, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "lstat", Path: path, Err: err}
	}
	defer windows.CloseHandle(handle)
	return windowsInspectionInfoForHandle(path, handle)
}

func windowsInspectionInfoForHandle(path string, handle windows.Handle) (os.FileInfo, error) {
	var data windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &data); err != nil {
		return nil, &os.PathError{Op: "lstat", Path: path, Err: err}
	}
	mode := os.FileMode(0666)
	if data.FileAttributes&windows.FILE_ATTRIBUTE_READONLY != 0 {
		mode = 0444
	}
	if data.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		mode |= os.ModeDir | 0111
	}
	if data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		mode |= os.ModeSymlink
	}
	return windowsInspectionInfo{
		name:       filepath.Base(path),
		size:       int64(data.FileSizeHigh)<<32 | int64(data.FileSizeLow),
		mode:       mode,
		modified:   time.Unix(0, data.LastWriteTime.Nanoseconds()),
		volume:     data.VolumeSerialNumber,
		fileIndexH: data.FileIndexHigh,
		fileIndexL: data.FileIndexLow,
	}, nil
}

func (info windowsInspectionInfo) Name() string       { return info.name }
func (info windowsInspectionInfo) Size() int64        { return info.size }
func (info windowsInspectionInfo) Mode() os.FileMode  { return info.mode }
func (info windowsInspectionInfo) ModTime() time.Time { return info.modified }
func (info windowsInspectionInfo) IsDir() bool        { return info.mode.IsDir() }
func (info windowsInspectionInfo) Sys() any           { return info }
func (info windowsInspectionInfo) inspectionIdentity() (uint64, uint64, bool) {
	return uint64(info.volume), uint64(info.fileIndexH)<<32 | uint64(info.fileIndexL), true
}
