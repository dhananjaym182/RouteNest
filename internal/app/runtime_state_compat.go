package app

import (
	"fmt"
	"time"

	"routenest/internal/budget"
	"routenest/internal/config"
	"routenest/internal/providers"
	"routenest/internal/providerstate"
)

// configureRuntimeState hydrates the one local runtime-state owner, imports
// active legacy cooldowns, and only then publishes the budget and availability
// services used by planning and dispatch.
func configureRuntimeState() error {
	now := time.Now
	budgetBackend, availabilityBackend, _, err := providers.NewLocalRuntimeStateBackend(now)
	if err != nil {
		return fmt.Errorf("load durable provider runtime state: %w", err)
	}

	availability, err := providerstate.NewDurableStore(availabilityBackend, now)
	if err != nil {
		return fmt.Errorf("hydrate provider availability: %w", err)
	}
	providers.Load()
	if err := providers.EnsureModelPrefixes(); err != nil {
		return fmt.Errorf("reserve provider model namespaces: %w", err)
	}
	if err := importLegacyProviderCooldowns(availability, providers.LegacyCooldowns(now())); err != nil {
		return fmt.Errorf("import legacy provider cooldowns: %w", err)
	}

	accounting := budget.NewStore(budgetBackend, providerBudgetPolicy, budget.Options{
		Now: now,
		FailurePolicySource: func() (string, bool) {
			current := config.Live().RuntimeState
			current.Normalize()
			return string(current.EffectiveOutagePolicy()), current.AcknowledgeFailOpen
		},
	})
	if err := budget.Publish(accounting); err != nil {
		return fmt.Errorf("publish provider budget store: %w", err)
	}
	if err := providerstate.Publish(availability); err != nil {
		return fmt.Errorf("publish provider availability store: %w", err)
	}
	return nil
}

func providerBudgetPolicy(providerID string) budget.Policy {
	provider := providers.Find(providerID)
	if provider == nil {
		return budget.Policy{}
	}
	return budget.Policy{
		RPMLimit: provider.RpmLimit, DailyLimit: provider.DailyLimit,
		TokenLimit: provider.TokenLimit, TokenWindow: provider.TokenWindow,
	}
}
