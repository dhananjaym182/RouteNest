package routing

import (
	"reflect"
	"testing"
	"time"

	"routenest/internal/config"
	"routenest/internal/domain"
	"routenest/internal/egress"
	"routenest/internal/providers"
)

func planInstance(id string, priority, weight int) domain.ProviderInstance {
	return domain.ProviderInstance{
		ID: domain.ProviderInstanceID(id), Name: id, Enabled: true,
		Priority: priority, Weight: weight,
	}
}

func planOffering(providerID, modelID string, access domain.AccessClass) domain.ModelOffering {
	return domain.ModelOffering{
		ProviderID: domain.ProviderInstanceID(providerID), ModelID: modelID,
		ProviderEnabled: true, Status: "active", Access: access,
	}
}

func planInput(modelID string) CandidatePlanInput {
	return CandidatePlanInput{
		ModelID:  modelID,
		Strategy: "round_robin",
		Now:      time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC),
		Instances: []domain.ProviderInstance{
			planInstance("a", 1, 0),
			planInstance("b", 10, 0),
			planInstance("c", 2, 0),
		},
		Offerings: []domain.ModelOffering{
			planOffering("a", "m", domain.AccessPaidAPI),
			planOffering("b", "m", domain.AccessFree),
			planOffering("c", "m", domain.AccessSubscription),
		},
	}
}

func candidateIDs(plan CandidatePlan) []string {
	out := make([]string, len(plan.Candidates))
	for i, candidate := range plan.Candidates {
		out[i] = string(candidate.ProviderID)
	}
	return out
}

func TestPlanCandidatesFreeFirst(t *testing.T) {
	plan := PlanCandidates(planInput("m"))
	if got := candidateIDs(plan); !reflect.DeepEqual(got, []string{"b", "c", "a"}) {
		t.Fatalf("free-first order = %v", got)
	}
	if len(plan.Excluded) != 0 {
		t.Fatalf("unexpected exclusions = %+v", plan.Excluded)
	}
}

func TestPlanCandidatesRequiresVerifiedFreeTier(t *testing.T) {
	verified := planOffering("verified-tier", "m", domain.AccessFreeTier)
	verified.AllowanceVerified = true
	unverified := planOffering("unverified-tier", "m", domain.AccessFreeTier)
	input := CandidatePlanInput{
		ModelID: "m",
		Instances: []domain.ProviderInstance{
			planInstance("verified-tier", 5, 0),
			planInstance("unverified-tier", 0, 0),
			planInstance("subscription", 3, 0),
			planInstance("paid", 1, 0),
		},
		Offerings: []domain.ModelOffering{
			verified,
			unverified,
			planOffering("subscription", "m", domain.AccessSubscription),
			planOffering("paid", "m", domain.AccessPaidAPI),
		},
	}
	if got := candidateIDs(PlanCandidates(input)); !reflect.DeepEqual(got, []string{"verified-tier", "subscription", "paid", "unverified-tier"}) {
		t.Fatalf("verified free-tier order = %v", got)
	}
}

func TestPlanCandidatesOnlyFree(t *testing.T) {
	input := planInput("m")
	input.OnlyFree = true
	plan := PlanCandidates(input)
	if got := candidateIDs(plan); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("only-free candidates = %v", got)
	}
	if len(plan.Excluded) != 2 {
		t.Fatalf("only-free exclusions = %+v", plan.Excluded)
	}
	for _, exclusion := range plan.Excluded {
		if exclusion.Reason != ExclusionNotFreeEligible {
			t.Fatalf("exclusion reason = %q", exclusion.Reason)
		}
	}
}

func TestPlanCandidatesPinByIDAndName(t *testing.T) {
	byName := planInput("m")
	byName.Pin = CandidatePin{ProviderName: "a"}
	if got := candidateIDs(PlanCandidates(byName)); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("name pin = %v", got)
	}
	byID := planInput("m")
	byID.Pin = CandidatePin{ProviderID: "c"}
	plan := PlanCandidates(byID)
	if got := candidateIDs(plan); !reflect.DeepEqual(got, []string{"c"}) || !plan.Candidates[0].Pinned {
		t.Fatalf("id pin = %+v", plan.Candidates)
	}
	if len(plan.Excluded) != 2 {
		t.Fatalf("pin exclusions = %+v", plan.Excluded)
	}
}

