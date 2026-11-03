package providers

import (
	"testing"
	"time"

	"routenest/internal/domain"
	"routenest/internal/providerstate"
)

func TestSetCooldownMirrorsProviderState(t *testing.T) {
	installProviders(t, &CustomProvider{ID: "p", Name: "P"})
	store := providerstate.NewStore(time.Now)
	if err := providerstate.Publish(store); err != nil {
		t.Fatal(err)
	}
	SetCooldown("p", "m", time.Now().Add(time.Minute))
	reasons := store.Blocking(domain.AvailabilityScope{ProviderID: "p", ModelID: "m"})
	if len(reasons) != 1 || reasons[0].Kind != domain.ObservationRateLimited {
		t.Fatalf("shadow state = %+v", reasons)
	}

	SetCooldown("p", "m", time.Time{})
	if reasons := store.Blocking(domain.AvailabilityScope{ProviderID: "p", ModelID: "m"}); len(reasons) != 0 {
		t.Fatalf("reset did not recover shadow state: %+v", reasons)
	}
}

func TestConfiguredBudgetExhaustionMirrorsQuotaReset(t *testing.T) {
	installProviders(t, &CustomProvider{
		ID: "budget", Name: "Budget", Enabled: true, ModelIDs: []string{"m"},
		TokenLimit: 100, TokenWindow: "daily",
	})
	budgetMu.Lock()
	oldTokens := providerTokens
	providerTokens = map[string]map[string]int64{
		"budget": {time.Now().UTC().Format("2006-01-02"): 100},
	}
	budgetMu.Unlock()
	t.Cleanup(func() {
		budgetMu.Lock()
		providerTokens = oldTokens
		budgetMu.Unlock()
	})

	store := providerstate.NewStore(time.Now)
	if err := providerstate.Publish(store); err != nil {
		t.Fatal(err)
	}
	if got := ResolveForModel("m"); len(got) != 0 {
		t.Fatalf("exhausted provider remained eligible: %+v", got)
	}
	reasons := store.Blocking(domain.AvailabilityScope{ProviderID: "budget", ModelID: "m"})
	if len(reasons) != 1 || reasons[0].Kind != domain.ObservationQuotaExhausted || reasons[0].ResetAt.IsZero() || reasons[0].Source != "configured_window" {
		t.Fatalf("quota shadow state = %+v", reasons)
	}
}
