package admin

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"routenest/internal/adminauth"
	"routenest/internal/platform"
	storeadminauth "routenest/internal/store/adminauth"
)

func newRecoveryAuthority(t *testing.T) (string, *storeadminauth.Store, *adminauth.Service) {
	t.Helper()
	dir := t.TempDir()
	if err := platform.EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	repo, err := storeadminauth.Open(filepath.Join(dir, "admin-auth"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	service, err := adminauth.NewService(repo, adminauth.Options{
		PasswordParams:    adminauth.PasswordParams{Memory: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32},
		BootstrapPassword: "bootstrap recovery fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	return dir, repo, service
}

func TestOfflineRecoveryCallerRequiresDowntimeAndPreservesAudit(t *testing.T) {
	dir, repo, service := newRecoveryAuthority(t)
	credentials, err := service.CompleteBootstrap(context.Background(), "bootstrap recovery fixture", "permanent recovery fixture")
	if err != nil {
		t.Fatal(err)
	}
	before, err := repo.AuditEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	delivered := ""
	deliver := func(secret string) error { delivered = secret; return nil }
	if err := recoverSecurityOffline(context.Background(), dir, deliver); !errors.Is(err, storeadminauth.ErrLocked) {
		t.Fatalf("running authority accepted recovery: %v", err)
	}
	if delivered != "" {
		t.Fatal("recovery secret delivered while server owned the authority")
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	if err := recoverSecurityOffline(context.Background(), dir, deliver); err != nil {
		t.Fatal(err)
	}
	if delivered == "" {
		t.Fatal("private recovery delivery missing")
	}
	reopened, err := storeadminauth.OpenForRecovery(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	state, err := reopened.Load(context.Background())
	if err != nil || !state.Initialized || state.RecoveryPHC == "" || len(state.Sessions) != 0 || state.Users[adminauth.BreakGlassID].PasswordPHC != "" {
		t.Fatalf("durable recovery state invalid: %+v, %v", state, err)
	}
	after, err := reopened.AuditEvents(context.Background())
	if err != nil || len(after) != len(before)+1 || after[len(after)-1].Kind != adminauth.AuditOfflineRecovery {
		t.Fatalf("recovery audit missing or history replaced: %v, %v", after, err)
	}
	for i := range before {
		if after[i] != before[i] {
			t.Fatal("recovery replaced previous audit history")
		}
	}
	for _, name := range []string{storeadminauth.SnapshotName, storeadminauth.JournalName} {
		data, err := os.ReadFile(filepath.Join(dir, "admin-auth", name))
		if err != nil || bytes.Contains(data, []byte(delivered)) {
			t.Fatalf("plaintext recovery secret in %s: %v", name, err)
		}
	}
	restarted, err := adminauth.NewService(reopened, adminauth.Options{BootstrapPassword: "environment cannot reset recovery"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Authenticate(context.Background(), credentials.Token); !errors.Is(err, adminauth.ErrUnauthorized) {
		t.Fatalf("old session survived recovery: %v", err)
	}
	if _, err := restarted.CompleteBootstrap(context.Background(), "environment cannot reset recovery", "new permanent recovery password"); !errors.Is(err, adminauth.ErrUnauthorized) {
		t.Fatalf("bootstrap option replaced durable recovery secret: %v", err)
	}
	if _, err := restarted.CompleteBootstrap(context.Background(), delivered, "new permanent recovery password"); err != nil {
		t.Fatal(err)
	}
}

func TestSecuritySetupStateSeparatesBootstrapAndOfflineRecovery(t *testing.T) {
	_, repo, service := newRecoveryAuthority(t)
	adminSecurityState.Lock()
	previous := adminSecurityState.runtime
	adminSecurityState.runtime = &adminSecurityRuntime{service: service, store: repo}
	adminSecurityState.Unlock()
	t.Cleanup(func() {
		adminSecurityState.Lock()
		adminSecurityState.runtime = previous
		adminSecurityState.Unlock()
	})

	status, err := SecuritySetupState()
	if err != nil || !status.BootstrapRequired || status.RecoveryRequired {
		t.Fatalf("fresh setup state = %+v, %v", status, err)
	}
	if _, err := service.CompleteBootstrap(context.Background(), "bootstrap recovery fixture", "permanent recovery fixture"); err != nil {
		t.Fatal(err)
	}
	status, err = SecuritySetupState()
	if err != nil || status.BootstrapRequired || status.RecoveryRequired {
		t.Fatalf("initialized setup state = %+v, %v", status, err)
	}
	if err := adminauth.RecoverOffline(context.Background(), repo, adminauth.Options{
		PasswordParams:   adminauth.PasswordParams{Memory: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32},
		DeliverBootstrap: func(string) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	status, err = SecuritySetupState()
	if err != nil || status.BootstrapRequired || !status.RecoveryRequired {
		t.Fatalf("offline recovery setup state = %+v, %v", status, err)
	}
}

func TestOfflineRecoveryCallerRefusesFreshBootstrapAndMissingDelivery(t *testing.T) {
	dir, repo, _ := newRecoveryAuthority(t)
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	if err := recoverSecurityOffline(context.Background(), dir, nil); err == nil {
		t.Fatal("recovery accepted missing private delivery")
	}
	called := false
	err := recoverSecurityOffline(context.Background(), dir, func(string) error { called = true; return nil })
	if !errors.Is(err, adminauth.ErrNotInitialized) || called {
		t.Fatalf("fresh bootstrap recovery accepted: %v, delivered=%v", err, called)
	}
}

func TestRecoveryDataDirResolutionDoesNotCreateOrTighten(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	t.Setenv(platform.DataDirEnv, dir)
	t.Setenv(platform.LegacyDataDirEnv, filepath.Join(t.TempDir(), "legacy"))
	got, err := recoveryDataDir()
	if err != nil || got != dir {
		t.Fatalf("recovery directory = %q, %v", got, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("recovery resolver created directory: %v", err)
	}
	t.Setenv(platform.DataDirEnv, "")
	got, err = recoveryDataDir()
	if err != nil || got != os.Getenv(platform.LegacyDataDirEnv) {
		t.Fatalf("legacy recovery directory = %q, %v", got, err)
	}
}

func TestOfflineRecoveryDeliveryFailureKeepsLockRecoverable(t *testing.T) {
	dir, repo, service := newRecoveryAuthority(t)
	if _, err := service.CompleteBootstrap(context.Background(), "bootstrap recovery fixture", "permanent recovery fixture"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("private terminal disconnected")
	if err := recoverSecurityOffline(context.Background(), dir, func(string) error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("delivery failure hidden: %v", err)
	}
	delivered := false
	if err := recoverSecurityOffline(context.Background(), dir, func(string) error { delivered = true; return nil }); err != nil || !delivered {
		t.Fatalf("recovery could not be retried after delivery failure: %v", err)
	}
}