func TestPlanCandidatesQualityAndWeightStrategies(t *testing.T) {
	input := planInput("m")
	for i := range input.Offerings {
		input.Offerings[i].Access = domain.AccessUnknown
	}
	input.Strategy = "quality"
	input.Signals = map[domain.ProviderInstanceID]CandidateSignals{
		"a": {Quality: CandidateQuality{SuccessRate: 0.2, AvgLatencyMS: 10, HasSamples: true}},
		"b": {Quality: CandidateQuality{SuccessRate: 0.9, AvgLatencyMS: 90, HasSamples: true}},
		"c": {Quality: CandidateQuality{SuccessRate: 0.5, AvgLatencyMS: 5}},
	}
	if got := candidateIDs(PlanCandidates(input)); !reflect.DeepEqual(got, []string{"b", "a", "c"}) {
		t.Fatalf("quality order = %v", got)
	}

	input.Strategy = "weighted_round_robin"
	input.Signals = map[domain.ProviderInstanceID]CandidateSignals{
		"a": {Priority: 1, Weight: 1},
		"b": {Priority: 10, Weight: 5},
		"c": {Priority: 2, Weight: 2},
	}
	if got := candidateIDs(PlanCandidates(input)); !reflect.DeepEqual(got, []string{"b", "c", "a"}) {
		t.Fatalf("weight order = %v", got)
	}

	input.Signals["c"] = CandidateSignals{Priority: 2, Weight: -1}
	plan := PlanCandidates(input)
	if got := candidateIDs(plan); !reflect.DeepEqual(got, []string{"b", "a"}) {
		t.Fatalf("negative weight candidates = %v", got)
	}
	if len(plan.Excluded) != 1 || plan.Excluded[0].Reason != ExclusionNegativeWeight {
		t.Fatalf("negative weight exclusions = %+v", plan.Excluded)
	}
}

func TestPlanCandidatesPriorityUsesSignalsAndDeterministicTies(t *testing.T) {
	input := planInput("m")
	for i := range input.Offerings {
		input.Offerings[i].Access = domain.AccessUnknown
	}
	same := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	input.Signals = map[domain.ProviderInstanceID]CandidateSignals{
		"a": {Priority: 5, CreatedAt: same},
		"b": {Priority: 5, CreatedAt: same},
		"c": {Priority: 1, CreatedAt: same},
	}
	if got := candidateIDs(PlanCandidates(input)); !reflect.DeepEqual(got, []string{"c", "a", "b"}) {
		t.Fatalf("tie order = %v", got)
	}
}

func TestPlanCandidatesExcludesCooldownScopes(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	until := now.Add(time.Minute)
	base := func() []domain.AvailabilityObservation {
		return []domain.AvailabilityObservation{
			{Kind: domain.ObservationRateLimited, Scope: domain.AvailabilityScope{ProviderID: "a"}, Until: until, Source: "test", Confidence: domain.ConfidenceExplicit},
			{Kind: domain.ObservationRateLimited, Scope: domain.AvailabilityScope{ProviderID: "b", ModelID: "m"}, Until: until, Source: "test", Confidence: domain.ConfidenceExplicit},
		}
	}

	input := planInput("m")
	input.Observations = base()
	plan := PlanCandidates(input)
	if got := candidateIDs(plan); !reflect.DeepEqual(got, []string{"c"}) {
		t.Fatalf("scope candidates = %v", got)
	}
	if len(plan.Excluded) != 2 {
		t.Fatalf("scope exclusions = %+v", plan.Excluded)
	}

	input = planInput("m")
	input.Egress = map[domain.ProviderInstanceID]egress.Decision{
		"a": {Mode: egress.ModeProfile, Endpoint: "global:0", Cooling: true},
	}
	plan = PlanCandidates(input)
	if got := candidateIDs(plan); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("egress cooling candidates = %v", got)
	}
	if len(plan.Excluded) != 1 || plan.Excluded[0].Reason != ExclusionEgressCooldown {
		t.Fatalf("egress exclusion = %+v", plan.Excluded)
	}
}

