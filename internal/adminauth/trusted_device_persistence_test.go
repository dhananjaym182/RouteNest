package adminauth_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"routenest/internal/adminauth"
	"routenest/internal/platform"
	authstore "routenest/internal/store/adminauth"
)

func trustedDeviceStore(t *testing.T) (string, *authstore.Store, *adminauth.Service, adminauth.Credentials, adminauth.Options) {
	t.Helper()
	dir := t.TempDir()
	if err := platform.EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	repo, err := authstore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	options := adminauth.Options{PasswordParams: adminauth.PasswordParams{Memory: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32}, BootstrapPassword: "one time bootstrap secret"}
	service, err := adminauth.NewService(repo, options)
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := service.CompleteBootstrap(t.Context(), options.BootstrapPassword, "permanent admin password")
	if err != nil {
		t.Fatal(err)
	}
	return dir, repo, service, credentials, options
}

func TestTrustedDeviceFileStorePersistsOnlyVerifier(t *testing.T) {
	dir, repo, service, admin, options := trustedDeviceStore(t)
	credential, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), admin.Principal, adminauth.TrustedDeviceMetadata{Name: "Desktop", Platform: "linux"}, "")
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
	credentials, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.40")
	if err != nil {
		t.Fatal(err)
	}
	state, err := repo.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	lastUse := state.TrustedDevices[credential.ID].LastUsedAt
	if lastUse.IsZero() {
		t.Fatal("device authentication did not durably record last use")
	}
	verifier := sha256.Sum256([]byte(credential.Secret))
	for _, name := range []string{authstore.SnapshotName, authstore.JournalName} {
		contents, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{credential.Secret, admin.Token, admin.CSRFToken, credentials.Token, credentials.CSRFToken, options.BootstrapPassword, "permanent admin password"} {
			if strings.Contains(string(contents), secret) {
				t.Fatalf("%s contains plaintext credential material", name)
			}
		}
		if !strings.Contains(string(contents), `"secretHash":"`+hex.EncodeToString(verifier[:])+`"`) {
			t.Fatalf("%s does not persist the SHA-256 verifier", name)
		}
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
	state, err = reopened.Load(t.Context())
	if err != nil || !state.TrustedDevices[credential.ID].LastUsedAt.Equal(lastUse) {
		t.Fatal("reopening lost the persisted device last use", err)
	}
	if _, err := restarted.Authenticate(t.Context(), credentials.Token); err != nil {
		t.Fatal("reopening lost the device session", err)
	}
	if _, err := restarted.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.40"); err != nil {
		t.Fatal("reopening lost the device verifier", err)
	}
	principal, err = restarted.Authenticate(t.Context(), admin.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.RevokeTrustedDevice(t.Context(), principal, "permanent admin password", credential.ID, "192.0.2.40"); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	finalRepo, err := authstore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = finalRepo.Close() })
	finalService, err := adminauth.NewService(finalRepo, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := finalService.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.40"); !errors.Is(err, adminauth.ErrTrustedDeviceUntrusted) {
		t.Fatal("reopening restored a revoked device", err)
	}
	if _, err := finalService.Authenticate(t.Context(), credentials.Token); !errors.Is(err, adminauth.ErrUnauthorized) {
		t.Fatal("reopening restored a revoked device session", err)
	}
}

