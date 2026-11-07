package admin

import (
	"fmt"

	"routenest/internal/accounts"
	"routenest/internal/adminauth"
)

// legacyAdminCredential returns the pre-Phase-5 password authority exactly as
// stored. A partially populated or malformed legacy credential is deliberately
// returned to adminauth so startup fails closed instead of treating corruption
// as a fresh anonymous installation.
func legacyAdminCredential() *adminauth.LegacyCredential {
	p := accounts.LoadPool()
	accounts.PoolMu.Lock()
	defer accounts.PoolMu.Unlock()
	if p.AdminPasswordHash == "" && p.AdminPasswordSalt == "" {
		return nil
	}
	return &adminauth.LegacyCredential{
		Salt: p.AdminPasswordSalt,
		Hash: p.AdminPasswordHash,
	}
}

// markLegacyAdminMigrated replaces the old fast-password authority only after
// adminauth has durably committed the Argon2id credential. The non-hex sentinel
// keeps old binaries in "password configured" mode while making every legacy
// password verification fail, preventing an anonymous downgrade.
func markLegacyAdminMigrated() error {
	p := accounts.LoadPool()
	accounts.PoolMu.Lock()
	oldHash, oldSalt := p.AdminPasswordHash, p.AdminPasswordSalt
	p.AdminPasswordHash = adminauth.LegacyMigrationSentinel
	p.AdminPasswordSalt = adminauth.LegacyMigrationSentinel
	if err := accounts.SavePoolStrictLocked(); err != nil {
		p.AdminPasswordHash, p.AdminPasswordSalt = oldHash, oldSalt
		accounts.PoolMu.Unlock()
		return fmt.Errorf("persist legacy administrator migration sentinel: %w", err)
	}
	accounts.PoolMu.Unlock()
	return nil
}
