//go:build windows

package platform

import (
	"testing"

	"golang.org/x/sys/windows"
)

func wellKnownSID(t *testing.T, kind windows.WELL_KNOWN_SID_TYPE) *windows.SID {
	t.Helper()
	sid, err := windows.CreateWellKnownSid(kind)
	if err != nil {
		t.Fatal(err)
	}
	return sid
}

// An elevated process (GitHub's Windows runners) creates files owned by BUILTIN\Administrators,
// not by its user SID. The token's own default owner must be accepted; strangers must not.
func TestPrivateOwnerAllowedAcceptsOnlyUserOrTokenDefaultOwner(t *testing.T) {
	user := wellKnownSID(t, windows.WinLocalSystemSid) // any SID stands in for "the current user"
	admins := wellKnownSID(t, windows.WinBuiltinAdministratorsSid)
	everyone := wellKnownSID(t, windows.WinWorldSid)
	users := wellKnownSID(t, windows.WinBuiltinUsersSid)

	if !privateOwnerAllowed(user, user, admins) {
		t.Fatal("the current user must be allowed to own a private path")
	}
	if !privateOwnerAllowed(admins, user, admins) {
		t.Fatal("the token default owner (Administrators when elevated) must be allowed")
	}
	if privateOwnerAllowed(everyone, user, admins) || privateOwnerAllowed(users, user, admins) {
		t.Fatal("an unrelated principal must not be accepted as owner")
	}
	if privateOwnerAllowed(admins, user, nil) {
		t.Fatal("without a matching default owner, Administrators must not be accepted")
	}
	if privateOwnerAllowed(nil, user, admins) {
		t.Fatal("a missing owner must be rejected")
	}
}

// The helper must work against the real process token on the machine running the tests.
func TestTokenDefaultOwnerIsReadable(t *testing.T) {
	owner, err := tokenDefaultOwner(windows.GetCurrentProcessToken())
	if err != nil || owner == nil || !owner.IsValid() {
		t.Fatalf("token default owner = %v, %v", owner, err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	// Whichever account Windows uses, a directory this process creates must verify as private.
	dir := t.TempDir()
	if err := secureDirectoryPermissions(dir); err != nil {
		t.Fatalf("a directory created and secured by this process was rejected: %v", err)
	}
	if err := verifyWindowsPrivatePath(dir, true, user.User.Sid); err != nil {
		t.Fatalf("verification of a freshly secured directory failed: %v", err)
	}
}
