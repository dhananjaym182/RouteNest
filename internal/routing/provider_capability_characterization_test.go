package routing

import (
	"path/filepath"
	"testing"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/domain"
	"routenest/internal/providerstate"
)

func TestCharacterizationClineQuotaResetBody(t *testing.T) {
	tests := []struct {
		name string
		body string
		want time.Duration
	}{
		{name: "hours and minutes", body: `{"error":"quota","message":"Try again in 1h 1m"}`, want: 61 * time.Minute},
		{name: "minutes after zero hours", body: `Try again in 0h 15m`, want: 15 * time.Minute},
		{name: "unparseable falls back", body: `quota exceeded`, want: time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := time.Now()
			got := parseCooldownUntil(tt.body)
			after := time.Now()
			if got.Before(before.Add(tt.want)) || got.After(after.Add(tt.want)) {
				t.Fatalf("reset = %s, want within [%s, %s]", got, before.Add(tt.want), after.Add(tt.want))
			}
		})
	}
}

func TestSetModelCooldownMirrorsCredentialOfferingState(t *testing.T) {
	oldPool, oldPath := accounts.Pool, accounts.PoolPath
	accounts.PoolPath = filepath.Join(t.TempDir(), ".routenest-accounts.json")
	account := &domain.Account{AccountID: "a1", Email: "a@example.com", Status: "active"}
	accounts.Pool = &domain.AccountPool{Accounts: []*domain.Account{account}}
	t.Cleanup(func() { accounts.Pool, accounts.PoolPath = oldPool, oldPath })

	store := providerstate.NewStore(time.Now)
	if err := providerstate.Publish(store); err != nil {
		t.Fatal(err)
	}
	SetModelCooldown(account, "model", time.Now().Add(time.Minute))
	reasons := store.Blocking(domain.AvailabilityScope{
		ProviderID: "cline", CredentialID: "cline:a1", ModelID: "model",
	})
	if len(reasons) != 1 || reasons[0].Kind != domain.ObservationRateLimited {
		t.Fatalf("shadow state = %+v", reasons)
	}
}
