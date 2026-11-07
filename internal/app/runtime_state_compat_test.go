package app

import (
	"testing"
	"time"

	"routenest/internal/domain"
	"routenest/internal/providers"
	"routenest/internal/providerstate"
)

func TestLegacyCooldownImportIsDurableStoreIdempotent(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	store := providerstate.NewStore(func() time.Time { return now })
	cooldowns := []providers.LegacyCooldown{
		{ProviderID: "p", ModelID: "m1", Until: now.Add(time.Hour)},
		{ProviderID: "p", ModelID: "m2", Until: now.Add(2 * time.Hour)},
	}
	if err := importLegacyProviderCooldowns(store, cooldowns); err != nil {
		t.Fatal(err)
	}
	if err := importLegacyProviderCooldowns(store, cooldowns); err != nil {
		t.Fatal(err)
	}
	observations := store.Snapshot(domain.ProviderInstanceID("p"))
	if len(observations) != 2 {
		t.Fatalf("observations = %+v", observations)
	}
	for _, observation := range observations {
		if observation.Kind != domain.ObservationRateLimited || observation.Source != "legacy_provider_cooldown" ||
			observation.Confidence != domain.ConfidenceFallback {
			t.Fatalf("legacy projection = %+v", observation)
		}
	}
}
