package routing

import (
	"sort"
	"strings"
	"time"

	"routenest/internal/config"
	"routenest/internal/domain"
	"routenest/internal/egress"
	"routenest/internal/providerstate"
)

// CandidateExclusionReason explains why one provider offering was omitted. The
// planner returns reasons instead of logging so callers can surface previews and
// tests can assert the exact routing decision.
type CandidateExclusionReason string

const (
	ExclusionProviderDisabled   CandidateExclusionReason = "provider_disabled"
	ExclusionProviderNotPinned  CandidateExclusionReason = "provider_not_pinned"
	ExclusionOfferingDisabled   CandidateExclusionReason = "offering_disabled"
	ExclusionOfferingDelisted   CandidateExclusionReason = "offering_delisted"
	ExclusionOfferingInactive   CandidateExclusionReason = "offering_inactive"
	ExclusionNotFreeEligible    CandidateExclusionReason = "not_free_eligible"
	ExclusionProviderCooldown   CandidateExclusionReason = "provider_cooldown"
	ExclusionModelCooldown      CandidateExclusionReason = "model_cooldown"
	ExclusionCredentialCooldown CandidateExclusionReason = "credential_cooldown"
	ExclusionEgressCooldown     CandidateExclusionReason = "egress_cooldown"
	ExclusionCredentialMissing  CandidateExclusionReason = "credential_missing"
	ExclusionCredentialExpired  CandidateExclusionReason = "credential_expired"
	ExclusionAuthExpired        CandidateExclusionReason = "auth_expired"
	ExclusionModelGone          CandidateExclusionReason = "model_gone"
	ExclusionBudgetExhausted    CandidateExclusionReason = "budget_exhausted"
	ExclusionNegativeWeight     CandidateExclusionReason = "negative_weight"
)

// CandidatePin narrows the plan to one provider, matching either the stable ID
// or the administrator-visible name. An empty pin keeps every provider.
type CandidatePin struct {
	ProviderID   domain.ProviderInstanceID
	ProviderName string
}

func (pin CandidatePin) active() bool {
	return pin.ProviderID != "" || strings.TrimSpace(pin.ProviderName) != ""
}

func (pin CandidatePin) matches(instance domain.ProviderInstance) bool {
	if !pin.active() {
		return false
	}
	if pin.ProviderID != "" && instance.ID != pin.ProviderID {
		return false
	}
	if name := strings.TrimSpace(pin.ProviderName); name != "" && !strings.EqualFold(strings.TrimSpace(instance.Name), name) {
		return false
	}
	return true
}

// CandidateQuality is a read-only quality sample for one provider/model.
type CandidateQuality struct {
	SuccessRate  float64
	AvgLatencyMS float64
	HasSamples   bool
}

// CandidateSignals are the ordering inputs that still live in legacy provider
// records. Passing them explicitly keeps the planner pure.
type CandidateSignals struct {
	Priority      int
	Weight        int
	CreatedAt     time.Time
	Quality       CandidateQuality
	WeightRank    int
	HasWeightRank bool
}

// CandidatePlanInput is a complete snapshot for one planning pass.
type CandidatePlanInput struct {
	ModelID      string
	OnlyFree     bool
	Strategy     string
	Pin          CandidatePin
	Now          time.Time
	Instances    []domain.ProviderInstance
	Offerings    []domain.ModelOffering
	Credentials  []domain.ProviderCredential
	Observations []domain.AvailabilityObservation
	Budgets      map[domain.ProviderInstanceID]bool
	// BudgetExhausted is the provider/model-scoped budget snapshot. Budgets is
	// retained during migration for compatibility with older planner callers.
	BudgetExhausted       map[domain.OfferingKey]bool
	CredentialRequired    map[domain.ProviderInstanceID]bool
	Egress                map[domain.ProviderInstanceID]egress.Decision
	Signals               map[domain.ProviderInstanceID]CandidateSignals
	OfferingSignals       map[domain.OfferingKey]CandidateSignals
	AffinityModelID       string
	PreferredProviderID   domain.ProviderInstanceID
	PreferredCredential   domain.CredentialID
	ProviderModelPrefixes map[string]string
}

// Candidate is one routable provider offering with its eligible credentials
// and resolved egress decision.
type Candidate struct {
	ProviderID      domain.ProviderInstanceID
	ProviderName    string
	AdapterID       string
	ModelID         string
	UpstreamModelID string
	Group           int
	Order           int
	Access          domain.AccessClass
	Offering        domain.ModelOffering
	Credentials     []domain.CredentialID
	Egress          egress.Decision
	Signals         CandidateSignals
	Pinned          bool
}

