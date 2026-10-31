package routing_test

import (
	"path/filepath"
	"strings"
	"testing"

	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/admin"
	"cline-go-proxy/internal/domain"
	"cline-go-proxy/internal/routing"
)

// User-configured fallback models remain valid even when absent from the
// synchronized list; dynamically derived free chains still filter stale models.
func TestModelFallbackChainKeepsUserConfiguredOutOfList(t *testing.T) {
	oldProvider := routing.ConfigProvider
	routing.ConfigProvider = admin.GetProxyConfig
	t.Cleanup(func() { routing.ConfigProvider = oldProvider })
	oldPool, oldPath := accounts.Pool, accounts.PoolPath
	oldCfg := admin.GetProxyConfig()
	t.Cleanup(func() {
		accounts.Pool, accounts.PoolPath = oldPool, oldPath
		admin.SetProxyConfig(oldCfg)
	})
	accounts.PoolPath = filepath.Join(t.TempDir(), ".cline-accounts.json")
	accounts.Pool = &domain.AccountPool{Accounts: []*domain.Account{}, Keys: []string{}, Models: []domain.Model{
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
