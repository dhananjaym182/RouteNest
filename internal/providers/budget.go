package providers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"routenest/internal/budget"
	"routenest/internal/dispatch"
	"routenest/internal/domain"
	"routenest/internal/providerstate"
)

type budgetAttemptContextKey struct{}

func providerBudgetPolicy(provider CustomProvider) budget.Policy {
	return budget.Policy{
		RPMLimit: provider.RpmLimit, DailyLimit: provider.DailyLimit,
		TokenLimit: provider.TokenLimit, TokenWindow: provider.TokenWindow,
	}
}

// providerBudgetExceeded is the lock-order-safe compatibility check used by
// legacy selection while it already holds the provider registry lock. Planning
// consumes no capacity; every real invocation is rechecked atomically by
// AdmitDispatch.
func providerBudgetExceeded(providerID, model string, rpmLimit, dailyLimit int, tokenLimit int64, tokenWindow string) bool {
	if !budget.HasDefault() {
		return false
	}
	policy := budget.Policy{
		RPMLimit: rpmLimit, DailyLimit: dailyLimit,
		TokenLimit: tokenLimit, TokenWindow: tokenWindow,
	}
	exhaustion, err := budget.Default().ExhaustionWithPolicy(context.Background(), providerID, model, policy)
	blocked := exhaustion != nil
	mirrorConfiguredBudgetObservation(providerID, model, exhaustion)
	if err != nil && policy.HardLimited() {
		return true
	}
	return blocked
}

func mirrorConfiguredBudgetObservation(providerID, model string, exhaustion *budget.ExhaustionError) {
	if !providerstate.HasDefault() || providerID == "" || model == "" {
		return
	}
	store := providerstate.Default()
	scope := domain.AvailabilityScope{ProviderID: domain.ProviderInstanceID(providerID), ModelID: model}
	current := store.Blocking(scope)
	if exhaustion != nil {
		for _, observation := range current {
			if observation.Scope == scope && observation.Kind == domain.ObservationQuotaExhausted &&
				observation.Source == "configured_window" && observation.ResetAt.Equal(exhaustion.ResetAt) {
				return
			}
		}
		_ = store.Apply(domain.AvailabilityObservation{
			Kind: domain.ObservationQuotaExhausted, Scope: scope,
			Until: exhaustion.ResetAt, ResetAt: exhaustion.ResetAt,
			Source: "configured_window", Confidence: domain.ConfidenceExplicit,
		})
		return
	}
	for _, observation := range current {
		if observation.Scope == scope && observation.Kind == domain.ObservationQuotaExhausted && observation.Source == "configured_window" {
			_ = store.Apply(domain.AvailabilityObservation{
				Kind: domain.ObservationQuotaReset, Scope: scope,
				Source: "configured_window", Confidence: domain.ConfidenceExplicit,
			})
			return
		}
	}
}

// AdmitDispatch atomically checks and records one actual custom-provider
// invocation. The caller supplies the server-owned event ID and timestamp from
// the dispatch invocation boundary.
func AdmitDispatch(ctx context.Context, provider CustomProvider, model, eventID string, at time.Time) (string, error) {
	if !budget.HasDefault() {
		return "", &budget.UnavailableError{}
	}
	receipt, err := budget.Default().CheckAndRecordWithPolicy(ctx, budget.AdmissionCommand{
		ProviderID: provider.ID, ModelID: model, AttemptID: eventID,
		At: at, Policy: providerBudgetPolicy(provider),
	})
	if err != nil {
		return receipt.AttemptID, err
	}
	return receipt.AttemptID, nil
}

// RecordDispatch remains as a temporary compatibility helper for internal
// callers that cannot yet carry an invocation event. Production dispatch paths
// use AdmitDispatch and surface failures instead of logging through them.
func RecordDispatch(providerID, model string) {
	provider := Find(providerID)
	if provider == nil {
		return
	}
	eventID, err := dispatch.GenerateID()
	if err != nil {
		log.Printf("provider %s: generate budget event: %v", providerID, err)
		return
	}
	if _, err := AdmitDispatch(context.Background(), *provider, model, eventID, time.Now()); err != nil && !errors.Is(err, budget.ErrExhausted) {
		log.Printf("provider %s: record dispatch budget: %v", providerID, err)
	}
}

// SnapshotBudgetExhaustion projects durable counters by provider and logical
// model. Backend errors fail closed only for offerings with configured limits.
func SnapshotBudgetExhaustion(offerings []domain.ModelOffering) map[domain.OfferingKey]bool {
	providersByID := make(map[string]CustomProvider)
	for _, provider := range List() {
		providersByID[provider.ID] = provider
	}
	out := make(map[domain.OfferingKey]bool)
	if !budget.HasDefault() {
		return out
	}
	for _, offering := range offerings {
		provider, ok := providersByID[string(offering.ProviderID)]
		if !ok {
			continue
		}
		policy := providerBudgetPolicy(provider)
		blocked, err := budget.Default().ExhaustedWithPolicy(context.Background(), provider.ID, offering.ModelID, policy)
		if blocked || (err != nil && policy.HardLimited()) {
			out[offering.Key()] = true
		}
	}
	return out
}

// TokenWindow reports the configured compatibility window.
func TokenWindow(providerID string) string {
	Load()
	Mu.Lock()
	defer Mu.Unlock()
	for _, provider := range CustomProviders {
		if provider.ID == providerID && provider.TokenWindow == "daily" {
			return "daily"
		}
	}
	return "monthly"
}

// RecordTokenUsage persists one final reported usage event. Replaying the same
// event is idempotent; changing its payload is rejected.
func RecordTokenUsage(providerID, attemptID, eventID string, tokens int64) error {
	if !budget.HasDefault() {
		return &budget.UnavailableError{}
	}
	return budget.Default().RecordTokens(context.Background(), providerID, attemptID, eventID, tokens)
}

// TagResponseBudgetAttempt retains internal accounting attribution without
// adding an upstream header or exposing it to downstream clients.
func TagResponseBudgetAttempt(response *http.Response, attemptID string) {
	if response == nil || attemptID == "" {
		return
	}
	request := response.Request
	if request == nil {
		request = &http.Request{}
	}
	response.Request = request.WithContext(context.WithValue(request.Context(), budgetAttemptContextKey{}, attemptID))
}

func ResponseBudgetAttempt(response *http.Response) string {
	if response == nil || response.Request == nil {
		return ""
	}
	attemptID, _ := response.Request.Context().Value(budgetAttemptContextKey{}).(string)
	return attemptID
}