func TestPlanCandidatesCredentialAvailability(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	input := planInput("m")
	input.Instances = []domain.ProviderInstance{planInstance("a", 1, 0)}
	input.Offerings = []domain.ModelOffering{planOffering("a", "m", domain.AccessUnknown)}
	input.Credentials = []domain.ProviderCredential{
		{ID: "active", ProviderIDs: []domain.ProviderInstanceID{"a"}, Kind: domain.CredentialOAuthAccount, Status: "active"},
		{ID: "cooling", ProviderIDs: []domain.ProviderInstanceID{"a"}, Kind: domain.CredentialOAuthAccount, Status: "active",
			ModelCooldowns: map[string]time.Time{"m": now.Add(time.Minute)}},
		{ID: "expired", ProviderIDs: []domain.ProviderInstanceID{"a"}, Kind: domain.CredentialOAuthAccount, Status: "active",
			ExpiresAt: now.Add(-time.Minute)},
		{ID: "reauth", ProviderIDs: []domain.ProviderInstanceID{"a"}, Kind: domain.CredentialOAuthAccount, Status: "reauth_required"},
	}
	plan := PlanCandidates(input)
	if len(plan.Candidates) != 1 {
		t.Fatalf("candidates = %+v", plan.Candidates)
	}
	if got := plan.Candidates[0].Credentials; !reflect.DeepEqual(got, []domain.CredentialID{"active"}) {
		t.Fatalf("eligible credentials = %v", got)
	}

	// Credentials only for another provider must not require a credential here.
	input.Credentials = []domain.ProviderCredential{
		{ID: "other", ProviderIDs: []domain.ProviderInstanceID{"other"}, Kind: domain.CredentialAPIKey, Status: "active"},
	}
	if plan := PlanCandidates(input); len(plan.Candidates) != 1 || len(plan.Candidates[0].Credentials) != 0 {
		t.Fatalf("unbound provider = %+v", plan)
	}

	// All bound credentials unavailable: the provider is not a candidate.
	input.Credentials = []domain.ProviderCredential{
		{ID: "cooling", ProviderIDs: []domain.ProviderInstanceID{"a"}, Kind: domain.CredentialOAuthAccount, Status: "active",
			CooldownUntil: now.Add(time.Minute)},
	}
	plan = PlanCandidates(input)
	if len(plan.Candidates) != 0 || len(plan.Excluded) != 1 || plan.Excluded[0].Reason != ExclusionCredentialMissing {
		t.Fatalf("all-cooling plan = %+v", plan)
	}

	// Credential-level state expires lazily with input.Now.
	input.Now = now.Add(2 * time.Minute)
	if plan := PlanCandidates(input); len(plan.Candidates) != 1 {
		t.Fatalf("expired credential state = %+v", plan)
	}

	// An expired-only pool is distinguished from missing or cooling state.
	input.Now = now
	input.Credentials = []domain.ProviderCredential{
		{ID: "expired", ProviderIDs: []domain.ProviderInstanceID{"a"}, Kind: domain.CredentialOAuthAccount, Status: "active",
			ExpiresAt: now.Add(-time.Minute)},
	}
	plan = PlanCandidates(input)
	if len(plan.Candidates) != 0 || len(plan.Excluded) != 1 || plan.Excluded[0].Reason != ExclusionCredentialExpired {
		t.Fatalf("expired-only plan = %+v", plan)
	}
}

func TestPlanCandidatesSessionAffinityPrefersProviderAndCredential(t *testing.T) {
	input := planInput("m")
	for i := range input.Offerings {
		input.Offerings[i].Access = domain.AccessUnknown
	}
	input.AffinityModelID = "m"
	input.PreferredProviderID = "b"
	input.PreferredCredential = "b-second"
	input.Credentials = []domain.ProviderCredential{
		{ID: "b-first", ProviderIDs: []domain.ProviderInstanceID{"b"}, Status: "active", UsageCount: 0},
		{ID: "b-second", ProviderIDs: []domain.ProviderInstanceID{"b"}, Status: "active", UsageCount: 10},
	}
	plan := PlanCandidates(input)
	if got := candidateIDs(plan); !reflect.DeepEqual(got, []string{"b", "a", "c"}) {
		t.Fatalf("affinity provider order = %v", got)
	}
	if got := plan.Candidates[0].Credentials; !reflect.DeepEqual(got, []domain.CredentialID{"b-second", "b-first"}) {
		t.Fatalf("affinity credential order = %v", got)
	}
	freeFirst := planInput("m")
	freeFirst.AffinityModelID = "m"
	freeFirst.PreferredProviderID = "a"
	if got := candidateIDs(PlanCandidates(freeFirst)); !reflect.DeepEqual(got, []string{"b", "c", "a"}) {
		t.Fatalf("affinity overrode free-first order = %v", got)
	}
}