// CandidateExclusion records one omitted offering.
type CandidateExclusion struct {
	ProviderID domain.ProviderInstanceID
	ModelID    string
	Reason     CandidateExclusionReason
}

// CandidatePlan is the ordered result. Candidates are fallbacks in order.
type CandidatePlan struct {
	Candidates []Candidate
	Excluded   []CandidateExclusion
}

func accessRank(access domain.AccessClass, allowanceVerified bool) int {
	switch access {
	case domain.AccessFree:
		return 0
	case domain.AccessFreeTier:
		if allowanceVerified {
			return 1
		}
		// An unverified free-tier claim is treated like an unknown access
		// class so it never outranks a confirmed allowance.
		return 5
	case domain.AccessSubscription:
		return 2
	case domain.AccessTrial:
		return 3
	case domain.AccessPaidAPI:
		return 4
	default:
		return 5
	}
}

func normalizedWeight(weight int) int {
	if weight == 0 {
		return 1
	}
	return weight
}

func credentialBound(credential domain.ProviderCredential, providerID domain.ProviderInstanceID) bool {
	for _, id := range credential.ProviderIDs {
		if id == providerID {
			return true
		}
	}
	return false
}

func credentialExpired(credential domain.ProviderCredential, now time.Time) bool {
	if credential.Status != "active" {
		return false
	}
	return !credential.ExpiresAt.IsZero() && !now.Before(credential.ExpiresAt) && !credential.Refreshable
}

func credentialUsable(credential domain.ProviderCredential, modelID string, now time.Time) bool {
	if credential.Status != "active" {
		return false
	}
	if credentialExpired(credential, now) {
		return false
	}
	if until := credential.ModelCooldowns[modelID]; !until.IsZero() && now.Before(until) {
		return false
	}
	if !credential.CooldownUntil.IsZero() && now.Before(credential.CooldownUntil) {
		return false
	}
	return true
}

func blockingReason(observations []domain.AvailabilityObservation) (CandidateExclusionReason, bool) {
	for _, observation := range observations {
		switch observation.Kind {
		case domain.ObservationAuthExpired:
			return ExclusionAuthExpired, true
		case domain.ObservationModelGone:
			return ExclusionModelGone, true
		}
		switch {
		case observation.Scope.CredentialID != "":
			return ExclusionCredentialCooldown, true
		case observation.Scope.ModelID != "":
			return ExclusionModelCooldown, true
		case observation.Scope.EgressID != "":
			return ExclusionEgressCooldown, true
		default:
			return ExclusionProviderCooldown, true
		}
	}
	return "", false
}

func eligibleCredentials(input CandidatePlanInput, instance domain.ProviderInstance, offering domain.ModelOffering, egressID string) ([]domain.CredentialID, CandidateExclusionReason) {
	var bound []domain.ProviderCredential
	for _, credential := range input.Credentials {
		if credentialBound(credential, instance.ID) {
			bound = append(bound, credential)
		}
	}
	if len(bound) == 0 {
		return nil, ""
	}
	eligible := make([]domain.ProviderCredential, 0, len(bound))
	expiredCount := 0
	for _, credential := range bound {
		if !credentialUsable(credential, offering.ModelID, input.Now) {
			if credentialExpired(credential, input.Now) {
				expiredCount++
			}
			continue
		}
		blocked := false
		for _, scope := range []domain.AvailabilityScope{
			{ProviderID: instance.ID, CredentialID: credential.ID},
			{ProviderID: instance.ID, CredentialID: credential.ID, ModelID: offering.ModelID},
			{ProviderID: instance.ID, CredentialID: credential.ID, ModelID: offering.ModelID, EgressID: egressID},
		} {
			if len(providerstate.Blocks(input.Observations, scope, input.Now)) > 0 {
				blocked = true
				break
			}
		}
		if !blocked {
			eligible = append(eligible, credential)
		}
	}
	if len(eligible) == 0 {
		if expiredCount == len(bound) {
			return nil, ExclusionCredentialExpired
		}
		return nil, ExclusionCredentialMissing
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		if eligible[i].UsageCount != eligible[j].UsageCount {
			return eligible[i].UsageCount < eligible[j].UsageCount
		}
		return eligible[i].ID < eligible[j].ID
	})
	ids := make([]domain.CredentialID, len(eligible))
	for i, credential := range eligible {
		ids[i] = credential.ID
	}
	if offering.ModelID == input.AffinityModelID && input.PreferredCredential != "" {
		for index, id := range ids {
			if id != input.PreferredCredential || index == 0 {
				continue
			}
			copy(ids[1:index+1], ids[:index])
			ids[0] = id
			break
		}
	}
	return ids, ""
}

