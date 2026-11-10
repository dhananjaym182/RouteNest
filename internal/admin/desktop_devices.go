package admin

import (
	"context"
	"errors"

	"routenest/internal/adminauth"
	"routenest/internal/desktopauth"
)

func InspectDesktopDeviceCredential(ctx context.Context, credential adminauth.TrustedDeviceCredential) (adminauth.TrustedDeviceView, bool, bool, error) {
	runtime, err := configuredAdminSecurity()
	if err != nil {
		return adminauth.TrustedDeviceView{}, false, false, err
	}
	view, err := runtime.service.InspectTrustedDevice(ctx, credential.ID, credential.Secret)
	if err == nil {
		return view, true, true, nil
	}
	if errors.Is(err, adminauth.ErrTrustedDeviceExpired) {
		return view, true, false, nil
	}
	if errors.Is(err, adminauth.ErrTrustedDeviceNotFound) {
		return adminauth.TrustedDeviceView{}, false, false, nil
	}
	if errors.Is(err, adminauth.ErrTrustedDeviceUntrusted) {
		return adminauth.TrustedDeviceView{}, true, false, desktopauth.ErrCorrupt
	}
	return adminauth.TrustedDeviceView{}, false, false, err
}

func SignOutDesktopDeviceSession(ctx context.Context, credentials adminauth.Credentials, id string) error {
	runtime, err := configuredAdminSecurity()
	if err != nil {
		return err
	}
	credentials, err = authenticateDesktopCredentials(ctx, runtime, credentials)
	if err != nil {
		return err
	}
	return runtime.service.LogoutWithTrustedDevicePause(ctx, credentials.Principal, id)
}

func ListDesktopDevices(ctx context.Context, credentials adminauth.Credentials) ([]adminauth.TrustedDeviceView, error) {
	runtime, err := configuredAdminSecurity()
	if err != nil {
		return nil, err
	}
	principal, err := runtime.service.Authenticate(ctx, credentials.Token)
	if err != nil {
		return nil, err
	}
	return runtime.service.ListTrustedDevices(ctx, principal)
}

func SetDesktopDeviceAutoSignIn(ctx context.Context, credentials adminauth.Credentials, currentPassword, deviceID string, enabled bool) error {
	runtime, err := configuredAdminSecurity()
	if err != nil {
		return err
	}
	principal, err := runtime.service.Authenticate(ctx, credentials.Token)
	if err != nil {
		return err
	}
	return runtime.service.SetTrustedDeviceAutoSignIn(ctx, principal, currentPassword, deviceID, enabled, "127.0.0.1")
}

func RevokeDesktopDevice(ctx context.Context, credentials adminauth.Credentials, currentPassword, deviceID string) error {
	runtime, err := configuredAdminSecurity()
	if err != nil {
		return err
	}
	principal, err := runtime.service.Authenticate(ctx, credentials.Token)
	if err != nil {
		return err
	}
	return runtime.service.RevokeTrustedDevice(ctx, principal, currentPassword, deviceID, "127.0.0.1")
}

func RevokeEveryDesktopDevice(ctx context.Context, credentials adminauth.Credentials, currentPassword string) error {
	runtime, err := configuredAdminSecurity()
	if err != nil {
		return err
	}
	principal, err := runtime.service.Authenticate(ctx, credentials.Token)
	if err != nil {
		return err
	}
	return runtime.service.RevokeAllTrustedDevices(ctx, principal, currentPassword, "127.0.0.1")
}
