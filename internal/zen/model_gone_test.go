package zen

import (
	"path/filepath"
	"testing"

	"routenest/internal/accounts"
	"routenest/internal/domain"
)

func TestMarkModelGoneIsZenSourceScoped(t *testing.T) {
	oldPool, oldPath := accounts.Pool, accounts.PoolPath
	t.Cleanup(func() { accounts.Pool, accounts.PoolPath = oldPool, oldPath })
	accounts.PoolPath = filepath.Join(t.TempDir(), ".routenest-accounts.json")
	accounts.Pool = &domain.AccountPool{
		DefaultModel: "org/model@revision",
		Models: []domain.Model{
			{ID: "org/model@revision", Source: "remote", Delisted: true},
			{ID: "org/model@revision", Source: "seed", Delisted: true},
			{ID: "org/model@revision", Source: "provider", Provider: "Sibling", Delisted: true},
		},
	}

	MarkModelGone("org/model@revision")
	got := accounts.LoadPool()
	if len(got.Models) != 2 || got.Models[0].Source != "remote" || got.Models[1].Source != "provider" {
		t.Fatalf("Zen cleanup removed a sibling offering: %+v", got.Models)
	}
	if got.DefaultModel != "org/model@revision" {
		t.Fatalf("sibling-backed literal default was cleared: %q", got.DefaultModel)
	}
}
