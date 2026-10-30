package providers

import (
	"cline-go-proxy/internal/accounts"
	"testing"
	"time"
)

// withEmptyAccounts 清空账号池并在测试结束后恢复，避免测试间相互污染。
func withEmptyAccounts(t *testing.T) {
	t.Helper()
	p := accounts.LoadPool()
	old := p.Accounts
	p.Accounts = []*Account{}
	accounts.SavePool()
	t.Cleanup(func() {
		q := accounts.LoadPool()
		q.Accounts = old
		accounts.SavePool()
	})
}

func TestFindAccountByRefreshToken(t *testing.T) {
	withEmptyAccounts(t)

	tok := "refresh-token-abc"
	if got := accounts.FindAccountByRefreshToken(tok); got != nil {
		t.Fatalf("unknown token should not match, got %+v", got)
	}
	if accounts.AccountExists(tok) {
		t.Fatal("accounts.AccountExists should be false for unknown token")
	}

	accounts.AddAccount(&Account{
		AccountID:    "acc_test_1",
		Email:        "a@example.com",
		RefreshToken: tok,
		Status:       "active",
		CreatedAt:    time.Now(),
	})

	if got := accounts.FindAccountByRefreshToken(tok); got == nil || got.AccountID != "acc_test_1" {
		t.Fatalf("accounts.FindAccountByRefreshToken(%q) = %+v, want acc_test_1", tok, got)
	}
	// 前后空白应被忽略
	if got := accounts.FindAccountByRefreshToken("  " + tok + "\t"); got == nil || got.AccountID != "acc_test_1" {
		t.Fatalf("lookup should trim whitespace, got %+v", got)
	}
	// 空 token 永远不应命中
	if accounts.AccountExists("") || accounts.AccountExists("   ") {
		t.Fatal("empty token must never match an account")
	}
}

func TestIsDuplicateImportToken(t *testing.T) {
	withEmptyAccounts(t)

	accounts.AddAccount(&Account{
		AccountID:    "acc_existing",
		Email:        "b@example.com",
		RefreshToken: "existing-token",
		Status:       "active",
		CreatedAt:    time.Now(),
	})

	seen := make(map[string]bool)

	// 账号池中已存在 → 重复
	if !accounts.IsDuplicateImportToken("existing-token", seen) {
		t.Fatal("token already in pool should be a duplicate")
	}
	// 首次出现 → 不重复，但记入 seen
	if accounts.IsDuplicateImportToken("new-token", seen) {
		t.Fatal("first occurrence in batch should not be a duplicate")
	}
	// 同批次第二次出现 → 重复
	if !accounts.IsDuplicateImportToken("new-token", seen) {
		t.Fatal("second occurrence within the same batch should be a duplicate")
	}
	// 未见过的新 token → 不重复
	if accounts.IsDuplicateImportToken("another-token", seen) {
		t.Fatal("unseen new token should not be a duplicate")
	}
}
