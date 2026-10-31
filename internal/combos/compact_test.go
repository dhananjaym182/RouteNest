package combos

import (
	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/domain"
	"testing"
)

// TestComboCompactionModelUsesNarrowestWindow ensures a combo budgets its
// compaction for the smallest candidate context, not the first entry.
func TestComboCompactionModelUsesNarrowestWindow(t *testing.T) {
	oldPool := accounts.Pool
	t.Cleanup(func() { accounts.Pool = oldPool })
	accounts.Pool = &domain.AccountPool{Models: []domain.Model{
		{ID: "wide", Context: 128000, Output: 32000, MetaLocked: true},
		{ID: "narrow", Context: 32000, Output: 8000, MetaLocked: true},
	}}
	combo := domain.ModelCombo{Entries: []domain.ComboEntry{{Model: "wide"}, {Model: "narrow"}}}
	if got := CompactionModel(combo); got.ID != "narrow" || got.Context != 32000 {
		t.Fatalf("combo compaction model = %+v, want narrow/32000", got)
	}
}
