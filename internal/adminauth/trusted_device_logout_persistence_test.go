package adminauth_test

import (
	"errors"
	"testing"

	"routenest/internal/adminauth"
	authstore "routenest/internal/store/adminauth"
)

func TestTrustedDeviceLogoutAndLockSurviveRestart(t *testing.T) {
	for _, pause := range []bool{true, false} {
		name := "lock"
		if pause {
			name = "sign out"
		}
		t.Run(name, func(t *testing.T) {
			dir, repo, service, admin, options := trustedDeviceStore(t)
			credential, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), admin.Principal, adminauth.TrustedDeviceMetadata{Name: "Desktop"}, "")
			if err != nil {
				t.Fatal(err)
			}
			principal, err := service.Authenticate(t.Context(), admin.Token)
			if err != nil {
				t.Fatal(err)
			}
			if err := service.ActivateTrustedDevice(t.Context(), principal, credential.ID); err != nil {
				t.Fatal(err)
			}
			before, err := repo.AuditEvents(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if pause {
				err = service.LogoutWithDevicePause(t.Context(), principal)
			} else {
				err = service.Logout(t.Context(), principal)
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
			state, err := reopened.Load(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(state.TrustedDevices) != 1 || state.TrustedDevices[credential.ID].AutomaticSignIn == pause {
				t.Fatal("restart lost the requested automatic sign-in state")
			}
			if _, err := restarted.Authenticate(t.Context(), admin.Token); !errors.Is(err, adminauth.ErrUnauthorized) {
				t.Fatal("ended session survived restart", err)
			}
			events, err := reopened.AuditEvents(t.Context())
			if err != nil || len(events) != len(before)+1 || events[len(before)].Kind != adminauth.AuditLogout {
				t.Fatal("sign-out did not persist exactly one logout audit", err)
			}
			_, err = restarted.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.89")
			if pause && !errors.Is(err, adminauth.ErrTrustedDeviceAutoSignInDisabled) || !pause && err != nil {
				t.Fatal("restart changed automatic sign-in behavior", err)
			}
		})
	}
}

func TestTrustedDeviceLogoutPausePersistenceFailuresAreAtomic(t *testing.T) {
	for _, point := range []authstore.FaultPoint{authstore.BeforePrepare, authstore.BeforeSnapshotWrite, authstore.AfterSnapshotRename, authstore.BeforeCommit} {
		t.Run(string(point), func(t *testing.T) {
			dir, repo, service, admin, options := trustedDeviceStore(t)
			credential, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), admin.Principal, adminauth.TrustedDeviceMetadata{Name: "Desktop"}, "")
			if err != nil {
				t.Fatal(err)
			}
			principal, err := service.Authenticate(t.Context(), admin.Token)
			if err != nil {
				t.Fatal(err)
			}
			if err := service.ActivateTrustedDevice(t.Context(), principal, credential.ID); err != nil {
				t.Fatal(err)
			}
			before, err := repo.AuditEvents(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			faulted, err := authstore.Open(dir, authstore.Options{Fault: func(current authstore.FaultPoint) error {
				if current == point {
					return errors.New("injected logout persistence failure")
				}
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = faulted.Close() })
			service, err = adminauth.NewService(faulted, options)
			if err != nil {
				t.Fatal(err)
			}
			err = service.LogoutWithDevicePause(t.Context(), principal)
			prepared := point != authstore.BeforePrepare
			if err == nil || errors.Is(err, adminauth.ErrMutationPrepared) != prepared {
				t.Fatalf("faulted sign-out = %v, prepared = %v", err, prepared)
			}
			if _, err := service.Authenticate(t.Context(), admin.Token); !errors.Is(err, adminauth.ErrDegraded) {
				t.Fatal("faulted sign-out continued serving sessions", err)
			}
			if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.90"); !errors.Is(err, adminauth.ErrDegraded) {
				t.Fatal("faulted sign-out continued automatic sign-in", err)
			}
			if err := faulted.Close(); err != nil {
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
			state, err := reopened.Load(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			_, sessionExists := state.Sessions[principal.SessionHash]
			if sessionExists == prepared || state.TrustedDevices[credential.ID].AutomaticSignIn == prepared {
				t.Fatal("restart recovered a partial sign-out mutation")
			}
			events, err := reopened.AuditEvents(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			wantEvents := len(before)
			if prepared {
				wantEvents++
			}
			if len(events) != wantEvents || prepared && events[len(before)].Kind != adminauth.AuditLogout {
				t.Fatal("restart recovered the wrong logout audit count")
			}
			_, err = restarted.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.90")
			if prepared && !errors.Is(err, adminauth.ErrTrustedDeviceAutoSignInDisabled) || !prepared && err != nil {
				t.Fatal("restart recovered inconsistent automatic sign-in state", err)
			}
		})
	}
}
