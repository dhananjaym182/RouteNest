//go:build windows

package platform

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func secureDirectoryPermissions(path string) error {
	return secureWindowsPath(path, true)
}

func secureFilePermissions(path string, _ os.FileMode) error {
	return secureWindowsPath(path, false)
}

func secureWindowsPath(path string, directory bool) error {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	inherit := ""
	if directory {
		inherit = "OICI"
	}
	descriptor, err := windows.SecurityDescriptorFromString(
		"D:P(A;" + inherit + ";FA;;;" + user.User.Sid.String() + ")(A;" + inherit + ";FA;;;SY)",
	)
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		return err
	}
	return verifyWindowsPrivatePath(path, directory, user.User.Sid)
}

// tokenDefaultOwner returns the SID Windows assigns as owner of new objects created by this
// process. It is the user's SID for a normal process, but BUILTIN\Administrators for an
// elevated one (for example the GitHub-hosted Windows runners), so owner checks that only
// compare against the user SID reject every directory the process itself just created.
func tokenDefaultOwner(token windows.Token) (*windows.SID, error) {
	var needed uint32
	if err := windows.GetTokenInformation(token, windows.TokenOwner, nil, 0, &needed); err != windows.ERROR_INSUFFICIENT_BUFFER {
		return nil, err
	}
	buffer := make([]byte, needed)
	if err := windows.GetTokenInformation(token, windows.TokenOwner, &buffer[0], needed, &needed); err != nil {
		return nil, err
	}
	owner := (*struct{ Owner *windows.SID })(unsafe.Pointer(&buffer[0])).Owner
	return owner.Copy() // the SID points into buffer; copy before it can be collected
}

// privateOwnerAllowed reports whether owner may own a private path: the current user, or the
// token's own default owner. Any other principal (another account, an unrelated group) is
// rejected, and the ACL check below still limits access to the user and SYSTEM.
func privateOwnerAllowed(owner *windows.SID, allowed ...*windows.SID) bool {
	if owner == nil {
		return false
	}
	for _, sid := range allowed {
		if sid != nil && owner.Equals(sid) {
			return true
		}
	}
	return false
}

// OwnerIsCurrentPrincipal reports whether owner is the account this process runs as, or the
// default owner Windows assigns to objects this process creates. Other packages that verify
// private paths (the administrator authority store) share this rule so they cannot drift.
func OwnerIsCurrentPrincipal(owner *windows.SID) (bool, error) {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return false, err
	}
	defaultOwner, err := tokenDefaultOwner(token)
	if err != nil {
		return false, err
	}
	return privateOwnerAllowed(owner, user.User.Sid, defaultOwner), nil
}

func verifyWindowsPrivatePath(path string, directory bool, currentUser *windows.SID) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.IsDir() != directory || (!directory && !info.Mode().IsRegular()) {
		return fmt.Errorf("unsafe private path")
	}
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return err
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return err
	}
	defaultOwner, err := tokenDefaultOwner(windows.GetCurrentProcessToken())
	if err != nil {
		return err
	}
	if !privateOwnerAllowed(owner, currentUser, defaultOwner) {
		return fmt.Errorf("private path owner mismatch")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return fmt.Errorf("private path requires an explicit ACL")
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			return err
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return fmt.Errorf("unsupported private path ACL entry")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.Equals(currentUser) && !sid.Equals(system) {
			return fmt.Errorf("private path ACL grants another principal")
		}
	}
	return nil
}
