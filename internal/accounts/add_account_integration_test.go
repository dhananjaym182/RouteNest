package accounts_test

import (
	"path/filepath"
	"testing"
	"time"

	"cline-go-proxy/internal/accounts"
)

// AddAccount deduplicates by email, updating credentials in place while
// preserving identity and accumulated statistics.
func TestAddAccountUpsertsByEmail(t *testing.T) {
	oldPool, oldPath := accounts.Pool, accounts.PoolPath
	t.Cleanup(func() { accounts.Pool, accounts.PoolPath = oldPool, oldPath })
	accounts.PoolPath = filepath.Join(t.TempDir(), ".cline-accounts.json")
	oldAcc := &Account{AccountID: "acc_old", Email: "user@example.com", RefreshToken: "old-rt", Status: "expired", UsageCount: 100, TotalTokens: 5000, CreatedAt: time.Now().Add(-72 * time.Hour)}
	accounts.Pool = &AccountPool{Accounts: []*Account{oldAcc}, Keys: []string{}}

	accounts.AddAccount(&Account{AccountID: "acc_new", Email: "user@example.com", RefreshToken: "new-rt", Status: "active", UsageCount: 5})

	p := accounts.LoadPool()
	if len(p.Accounts) != 1 {
		t.Fatalf("accounts = %d, want 1 (in-place re-auth, no duplicate)", len(p.Accounts))
	}
	got := p.Accounts[0]
	if got.AccountID != "acc_old" {
		t.Errorf("AccountID = %q, want original preserved", got.AccountID)
	}
	if got.Status != "active" || got.RefreshToken != "new-rt" {
		t.Errorf("credentials not updated: status=%s rt=%s", got.Status, got.RefreshToken)
	}
	if got.UsageCount != 100 {
		t.Errorf("UsageCount = %d, want original 100 preserved", got.UsageCount)
	}

	dup := &Account{AccountID: "acc_dup", Email: "USER@example.com", RefreshToken: "dup-rt", Status: "active", UsageCount: 7}
	accounts.Pool.Accounts = append(accounts.Pool.Accounts, dup)
	accounts.AddAccount(&Account{AccountID: "acc_x", Email: "user@example.com", RefreshToken: "latest-rt", Status: "active"})

	p = accounts.LoadPool()
	if len(p.Accounts) != 1 {
		t.Fatalf("accounts = %d, want 1 (duplicates merged)", len(p.Accounts))
	}
	got = p.Accounts[0]
	if got.UsageCount != 107 {
		t.Errorf("UsageCount = %d, want 107 (100 + 7 merged)", got.UsageCount)
	}
	if got.RefreshToken != "latest-rt" {
		t.Errorf("RefreshToken = %q, want latest-rt", got.RefreshToken)
	}
}
