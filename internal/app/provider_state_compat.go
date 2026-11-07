package app

import (
	"fmt"
	"strconv"

	"routenest/internal/domain"
	"routenest/internal/providers"
	"routenest/internal/providerstate"
)

// importLegacyProviderCooldowns projects the still-supported cooldown file into
// normalized durable observations before the store becomes visible to routing.
func importLegacyProviderCooldowns(store *providerstate.Store, cooldowns []providers.LegacyCooldown) error {
	for _, cooldown := range cooldowns {
		scope := domain.AvailabilityScope{
			ProviderID: domain.ProviderInstanceID(cooldown.ProviderID),
			ModelID:    cooldown.ModelID,
		}
		observation := domain.AvailabilityObservation{
			Kind: domain.ObservationRateLimited, Scope: scope, Until: cooldown.Until.UTC(),
			Source: "legacy_provider_cooldown", Confidence: domain.ConfidenceFallback,
		}
		eventID := fmt.Sprintf("legacy-cooldown:%s:%s:%s", cooldown.ProviderID, cooldown.ModelID, strconv.FormatInt(cooldown.Until.UTC().UnixNano(), 10))
		if err := store.ApplyEvent(eventID, observation); err != nil {
			return err
		}
	}
	return nil
}