func TestPlanCandidatesBudgetAndDisabledOfferings(t *testing.T) {
	input := planInput("m")
	input.Budgets = map[domain.ProviderInstanceID]bool{"a": true}
	plan := PlanCandidates(input)
	if got := candidateIDs(plan); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("budget candidates = %v", got)
	}

	input = planInput("m")
	input.Offerings[0].Disabled = true
	input.Offerings[1].Delisted = true
	input.Instances[2].Enabled = false
	plan = PlanCandidates(input)
	if len(plan.Candidates) != 0 {
		t.Fatalf("disabled plan = %+v", plan.Candidates)
	}
	if len(plan.Excluded) != 3 {
		t.Fatalf("disabled exclusions = %+v", plan.Excluded)
	}
}

func TestPlanFallbackChainPreservesModelOrder(t *testing.T) {
	input := planInput("primary")
	input.Offerings = []domain.ModelOffering{
		planOffering("a", "primary", domain.AccessUnknown),
		planOffering("b", "primary", domain.AccessUnknown),
		planOffering("a", "fallback", domain.AccessUnknown),
	}
	plan := PlanFallbackChain(input, []string{"primary", "fallback", "primary", ""})
	if len(plan.Candidates) != 3 {
		t.Fatalf("chain candidates = %+v", plan.Candidates)
	}
	got := []string{
		string(plan.Candidates[0].ModelID) + ":" + string(plan.Candidates[0].ProviderID),
		string(plan.Candidates[1].ModelID) + ":" + string(plan.Candidates[1].ProviderID),
		string(plan.Candidates[2].ModelID) + ":" + string(plan.Candidates[2].ProviderID),
	}
	want := []string{"primary:a", "primary:b", "fallback:a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("chain order = %v", got)
	}
	if groups := []int{plan.Candidates[0].Group, plan.Candidates[1].Group, plan.Candidates[2].Group}; !reflect.DeepEqual(groups, []int{0, 0, 1}) {
		t.Fatalf("chain groups = %v", groups)
	}
	if orders := []int{plan.Candidates[0].Order, plan.Candidates[1].Order, plan.Candidates[2].Order}; !reflect.DeepEqual(orders, []int{1, 2, 3}) {
		t.Fatalf("chain orders = %v", orders)
	}
}

func TestPlanCandidatesCarriesUpstreamModelAndAdapterMetadata(t *testing.T) {
	input := planInput("m")
	input.Instances = []domain.ProviderInstance{{ID: "a", Name: "Alpha", AdapterID: "openaicompat", Enabled: true}}
	offering := planOffering("a", "m", domain.AccessUnknown)
	offering.UpstreamModelID = "vendor/model-v2"
	input.Offerings = []domain.ModelOffering{offering}
	plan := PlanCandidates(input)
	if len(plan.Candidates) != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	candidate := plan.Candidates[0]
	if candidate.ModelID != "m" || candidate.UpstreamModelID != "vendor/model-v2" || candidate.AdapterID != "openaicompat" || candidate.ProviderName != "Alpha" {
		t.Fatalf("candidate metadata = %+v", candidate)
	}
}

func TestPlanCandidatesBudgetIsProviderModelScoped(t *testing.T) {
	input := CandidatePlanInput{
		ModelID:   "m1",
		Instances: []domain.ProviderInstance{{ID: "a", Enabled: true}},
		Offerings: []domain.ModelOffering{
			planOffering("a", "m1", domain.AccessUnknown),
			planOffering("a", "m2", domain.AccessUnknown),
		},
		BudgetExhausted: map[domain.OfferingKey]bool{{ProviderID: "a", ModelID: "m2"}: true},
	}
	if plan := PlanCandidates(input); len(plan.Candidates) != 1 || plan.Candidates[0].ModelID != "m1" {
		t.Fatalf("m1 plan = %+v", plan)
	}
	input.ModelID = "m2"
	plan := PlanCandidates(input)
	if len(plan.Candidates) != 0 || len(plan.Excluded) != 1 || plan.Excluded[0].Reason != ExclusionBudgetExhausted {
		t.Fatalf("m2 plan = %+v", plan)
	}
}

