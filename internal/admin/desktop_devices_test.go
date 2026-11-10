package admin

import (
	"errors"
	"testing"
	"time"

	"routenest/internal/adminauth"
	"routenest/internal/desktopauth"
)

func TestDesktopInspectionDistinguishesAbsentRegistrationFromInvalidStoredProof(t *testing.T) {
	_, runtime := newAdminAuthTestMux(t)
	setDesktopSecurityRuntimeForTest(t, runtime)
	credentials, err := runtime.service.CompleteBootstrap(t.Context(), "one time bootstrap secret", "DeviceInspection8!")
	if err != nil {
		t.Fatal(err)
	}
	device, err := runtime.service.EnrollTrustedDeviceAfterAuthentication(t.Context(), credentials.Principal, adminauth.TrustedDeviceMetadata{Name: "Inspection desktop"}, "")
	if err != nil {
		t.Fatal(err)
	}
	principal, err := runtime.service.Authenticate(t.Context(), credentials.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.service.ActivateTrustedDevice(t.Context(), principal, device.ID); err != nil {
		t.Fatal(err)
	}
	view, registered, valid, err := InspectDesktopDeviceCredential(t.Context(), device)
	if err != nil || !registered || !valid || view.UserID != adminauth.BreakGlassID {
		t.Fatalf("valid inspection flags = %t, %t, %v", registered, valid, err)
	}
	wrong := device
	wrong.Secret = device.ID
	if wrong.Secret == device.Secret {
		t.Fatal("fixture secret unexpectedly matches")
	}
	_, registered, valid, err = InspectDesktopDeviceCredential(t.Context(), wrong)
	if !errors.Is(err, desktopauth.ErrCorrupt) || !registered || valid {
		t.Fatalf("bad stored proof treated as missing: %t, %t, %v", registered, valid, err)
	}
	if _, err := runtime.service.InspectTrustedDevice(t.Context(), device.ID, device.Secret); err != nil {
		t.Fatal("inspection revoked the original valid credential", err)
	}
	principal, err = runtime.service.Authenticate(t.Context(), credentials.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.service.RevokeTrustedDevice(t.Context(), principal, "DeviceInspection8!", device.ID, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	_, registered, valid, err = InspectDesktopDeviceCredential(t.Context(), device)
	if err != nil || registered || valid {
		t.Fatalf("absent registration flags = %t, %t, %v", registered, valid, err)
	}
}

func TestDesktopExplicitSignOutPausesStoredDeviceForPasswordOnlySession(t *testing.T) {
	_, runtime := newAdminAuthTestMux(t)
	setDesktopSecurityRuntimeForTest(t, runtime)
	credentials, err := runtime.service.CompleteBootstrap(t.Context(), "one time bootstrap secret", "DeviceSignOut8!")
	if err != nil {
		t.Fatal(err)
	}
	device, err := runtime.service.EnrollTrustedDeviceAfterAuthentication(t.Context(), credentials.Principal, adminauth.TrustedDeviceMetadata{Name: "Desktop"}, "")
	if err != nil {
		t.Fatal(err)
	}
	principal, err := runtime.service.Authenticate(t.Context(), credentials.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.service.ActivateTrustedDevice(t.Context(), principal, device.ID); err != nil {
		t.Fatal(err)
	}
	manual, err := AuthenticateDesktopLocal(t.Context(), "admin", "DeviceSignOut8!")
	if err != nil || manual.Principal.DeviceID != "" {
		t.Fatal("manual fixture is not a password-only session", err)
	}
	if err := SignOutDesktopDeviceSession(t.Context(), manual, device.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.service.Authenticate(t.Context(), manual.Token); !errors.Is(err, adminauth.ErrUnauthorized) {
		t.Fatal("explicit sign-out retained password session", err)
	}
	view, err := runtime.service.InspectTrustedDevice(t.Context(), device.ID, device.Secret)
	if err != nil || view.AutomaticSignIn || !view.ExpiresAt.After(time.Now()) {
		t.Fatal("explicit password-session sign-out did not pause the device", err)
	}
}
