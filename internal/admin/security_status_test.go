package admin

import (
	"context"
	"testing"

	"routenest/internal/adminauth"
	"routenest/internal/domain"
)

func TestConfigPasswordStatusUsesDurableAuthority(t *testing.T) {
	installLegacyAdminPool(t, &domain.AccountPool{})
	_, runtime := newAdminAuthTestMux(t)
	adminSecurityState.Lock()
	previous := adminSecurityState.runtime
	adminSecurityState.runtime = runtime
	adminSecurityState.Unlock()
	t.Cleanup(func() {
		adminSecurityState.Lock()
		adminSecurityState.runtime = previous
		adminSecurityState.Unlock()
	})
	if adminConfig.HasPassword() {
		t.Fatal("pending bootstrap reported a permanent password")
	}
	if _, err := runtime.service.CompleteBootstrap(context.Background(), "one time bootstrap secret", "permanent admin password"); err != nil {
		t.Fatal(err)
	}
	if !adminConfig.HasPassword() {
		t.Fatal("fresh durable password was hidden by the empty legacy pool")
	}
	if err := adminauth.RecoverOffline(context.Background(), runtime.store, adminauth.Options{
		PasswordParams:   adminauth.PasswordParams{Memory: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32},
		DeliverBootstrap: func(string) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	if adminConfig.HasPassword() {
		t.Fatal("pending recovery reported a permanent password")
	}
	if err := runtime.store.Close(); err != nil {
		t.Fatal(err)
	}
	if adminConfig.HasPassword() {
		t.Fatal("unavailable durable authority reported a password")
	}
}

func TestConfigPasswordStatusDoesNotTrustLegacyHashWithoutRuntime(t *testing.T) {
	installLegacyAdminPool(t, &domain.AccountPool{AdminPasswordHash: adminauth.LegacyMigrationSentinel})
	adminSecurityState.Lock()
	previous := adminSecurityState.runtime
	adminSecurityState.runtime = nil
	adminSecurityState.Unlock()
	t.Cleanup(func() {
		adminSecurityState.Lock()
		adminSecurityState.runtime = previous
		adminSecurityState.Unlock()
	})
	if adminConfig.HasPassword() {
		t.Fatal("legacy migration sentinel was treated as a current password")
	}
}