func candidateLess(a, b Candidate, input CandidatePlanInput) bool {
	if ra, rb := accessRank(a.Access, a.Offering.AllowanceVerified), accessRank(b.Access, b.Offering.AllowanceVerified); ra != rb {
		return ra < rb
	}
	aPreferred := a.ModelID == input.AffinityModelID && a.ProviderID == input.PreferredProviderID
	bPreferred := b.ModelID == input.AffinityModelID && b.ProviderID == input.PreferredProviderID
	if aPreferred != bPreferred {
		return aPreferred
	}
	switch input.Strategy {
	case "quality":
		qa, qb := a.Signals.Quality, b.Signals.Quality
		if qa.HasSamples != qb.HasSamples {
			return qa.HasSamples
		}
		if qa.SuccessRate != qb.SuccessRate {
			return qa.SuccessRate > qb.SuccessRate
		}
		if qa.AvgLatencyMS > 0 && qb.AvgLatencyMS > 0 && qa.AvgLatencyMS != qb.AvgLatencyMS {
			return qa.AvgLatencyMS < qb.AvgLatencyMS
		}
	case "weighted_round_robin", "weight", "weighted":
		if a.Signals.HasWeightRank != b.Signals.HasWeightRank {
			return a.Signals.HasWeightRank
		}
		if a.Signals.HasWeightRank && a.Signals.WeightRank != b.Signals.WeightRank {
			return a.Signals.WeightRank < b.Signals.WeightRank
		}
		if wa, wb := normalizedWeight(a.Signals.Weight), normalizedWeight(b.Signals.Weight); wa != wb {
			return wa > wb
		}
	}
	if a.Signals.Priority != b.Signals.Priority {
		return a.Signals.Priority < b.Signals.Priority
	}
	if !a.Signals.CreatedAt.Equal(b.Signals.CreatedAt) {
		return a.Signals.CreatedAt.Before(b.Signals.CreatedAt)
	}
	if a.ProviderID != b.ProviderID {
		return a.ProviderID < b.ProviderID
	}
	return a.ModelID < b.ModelID
}

