package integration

import (
	"cline-go-proxy/internal/admin"
	"cline-go-proxy/internal/routing"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cline-go-proxy/internal/accounts"
)

// 用户显式配置的回退链不做列表过滤：列表外但实测仍可用的模型允许配置；
// 动态派生的免费链仍按 routing.LiveModelSet 过滤下架条目。
func TestModelFallbackChainKeepsUserConfiguredOutOfList(t *testing.T) {
	oldPool, oldPath := accounts.Pool, accounts.PoolPath
	oldCfg := admin.GetProxyConfig()
	t.Cleanup(func() {
		accounts.Pool, accounts.PoolPath = oldPool, oldPath
		admin.SetProxyConfig(oldCfg)
	})
	accounts.PoolPath = filepath.Join(t.TempDir(), ".cline-accounts.json")
	accounts.Pool = &AccountPool{Accounts: []*Account{}, Keys: []string{}, Models: []Model{
		{ID: "cline-free/gemini-3.8-flash", Source: "remote", Cost: "free", Status: "active"},
	}}
	admin.SetProxyConfig(&admin.ProxyConfig{ModelChain: []string{"out-of-list/still-works", "cline-free/gemini-3.8-flash", "out-of-list/still-works"}})

	chain := routing.ModelFallbackChain("requested-model")
	got := strings.Join(chain, ",")
	if !strings.Contains(got, "out-of-list/still-works") {
		t.Errorf("user-configured out-of-list model must be kept in the chain, got: %s", got)
	}
	if strings.Count(got, "out-of-list/still-works") != 1 {
		t.Errorf("chain should be deduped, got: %s", got)
	}
	if chain[0] != "requested-model" {
		t.Errorf("requested model must stay first, got: %s", got)
	}
}

// accounts.AddAccount 按邮箱去重：同邮箱原地更新凭据（保留 AccountID/统计），不产生
// 重复条目；已存在的同邮箱重复条目合并移除。
func TestAddAccountUpsertsByEmail(t *testing.T) {
	oldPool, oldPath := accounts.Pool, accounts.PoolPath
	t.Cleanup(func() { accounts.Pool, accounts.PoolPath = oldPool, oldPath })
	accounts.PoolPath = filepath.Join(t.TempDir(), ".cline-accounts.json")
	oldAcc := &Account{AccountID: "acc_old", Email: "user@example.com", RefreshToken: "old-rt", Status: "expired", UsageCount: 100, TotalTokens: 5000, CreatedAt: time.Now().Add(-72 * time.Hour)}
	accounts.Pool = &AccountPool{Accounts: []*Account{oldAcc}, Keys: []string{}}

	// 同邮箱重新授权：原地更新，不新增
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

	// 已存在的重复条目：accounts.AddAccount 合并移除
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
