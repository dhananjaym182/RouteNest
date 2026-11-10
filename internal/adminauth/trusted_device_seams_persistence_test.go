package adminauth_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"routenest/internal/adminauth"
	authstore "routenest/internal/store/adminauth"
)

func TestExplicitDeviceLogoutAndInspectionSurviveRestart(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "committed"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			dir, repo, service, admin, options := trustedDeviceStore(t)
			device, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), admin.Principal, adminauth.TrustedDeviceMetadata{Name: "Desktop"}, "")
			if err != nil {
				t.Fatal(err)
			}
			principal, err := service.Authenticate(t.Context(), admin.Token)
			if err != nil {
				t.Fatal(err)
			}
			if err := service.ActivateTrustedDevice(t.Context(), principal, device.ID); err != nil {
				t.Fatal(err)
			}
			passwordSession, err := service.Login(t.Context(), adminauth.BreakGlassUsername, "permanent admin password", "192.0.2.132")
			if err != nil {
				t.Fatal(err)
			}
			before, err := repo.AuditEvents(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if prepared {
				if err := repo.Close(); err != nil {
					t.Fatal(err)
				}
				repo, err = authstore.Open(dir, authstore.Options{Fault: func(point authstore.FaultPoint) error {
					if point == authstore.BeforeSnapshotWrite {
						return errors.New("injected explicit-device logout failure")
					}
					return nil
				}})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = repo.Close() })
				service, err = adminauth.NewService(repo, options)
				if err != nil {
					t.Fatal(err)
				}
			}
			err = service.LogoutWithTrustedDevicePause(t.Context(), passwordSession.Principal, device.ID)
			if prepared && !errors.Is(err, adminauth.ErrMutationPrepared) || !prepared && err != nil {
				t.Fatal("explicit-device logout failed unexpectedly", err)
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
			if _, err := restarted.Authenticate(t.Context(), passwordSession.Token); !errors.Is(err, adminauth.ErrUnauthorized) {
				t.Fatal("ended unbound session survived restart", err)
			}
			if _, err := restarted.Authenticate(t.Context(), admin.Token); err != nil {
				t.Fatal("explicit-device logout revoked another session", err)
			}
			if _, err := restarted.AuthenticateTrustedDevice(t.Context(), device.ID, device.Secret, "192.0.2.133"); !errors.Is(err, adminauth.ErrTrustedDeviceAutoSignInDisabled) {
				t.Fatal("unbound sign-out allowed automatic login after restart", err)
			}
			events, err := reopened.AuditEvents(t.Context())
			if err != nil || len(events) != len(before)+1 || events[len(before)].Kind != adminauth.AuditLogout {
				t.Fatal("explicit-device logout did not persist one audit", err)
			}
			evidence := map[string][]byte{}
			for _, name := range []string{authstore.SnapshotName, authstore.JournalName} {
				evidence[name], err = os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					t.Fatal(err)
				}
			}
			view, err := restarted.InspectTrustedDevice(t.Context(), device.ID, device.Secret)
			if err != nil || view.ID != device.ID || view.AutomaticSignIn || view.LastUsedAt != nil {
				t.Fatal("paused credential inspection failed after restart", err)
			}
			for name, before := range evidence {
				after, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil || string(before) != string(after) {
					t.Fatal("inspection rewrote persisted authority", err)
				}
			}
		})
	}
}