func TestPlanCandidatesRequiredCredentialCannotBecomePublic(t *testing.T) {
	input := CandidatePlanInput{
		ModelID:            "m",
		Instances:          []domain.ProviderInstance{{ID: "cline", Enabled: true}},
		Offerings:          []domain.ModelOffering{planOffering("cline", "m", domain.AccessUnknown)},
		CredentialRequired: map[domain.ProviderInstanceID]bool{"cline": true},
	}
	plan := PlanCandidates(input)
	if len(plan.Candidates) != 0 || len(plan.Excluded) != 1 || plan.Excluded[0].Reason != ExclusionCredentialMissing {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestPlanCandidatesDoesNotMutateInput(t *testing.T) {
	input := planInput("m")
	input.Offerings = append(input.Offerings, planOffering("b", "other", domain.AccessUnknown))
	before := CandidatePlanInput{
		ModelID:      input.ModelID,
		OnlyFree:     input.OnlyFree,
		Strategy:     input.Strategy,
		Pin:          input.Pin,
		Now:          input.Now,
		Instances:    append([]domain.ProviderInstance(nil), input.Instances...),
		Offerings:    append([]domain.ModelOffering(nil), input.Offerings...),
		Credentials:  append([]domain.ProviderCredential(nil), input.Credentials...),
		Observations: append([]domain.AvailabilityObservation(nil), input.Observations...),
	}
	_ = PlanCandidates(input)
	if !reflect.DeepEqual(input.Instances, before.Instances) || !reflect.DeepEqual(input.Offerings, before.Offerings) {
		t.Fatal("planner mutated input slices")
	}
}

func TestPlannerMatchesLegacyProviderOrder(t *testing.T) {
	oldConfig := testProxyConfig
	setProxyConfig(defaultProxyConfig())
	t.Cleanup(func() { setProxyConfig(oldConfig) })

	oldProviderConfig := providers.ConfigProvider
	providers.ConfigProvider = func() *config.Proxy { return testProxyConfig }
	t.Cleanup(func() { providers.ConfigProvider = oldProviderConfig })

	restore := installRoutingProviders(t,
		&providers.CustomProvider{ID: "p_low", Name: "Low", ModelIDs: []string{"m"}, Enabled: true, Priority: 1, CreatedAt: time.Unix(1, 0)},
		&providers.CustomProvider{ID: "p_high", Name: "High", ModelIDs: []string{"m"}, Enabled: true, Priority: 9, CreatedAt: time.Unix(2, 0)},
		&providers.CustomProvider{ID: "p_mid", Name: "Mid", ModelIDs: []string{"m"}, Enabled: true, Priority: 5, CreatedAt: time.Unix(3, 0)},
	)
	defer restore()

	instances := []domain.ProviderInstance{
		planInstance("p_low", 1, 0),
		planInstance("p_high", 9, 0),
		planInstance("p_mid", 5, 0),
	}
	offerings := []domain.ModelOffering{
		planOffering("p_low", "m", domain.AccessUnknown),
		planOffering("p_high", "m", domain.AccessUnknown),
		planOffering("p_mid", "m", domain.AccessUnknown),
	}
	signals := map[domain.ProviderInstanceID]CandidateSignals{
		"p_low":  {Priority: 1, CreatedAt: time.Unix(1, 0)},
		"p_high": {Priority: 9, CreatedAt: time.Unix(2, 0)},
		"p_mid":  {Priority: 5, CreatedAt: time.Unix(3, 0)},
	}
	plan := PlanCandidates(CandidatePlanInput{
		ModelID: "m", Strategy: "round_robin", Instances: instances,
		Offerings: offerings, Signals: signals,
	})

	legacy := providers.ResolveForModel("m")
	legacyIDs := make([]string, len(legacy))
	for i, provider := range legacy {
		legacyIDs[i] = provider.ID
	}
	if got := candidateIDs(plan); !reflect.DeepEqual(got, legacyIDs) {
		t.Fatalf("planner order %v != legacy %v", got, legacyIDs)
	}
}

func installRoutingProviders(t *testing.T, list ...*providers.CustomProvider) func() {
	t.Helper()
	providers.Mu.Lock()
	oldProviders := providers.CustomProviders
	oldLoaded := providers.Loaded
	oldCooldowns := providers.Cooldowns
	providers.CustomProviders = append([]*providers.CustomProvider(nil), list...)
	providers.Loaded = true
	providers.Cooldowns = map[string]time.Time{}
	providers.Mu.Unlock()
	return func() {
		providers.Mu.Lock()
		providers.CustomProviders = oldProviders
		providers.Loaded = oldLoaded
		providers.Cooldowns = oldCooldowns
		providers.Mu.Unlock()
	}
}
