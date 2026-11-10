package adminauth_test

import (
	"errors"
	"testing"

	"routenest/internal/adminauth"
	authstore "routenest/internal/store/adminauth"
)

func TestTrustedDevicePasswordAndRecoveryRevocationsSurviveRestart(t *testing.T) {
	for _, action := range []string{"password", "recovery code", "offline recovery", "revoke all"} {
		t.Run(action, func(t *testing.T) {
			dir, repo, service, admin, options := trustedDeviceStore(t)
			code, err := service.IssueRecoveryCode(t.Context(), admin.Principal, "permanent admin password", "192.0.2.120")
			if err != nil {
				t.Fatal(err)
			}
			user, err := service.CreateLocalUser(t.Context(), admin.Principal, "operator", "operator password", []adminauth.Role{adminauth.RoleSecurityAdmin})
			if err != nil {
				t.Fatal(err)
			}
			operator, err := service.Login(t.Context(), user.Username, "operator password", "192.0.2.121")
			if err != nil {
				t.Fatal(err)
			}
			credentials := []adminauth.Credentials{admin, operator}
			devices := make([]adminauth.TrustedDeviceCredential, 0, len(credentials))
			principals := make([]adminauth.Principal, 0, len(credentials))
			tokens := []string{admin.Token, operator.Token}
			for _, login := range credentials {
				device, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), login.Principal, adminauth.TrustedDeviceMetadata{Name: "Desktop"}, "")
				if err != nil {
					t.Fatal(err)
				}
				principal, err := service.Authenticate(t.Context(), login.Token)
				if err != nil {
					t.Fatal(err)
				}
				if err := service.ActivateTrustedDevice(t.Context(), principal, device.ID); err != nil {
					t.Fatal(err)
				}
				automatic, err := service.AuthenticateTrustedDevice(t.Context(), device.ID, device.Secret, "192.0.2.122")
				if err != nil {
					t.Fatal(err)
				}
				devices = append(devices, device)
				principals = append(principals, principal)
				tokens = append(tokens, automatic.Token)
			}
			switch action {
			case "password":
				err = service.RotatePassword(t.Context(), principals[0], "permanent admin password", "replacement password")
			case "recovery code":
				_, err = service.RecoverWithCode(t.Context(), code, "replacement password", "192.0.2.123")
			case "offline recovery":
				var secret string
				err = adminauth.RecoverOffline(t.Context(), repo, adminauth.Options{PasswordParams: options.PasswordParams, DeliverBootstrap: func(value string) error { secret = value; return nil }})
				if err == nil {
					_, err = service.CompleteBootstrap(t.Context(), secret, "replacement password", "192.0.2.123")
				}
			case "revoke all":
				err = service.RevokeAllTrustedDevices(t.Context(), principals[0], "permanent admin password", "192.0.2.123")
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := authstore.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			restarted, err := adminauth.NewService(reopened, options)
			if err != nil {
				t.Fatal(err)
			}
			for index, token := range tokens {
				_, err := restarted.Authenticate(t.Context(), token)
				if action == "password" && index%2 == 1 {
					if err != nil {
						t.Fatal("password rotation revoked another user's session", err)
					}
				} else if !errors.Is(err, adminauth.ErrUnauthorized) {
					t.Fatal("revoked session survived restart", err)
				}
			}
			for index, device := range devices {
				_, err := restarted.AuthenticateTrustedDevice(t.Context(), device.ID, device.Secret, "192.0.2.124")
				if action == "password" && index == 1 {
					if err != nil {
						t.Fatal("password rotation revoked another user's device", err)
					}
				} else if !errors.Is(err, adminauth.ErrTrustedDeviceUntrusted) {
					t.Fatal("revoked device survived restart", err)
				}
			}
		})
	}
}
