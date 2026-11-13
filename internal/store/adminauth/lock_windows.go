//go:build windows

package adminauth

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"routenest/internal/platform"
	"unsafe"
)

func lockRange() windows.Overlapped {
	return windows.Overlapped{Offset: ^uint32(0), OffsetHigh: 0x3fffffff}
}
func acquireLock(f *os.File) error {
	ov := lockRange()
	if e := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ov); e != nil {
		return fmt.Errorf("%w: %v", ErrLocked, e)
	}
	return nil
}
func releaseLock(f *os.File) error {
	ov := lockRange()
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ov)
}
func openPrivateFile(path string) (*os.File, error) {
	name, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return nil, e
	}
	handle, e := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		return nil, e
	}
	return os.NewFile(uintptr(handle), path), nil
}
func checkSecurePath(path string, directory bool) error {
	v, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if v.Mode()&os.ModeSymlink != 0 || v.IsDir() != directory || (!directory && !v.Mode().IsRegular()) {
		return fmt.Errorf("unsafe administrator authority path")
	}
	sd, e := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return e
	}
	owner, _, e := sd.Owner()
	if e != nil {
		return e
	}
	token := windows.GetCurrentProcessToken()
	user, e := token.GetTokenUser()
	if e != nil {
		return e
	}
	// Elevated processes (CI runners) own new files as BUILTIN\Administrators, not as the user SID.
	if ok, e := platform.OwnerIsCurrentPrincipal(owner); e != nil {
		return e
	} else if !ok {
		return fmt.Errorf("administrator authority owner mismatch")
	}
	acl, _, e := sd.DACL()
	if e != nil || acl == nil {
		return fmt.Errorf("administrator authority requires an explicit private ACL")
	}
	system, e := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if e != nil {
		return e
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var p *windows.ACCESS_ALLOWED_ACE
		if e = windows.GetAce(acl, i, &p); e != nil {
			return e
		}
		if p.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if p.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return fmt.Errorf("unsupported administrator authority ACL entry")
		}
		sid := (*windows.SID)(unsafe.Pointer(&p.SidStart))
		if !sid.Equals(user.User.Sid) && !sid.Equals(system) {
			return fmt.Errorf("administrator authority ACL grants another principal")
		}
	}
	return nil
}
func secureCreatedFile(path string) error      { return secureCreatedWindowsPath(path, false) }
func secureCreatedDirectory(path string) error { return secureCreatedWindowsPath(path, true) }
func secureCreatedWindowsPath(path string, directory bool) error {
	token := windows.GetCurrentProcessToken()
	user, e := token.GetTokenUser()
	if e != nil {
		return e
	}
	sid := user.User.Sid.String()
	inherit := ""
	if directory {
		inherit = "OICI"
	}
	sd, e := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;" + inherit + ";FA;;;" + sid + ")(A;" + inherit + ";FA;;;SY)")
	if e != nil {
		return e
	}
	dacl, _, e := sd.DACL()
	if e != nil {
		return e
	}
	if e = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); e != nil {
		return e
	}
	return checkSecurePath(path, directory)
}
func syncDirectory(string) error { return nil }
func replaceSnapshot(old, new string) error {
	o, e := windows.UTF16PtrFromString(old)
	if e != nil {
		return e
	}
	n, e := windows.UTF16PtrFromString(new)
	if e != nil {
		return e
	}
	return windows.MoveFileEx(o, n, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
