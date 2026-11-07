package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"

	"routenest/internal/accounts"
	"routenest/internal/adminauth"
	"routenest/internal/domain"
	poolstore "routenest/internal/store/accounts"
)

func installLegacyAdminPool(t *testing.T, pool *domain.AccountPool) {
	t.Helper()
	accounts.PoolMu.Lock()
	oldPool, oldPath := accounts.Pool, accounts.PoolPath
	accounts.Pool = pool
	accounts.PoolPath = filepath.Join(t.TempDir(), ".routenest-accounts.json")
	accounts.PoolMu.Unlock()
	t.Cleanup(func() {
		accounts.PoolMu.Lock()
		accounts.Pool, accounts.PoolPath = oldPool, oldPath
		accounts.PoolMu.Unlock()
	})
}

func TestLegacyAdminCredentialPreservesCorruptAuthorityForFailClosedValidation(t *testing.T) {
	installLegacyAdminPool(t, &domain.AccountPool{AdminPasswordHash: "hash-without-salt"})
	got := legacyAdminCredential()
	if got == nil || got.Hash != "hash-without-salt" || got.Salt != "" {
		t.Fatalf("legacyAdminCredential() = %#v", got)
	}
	if adminauth.ValidLegacy(*got) {
		t.Fatal("partial legacy authority must not validate")
	}
}

func TestMarkLegacyAdminMigratedPersistsUnmatchableSentinel(t *testing.T) {
	legacySalt := "00112233445566778899aabbccddeeff"
	sum := sha256.Sum256([]byte(legacySalt + "old-password"))
	legacyHash := hex.EncodeToString(sum[:])
	installLegacyAdminPool(t, &domain.AccountPool{
		AdminPasswordHash: legacyHash,
		AdminPasswordSalt: legacySalt,
	})

	if err := markLegacyAdminMigrated(); err != nil {
		t.Fatal(err)
	}
	if adminauth.VerifyLegacy(adminauth.LegacyCredential{
		Hash: adminauth.LegacyMigrationSentinel,
		Salt: adminauth.LegacyMigrationSentinel,
	}, "old-password") {
		t.Fatal("migration sentinel must never match through the legacy verifier")
	}

	persisted, err := poolstore.Load(accounts.PoolPath)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.AdminPasswordHash != adminauth.LegacyMigrationSentinel || persisted.AdminPasswordSalt != adminauth.LegacyMigrationSentinel {
		t.Fatalf("persisted sentinel = hash %q salt %q", persisted.AdminPasswordHash, persisted.AdminPasswordSalt)
	}
}
