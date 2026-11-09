//go:build windows

package platform

import "golang.org/x/sys/windows"

func syncDirectoryDurable(path string) error {
	utf16Path, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// FlushFileBuffers requires GENERIC_WRITE. FILE_FLAG_BACKUP_SEMANTICS is
	// required to obtain a directory handle; sharing keeps ordinary readers and
	// replacers from being blocked while the metadata flush is performed. This
	// is an explicit metadata-flush attempt after replacement, not a guarantee
	// against storage hardware that ignores flush commands.
	handle, err := windows.CreateFile(utf16Path, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	return windows.FlushFileBuffers(handle)
}

func replaceFileAtomic(oldPath, newPath string) error {
	oldUTF16, err := windows.UTF16PtrFromString(oldPath)
	if err != nil {
		return err
	}
	newUTF16, err := windows.UTF16PtrFromString(newPath)
	if err != nil {
		return err
	}
	// MOVEFILE_WRITE_THROUGH waits for the move to complete on disk. The temp
	// file itself has already been flushed by file.Sync before this call.
	return windows.MoveFileEx(oldUTF16, newUTF16, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