func TestTrustedDevicePreparedEnrollmentDoesNotPublishSecret(t *testing.T) {
	dir, repo, service, admin, options := trustedDeviceStore(t)
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	faulted, err := authstore.Open(dir, authstore.Options{Fault: func(point authstore.FaultPoint) error {
		if point == authstore.BeforeSnapshotWrite {
			return errors.New("injected device persistence failure")
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = faulted.Close() })
	random := make([]byte, 96)
	for i := range random {
		random[i] = byte(i + 1)
	}
	options.Random = bytes.NewReader(random)
	service, err = adminauth.NewService(faulted, options)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), admin.Principal, adminauth.TrustedDeviceMetadata{Name: "Desktop"}, "")
	if !errors.Is(err, adminauth.ErrMutationPrepared) || credential.ID != "" || credential.Secret != "" {
		t.Fatal("prepared enrollment published device proof", err)
	}
	if _, err := service.Authenticate(t.Context(), admin.Token); !errors.Is(err, adminauth.ErrDegraded) {
		t.Fatal("prepared enrollment did not fail closed", err)
	}
	if err := faulted.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := authstore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	state, err := reopened.Load(t.Context())
	if err != nil || len(state.TrustedDevices) != 1 {
		t.Fatal("prepared enrollment did not recover deterministically", err)
	}
	id := base64.RawURLEncoding.EncodeToString(random[:32])
	secret := base64.RawURLEncoding.EncodeToString(random[32:64])
	device := state.TrustedDevices[id]
	verifier := sha256.Sum256([]byte(secret))
	if device.ID != id || device.SecretHash != hex.EncodeToString(verifier[:]) || !device.ActivatedAt.IsZero() || !device.LastUsedAt.IsZero() {
		t.Fatal("prepared enrollment did not preserve the inactive verifier")
	}
	options.Random = nil
	restarted, err := adminauth.NewService(reopened, options)
	if err != nil {
		t.Fatal(err)
	}
	if credentials, err := restarted.AuthenticateTrustedDevice(t.Context(), id, secret, "192.0.2.42"); !errors.Is(err, adminauth.ErrTrustedDeviceUntrusted) || credentials.Token != "" {
		t.Fatalf("prepared orphan authenticated after restart: %v", err)
	}
	principal, err := restarted.Authenticate(t.Context(), admin.Token)
	if err != nil {
		t.Fatal(err)
	}
	expired := device.CreatedAt.Add(5 * time.Minute)
	options.Now = func() time.Time { return expired }
	restarted, err = adminauth.NewService(reopened, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.ActivateTrustedDevice(t.Context(), principal, id); !errors.Is(err, adminauth.ErrTrustedDeviceUntrusted) {
		t.Fatalf("expired prepared orphan activated after restart: %v", err)
	}
}

func TestUnactivatedTrustedDeviceRemainsInactiveAcrossRestart(t *testing.T) {
	dir, repo, service, admin, options := trustedDeviceStore(t)
	credential, err := service.EnrollTrustedDeviceAfterAuthentication(t.Context(), admin.Principal, adminauth.TrustedDeviceMetadata{Name: "Pending desktop"}, "")
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
	state, err := reopened.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	device := state.TrustedDevices[credential.ID]
	for _, delay := range []time.Duration{0, 5 * time.Minute} {
		now := device.CreatedAt.Add(delay)
		options.Now = func() time.Time { return now }
		restarted, err := adminauth.NewService(reopened, options)
		if err != nil {
			t.Fatal(err)
		}
		if credentials, err := restarted.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.43"); !errors.Is(err, adminauth.ErrTrustedDeviceUntrusted) || credentials.Token != "" {
			t.Fatalf("pending credential authenticated after restart: %v", err)
		}
		if delay > 0 {
			principal, err := restarted.Authenticate(t.Context(), admin.Token)
			if err != nil {
				t.Fatal(err)
			}
			if err := restarted.ActivateTrustedDevice(t.Context(), principal, credential.ID); !errors.Is(err, adminauth.ErrTrustedDeviceUntrusted) {
				t.Fatalf("pending credential activated after deadline and restart: %v", err)
			}
		}
	}
	state, err = reopened.Load(t.Context())
	if err != nil || !state.TrustedDevices[credential.ID].ActivatedAt.IsZero() || !state.TrustedDevices[credential.ID].LastUsedAt.IsZero() {
		t.Fatal("pending credential changed authority across restart", err)
	}
}

func TestTrustedDevicePreparedAuthenticationKeepsDurableLastUse(t *testing.T) {
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
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Minute)
	options.Now = func() time.Time { return now }
	faulted, err := authstore.Open(dir, authstore.Options{Fault: func(point authstore.FaultPoint) error {
		if point == authstore.BeforeSnapshotWrite {
			return errors.New("injected device last-use persistence failure")
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
	credentials, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.41")
	if !errors.Is(err, adminauth.ErrMutationPrepared) || credentials.Token != "" || credentials.CSRFToken != "" {
		t.Fatal("prepared authentication published session proof", err)
	}
	if err := faulted.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := authstore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	state, err := reopened.Load(t.Context())
	if err != nil || !state.TrustedDevices[credential.ID].LastUsedAt.Equal(now) {
		t.Fatal("prepared device last use was not recovered durably", err)
	}
	service, err = adminauth.NewService(reopened, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AuthenticateTrustedDevice(t.Context(), credential.ID, credential.Secret, "192.0.2.41"); err != nil {
		t.Fatal("prepared authentication lost the device credential", err)
	}
}