func planForModel(input CandidatePlanInput, modelID string, group int) CandidatePlan {
	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}
	input.Now = now
	instances := make(map[domain.ProviderInstanceID]domain.ProviderInstance, len(input.Instances))
	for _, instance := range input.Instances {
		instances[instance.ID] = instance
	}

	plan := CandidatePlan{}
	for _, offering := range input.Offerings {
		if offering.ModelID != modelID {
			continue
		}
		exclude := func(reason CandidateExclusionReason) {
			plan.Excluded = append(plan.Excluded, CandidateExclusion{
				ProviderID: offering.ProviderID, ModelID: offering.ModelID, Reason: reason,
			})
		}
		instance, ok := instances[offering.ProviderID]
		if !ok {
			exclude(ExclusionProviderDisabled)
			continue
		}
		if input.Pin.active() && !input.Pin.matches(instance) {
			exclude(ExclusionProviderNotPinned)
			continue
		}
		if !instance.Enabled || !offering.ProviderEnabled {
			exclude(ExclusionProviderDisabled)
			continue
		}
		if offering.Disabled {
			exclude(ExclusionOfferingDisabled)
			continue
		}
		if offering.Delisted {
			exclude(ExclusionOfferingDelisted)
			continue
		}
		if offering.Status != "" && offering.Status != "active" {
			exclude(ExclusionOfferingInactive)
			continue
		}
		if input.OnlyFree && !offering.FreeOnlyEligible() {
			exclude(ExclusionNotFreeEligible)
			continue
		}
		if input.Budgets[instance.ID] || input.BudgetExhausted[offering.Key()] {
			exclude(ExclusionBudgetExhausted)
			continue
		}
		decision, hasEgress := input.Egress[instance.ID]
		if hasEgress && decision.Cooling {
			exclude(ExclusionEgressCooldown)
			continue
		}
		egressID := decision.Endpoint
		if blocked := providerstate.Blocks(input.Observations, domain.AvailabilityScope{ProviderID: instance.ID}, now); len(blocked) > 0 {
			reason, _ := blockingReason(blocked)
			exclude(reason)
			continue
		}
		if blocked := providerstate.Blocks(input.Observations, domain.AvailabilityScope{ProviderID: instance.ID, ModelID: offering.ModelID}, now); len(blocked) > 0 {
			reason, _ := blockingReason(blocked)
			exclude(reason)
			continue
		}
		if egressID != "" {
			if blocked := providerstate.Blocks(input.Observations, domain.AvailabilityScope{ProviderID: instance.ID, EgressID: egressID}, now); len(blocked) > 0 {
				reason, _ := blockingReason(blocked)
				exclude(reason)
				continue
			}
		}
		credentials, reason := eligibleCredentials(input, instance, offering, egressID)
		credentialRequired := input.CredentialRequired[instance.ID]
		if reason == "" && credentialRequired && len(credentials) == 0 {
			reason = ExclusionCredentialMissing
		}
		if reason != "" {
			exclude(reason)
			continue
		}
		signals, ok := input.OfferingSignals[offering.Key()]
		if !ok {
			signals = input.Signals[instance.ID]
		}
		if signals.Priority == 0 && instance.Priority != 0 {
			signals.Priority = instance.Priority
		}
		if signals.Weight == 0 && instance.Weight != 0 {
			signals.Weight = instance.Weight
		}
		if (input.Strategy == "weighted_round_robin" || input.Strategy == "weight" || input.Strategy == "weighted") && signals.Weight < 0 {
			exclude(ExclusionNegativeWeight)
			continue
		}
		plan.Candidates = append(plan.Candidates, Candidate{
			ProviderID:      instance.ID,
			ProviderName:    instance.Name,
			AdapterID:       instance.AdapterID,
			ModelID:         offering.ModelID,
			UpstreamModelID: offering.UpstreamModelID,
			Group:           group,
			Access:          offering.Access,
			Offering:        offering,
			Credentials:     credentials,
			Egress:          decision,
			Signals:         signals,
			Pinned:          input.Pin.active() && input.Pin.matches(instance),
		})
	}
	sort.SliceStable(plan.Candidates, func(i, j int) bool {
		return candidateLess(plan.Candidates[i], plan.Candidates[j], input)
	})
	for i := range plan.Candidates {
		plan.Candidates[i].Order = i + 1
	}
	sort.SliceStable(plan.Excluded, func(i, j int) bool {
		if plan.Excluded[i].ProviderID != plan.Excluded[j].ProviderID {
			return plan.Excluded[i].ProviderID < plan.Excluded[j].ProviderID
		}
		if plan.Excluded[i].ModelID != plan.Excluded[j].ModelID {
			return plan.Excluded[i].ModelID < plan.Excluded[j].ModelID
		}
		return plan.Excluded[i].Reason < plan.Excluded[j].Reason
	})
	return plan
}

// PlanCandidates returns the ordered candidates for input.ModelID. It performs
// no I/O and mutates neither its arguments nor package state.
func PlanCandidates(input CandidatePlanInput) CandidatePlan {
	if strings.TrimSpace(input.ModelID) == "" {
		return CandidatePlan{}
	}
	return planForModel(input, input.ModelID, 0)
}

// PlanFallbackChain plans each model in order and concatenates the candidates,
// preserving chain position as the outer fallback order.
func PlanFallbackChain(input CandidatePlanInput, chain []string) CandidatePlan {
	combined := CandidatePlan{}
	seen := map[string]bool{}
	group := 0
	for _, modelID := range chain {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" || seen[modelID] {
			continue
		}
		seen[modelID] = true
		bare, pin, err := resolveModelReference(modelID, &config.Proxy{ProviderModelPrefixes: input.ProviderModelPrefixes}, input.Instances, input.Offerings)
		if err != nil {
			group++
			continue
		}
		entryInput := input
		if pin.active() {
			entryInput.Pin = pin
		}
		plan := planForModel(entryInput, bare, group)
		combined.Candidates = append(combined.Candidates, plan.Candidates...)
		combined.Excluded = append(combined.Excluded, plan.Excluded...)
		group++
	}
	for i := range combined.Candidates {
		combined.Candidates[i].Order = i + 1
	}
	return combined
}
