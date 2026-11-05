package app

import (
	"time"

	"routenest/internal/providerstate"
)

// configureProviderState publishes the provider-neutral shadow state. Legacy
// stores remain authoritative until their source-specific migration tasks.
func configureProviderState() error {
	return providerstate.Publish(providerstate.NewStore(time.Now))
}
