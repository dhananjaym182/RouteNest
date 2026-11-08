package routing

import (
	"fmt"
	"maps"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/config"
	"routenest/internal/credentials"
	"routenest/internal/dispatch"
	"routenest/internal/domain"
	"routenest/internal/egress"
	"routenest/internal/models"
	"routenest/internal/providers"
	"routenest/internal/providerstate"
)

// SnapshotOptions are the request-scoped policy inputs that are not owned by
// provider stores. Every other planner input is captured exactly once.
type SnapshotOptions struct {
	ModelID     string
	OnlyFree    bool
	Strategy    string
	Pin         CandidatePin
	AffinityKey string
	Now         time.Time
}

// PlanDiagnostics is safe to expose in shadow comparisons and request logs.
type PlanDiagnostics struct {
	RequestedModel string
	Candidates     []string
	Exclusions     []CandidateExclusion
}

var plannerWeightState = struct {
	sync.Mutex
	current map[domain.OfferingKey]float64
}{current: map[domain.OfferingKey]float64{}}

func resolveCandidatePin(instances []domain.ProviderInstance, raw string) (CandidatePin, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return CandidatePin{}, nil
	}
	for _, instance := range instances {
		if string(instance.ID) == raw {
			return CandidatePin{ProviderID: instance.ID}, nil
		}
	}
	if providers.GeneratedProviderIDPin(raw) || config.ProviderModelIDReserved(raw) {
		return CandidatePin{ProviderID: domain.ProviderInstanceID(raw)}, nil
	}
	matches := make([]domain.ProviderInstanceID, 0, 1)
	for _, instance := range instances {
		if strings.EqualFold(strings.TrimSpace(instance.Name), raw) {
			matches = append(matches, instance.ID)
		}
	}
	if len(matches) == 0 {
		return CandidatePin{}, fmt.Errorf("provider pin %q does not match a configured provider", raw)
	}
	if len(matches) > 1 {
		sort.Slice(matches, func(i, j int) bool { return matches[i] < matches[j] })
		return CandidatePin{}, fmt.Errorf("provider pin %q is ambiguous across provider IDs %v", raw, matches)
	}
	return CandidatePin{ProviderID: matches[0]}, nil
}

func snapshotEgress(instances []domain.ProviderInstance, custom []providers.CustomProvider) (map[domain.ProviderInstanceID]egress.Decision, error) {
	if !egress.HasDefault() {
		return nil, fmt.Errorf("routing snapshot: egress resolver is not configured")
	}
	customByID := make(map[domain.ProviderInstanceID]providers.CustomProvider, len(custom))
	for _, provider := range custom {
		customByID[domain.ProviderInstanceID(provider.ID)] = provider
	}
	out := make(map[domain.ProviderInstanceID]egress.Decision, len(instances))
	for _, instance := range instances {
		var override egress.Override
		switch instance.ID {
		case "opencode":
			override.ProfileID = "zen"
		default:
			if provider, ok := customByID[instance.ID]; ok {
				override.ProxyURL = provider.ProxyURL
			}
		}
		var target *url.URL
		if instance.BaseURL != "" {
			target, _ = url.Parse(instance.BaseURL)
		}
		decision, err := egress.Default().Resolve(instance.ID, override, target)
		if err != nil {
			return nil, err
		}
		out[instance.ID] = decision
	}
	return out, nil
}

func snapshotCredentialPolicy(custom []providers.CustomProvider) map[domain.ProviderInstanceID]bool {
	out := map[domain.ProviderInstanceID]bool{"cline": true}
	for _, provider := range custom {
		if provider.APIKey != "" || provider.ConnectionID != "" || len(provider.ConnectionIDs) > 0 {
			out[domain.ProviderInstanceID(provider.ID)] = true
		}
	}
	return out
}

func normalizeCredentialSnapshot(snapshot []domain.ProviderCredential, custom []providers.CustomProvider) []domain.ProviderCredential {
	hasZenSession := false
	connectionBound := make(map[domain.ProviderInstanceID]bool, len(custom))
	for _, provider := range custom {
		if provider.ConnectionID != "" || len(provider.ConnectionIDs) > 0 {
			connectionBound[domain.ProviderInstanceID(provider.ID)] = true
		}
	}
	for _, credential := range snapshot {
		if credential.Kind != domain.CredentialSession {
			continue
		}
		for _, providerID := range credential.ProviderIDs {
			if providerID == "opencode" {
				hasZenSession = true
			}
		}
	}
	out := make([]domain.ProviderCredential, 0, len(snapshot))
	for _, credential := range snapshot {
		if hasZenSession && credential.ID == "opencode:public" && credential.Kind != domain.CredentialSession {
			continue
		}
		if credential.Kind == domain.CredentialAPIKey {
			skip := false
			for _, providerID := range credential.ProviderIDs {
				if connectionBound[providerID] {
					skip = true
				}
			}
			if skip {
				continue
			}
		}
		out = append(out, credential)
	}
	return out
}

func baselineOfferingLess(a, b domain.ModelOffering, instances map[domain.ProviderInstanceID]domain.ProviderInstance, created map[domain.ProviderInstanceID]time.Time) bool {
	ai, bi := instances[a.ProviderID], instances[b.ProviderID]
	if ai.Priority != bi.Priority {
		return ai.Priority < bi.Priority
	}
	if !created[a.ProviderID].Equal(created[b.ProviderID]) {
		return created[a.ProviderID].Before(created[b.ProviderID])
	}
	return a.ProviderID < b.ProviderID
}

func snapshotWeightRanks(offerings []domain.ModelOffering, instances []domain.ProviderInstance, created map[domain.ProviderInstanceID]time.Time) map[domain.OfferingKey]int {
	instancesByID := make(map[domain.ProviderInstanceID]domain.ProviderInstance, len(instances))
	for _, instance := range instances {
		instancesByID[instance.ID] = instance
	}
	byModel := make(map[string][]domain.ModelOffering)
	for _, offering := range offerings {
		instance, ok := instancesByID[offering.ProviderID]
		if !ok || instance.Weight < 0 {
			continue
		}
		byModel[offering.ModelID] = append(byModel[offering.ModelID], offering)
	}
	out := make(map[domain.OfferingKey]int)
	plannerWeightState.Lock()
	defer plannerWeightState.Unlock()
	for _, group := range byModel {
		sort.SliceStable(group, func(i, j int) bool {
			return baselineOfferingLess(group[i], group[j], instancesByID, created)
		})
		total := 0
		best := 0
		bestValue := 0.0
		for i, offering := range group {
			weight := instancesByID[offering.ProviderID].Weight
			if weight == 0 {
				weight = 1
			}
			total += weight
			key := offering.Key()
			plannerWeightState.current[key] += float64(weight)
			if i == 0 || plannerWeightState.current[key] > bestValue {
				best = i
				bestValue = plannerWeightState.current[key]
			}
		}
		if len(group) == 0 || total == 0 {
			continue
		}
		plannerWeightState.current[group[best].Key()] -= float64(total)
		out[group[best].Key()] = 0
		rank := 1
		for i, offering := range group {
			if i == best {
				continue
			}
			out[offering.Key()] = rank
			rank++
		}
	}
	if len(plannerWeightState.current) > 10000 {
		plannerWeightState.current = map[domain.OfferingKey]float64{}
	}
	return out
}

func snapshotSignals(strategy string, instances []domain.ProviderInstance, offerings []domain.ModelOffering, custom []providers.CustomProvider) map[domain.OfferingKey]CandidateSignals {
	created := make(map[domain.ProviderInstanceID]time.Time, len(custom))
	for _, provider := range custom {
		created[domain.ProviderInstanceID(provider.ID)] = provider.CreatedAt
	}
	instancesByID := make(map[domain.ProviderInstanceID]domain.ProviderInstance, len(instances))
	for _, instance := range instances {
		instancesByID[instance.ID] = instance
	}
	var ranks map[domain.OfferingKey]int
	if strategy == "weighted_round_robin" || strategy == "weight" || strategy == "weighted" {
		ranks = snapshotWeightRanks(offerings, instances, created)
	}
	out := make(map[domain.OfferingKey]CandidateSignals, len(offerings))
	for _, offering := range offerings {
		instance := instancesByID[offering.ProviderID]
		rate, latency, hasSamples := providers.SnapshotQuality(string(offering.ProviderID), offering.ModelID)
		signal := CandidateSignals{
			Priority: instance.Priority, Weight: instance.Weight, CreatedAt: created[offering.ProviderID],
			Quality: CandidateQuality{SuccessRate: rate, AvgLatencyMS: latency, HasSamples: hasSamples},
		}
		if rank, ok := ranks[offering.Key()]; ok {
			signal.WeightRank = rank
			signal.HasWeightRank = true
		}
		out[offering.Key()] = signal
	}
	return out
}

// ensureLegacyClineOfferings preserves the compatibility contract that an
// explicitly requested or configured model may still be served by Cline even
// when it is absent from the last synced catalogue. The synthetic offering is
// request-local and never mutates or persists the model list.
func ensureLegacyClineOfferings(input *CandidatePlanInput, modelIDs []string) {
	if input == nil || len(modelIDs) == 0 {
		return
	}
	var cline domain.ProviderInstance
	for _, instance := range input.Instances {
		if instance.ID == "cline" {
			cline = instance
			break
		}
	}
	if !cline.ID.Valid() {
		return
	}
	removed := models.OfferingRemovalReservations()
	existing := make(map[domain.OfferingKey]bool, len(input.Offerings))
	offered := make(map[string]bool, len(input.Offerings))
	for _, offering := range input.Offerings {
		existing[offering.Key()] = true
		offered[offering.ModelID] = true
	}
	catalog := make(map[string]domain.Model)
	for _, model := range AllModels() {
		catalog[model.ID] = model
	}
	for _, raw := range modelIDs {
		modelID, pin, err := resolveModelReference(raw, &config.Proxy{ProviderModelPrefixes: input.ProviderModelPrefixes}, input.Instances, input.Offerings)
		if err != nil || pin.active() && pin.ProviderID != "cline" {
			continue
		}
		if modelID == "" {
			continue
		}
		key := domain.OfferingKey{ProviderID: "cline", ModelID: modelID}
		if removed[key] {
			continue
		}
		if existing[key] {
			continue
		}
		if offered[modelID] {
			continue
		}
		model, ok := catalog[modelID]
		if ok && (model.Source == models.SourceZen || model.Source == models.SourceSeed) {
			continue
		}
		if !ok {
			model = domain.Model{ID: modelID, Source: models.SourceRemote, Status: "active"}
		} else {
			model.Source = models.SourceRemote
			if model.Status == "" {
				model.Status = "active"
			}
		}
		projected := models.ProjectOfferings([]domain.Model{model}, []models.OfferingSource{{Instance: cline}})
		if len(projected) == 0 {
			continue
		}
		input.Offerings = append(input.Offerings, projected[0])
		existing[key] = true
		offered[modelID] = true
	}
}

// BuildCandidateSnapshot captures all mutable routing inputs once. It performs
// no upstream network calls and returns independently owned slices and maps.
func BuildCandidateSnapshot(options SnapshotOptions) (CandidatePlanInput, error) {
	instances, err := providers.SnapshotInstances()
	if err != nil {
		return CandidatePlanInput{}, err
	}
	offerings, err := ModelOfferings()
	if err != nil {
		return CandidatePlanInput{}, err
	}
	credentialSnapshot, err := credentials.Default().Snapshot()
	if err != nil {
		return CandidatePlanInput{}, err
	}
	custom := providers.List()
	egressSnapshot, err := snapshotEgress(instances, custom)
	if err != nil {
		return CandidatePlanInput{}, err
	}
	cfg := liveConfig()
	modelID, parsedPin, err := resolveModelReference(options.ModelID, cfg, instances, offerings)
	if err != nil {
		return CandidatePlanInput{}, err
	}
	pin := options.Pin
	if parsedPin.active() {
		if !pin.active() {
			pin = parsedPin
		} else if pin.ProviderID != parsedPin.ProviderID {
			return CandidatePlanInput{}, fmt.Errorf("model reference %q conflicts with its provider pin", options.ModelID)
		}
	}
	now := options.Now
	if now.IsZero() {
		now = time.Now()
	}
	input := CandidatePlanInput{
		ModelID: modelID, OnlyFree: options.OnlyFree, Strategy: options.Strategy,
		Pin: pin, Now: now, Instances: instances, Offerings: offerings,
		Credentials:           normalizeCredentialSnapshot(credentialSnapshot, custom),
		Observations:          providerstate.Default().Snapshot(""),
		BudgetExhausted:       providers.SnapshotBudgetExhaustion(offerings),
		CredentialRequired:    snapshotCredentialPolicy(custom),
		Egress:                egressSnapshot,
		OfferingSignals:       snapshotSignals(options.Strategy, instances, offerings, custom),
		ProviderModelPrefixes: maps.Clone(cfg.ProviderModelPrefixes),
	}
	if options.AffinityKey != "" {
		input.AffinityModelID = modelID
		input.PreferredProviderID = providers.PreferredProviderForSession(options.AffinityKey, modelID)
		if account, ok := accounts.PickAffinityAccount(options.AffinityKey, modelID); ok {
			input.PreferredCredential = domain.ClineCredentialID(account.AccountID, account.Email)
		}
	}
	ensureLegacyClineOfferings(&input, []string{modelID})
	return input, nil
}

func diagnosticsFor(requested string, plan CandidatePlan) PlanDiagnostics {
	diagnostics := PlanDiagnostics{RequestedModel: requested, Exclusions: append([]CandidateExclusion(nil), plan.Excluded...)}
	for _, candidate := range plan.Candidates {
		diagnostics.Candidates = append(diagnostics.Candidates, fmt.Sprintf("%s:%s:%s", candidate.ProviderID, candidate.ModelID, candidate.UpstreamModelID))
	}
	return diagnostics
}

// SnapshotAndPlan builds either a single-model plan or a model fallback chain
// from one immutable input snapshot.
func SnapshotAndPlan(options SnapshotOptions, chain []string) (CandidatePlan, PlanDiagnostics, error) {
	requested := options.ModelID
	input, err := BuildCandidateSnapshot(options)
	if err != nil {
		return CandidatePlan{}, PlanDiagnostics{}, err
	}
	ensureLegacyClineOfferings(&input, chain)
	var plan CandidatePlan
	if len(chain) == 0 {
		plan = PlanCandidates(input)
	} else {
		plan = PlanFallbackChain(input, chain)
	}
	return plan, diagnosticsFor(requested, plan), nil
}

func comboPin(instances []domain.ProviderInstance, entry domain.ComboEntry) (CandidatePin, error) {
	switch strings.TrimSpace(entry.Source) {
	case "cline":
		return CandidatePin{ProviderID: "cline"}, nil
	case "zen":
		return CandidatePin{ProviderID: "opencode"}, nil
	case "provider":
		return resolveCandidatePin(instances, entry.Provider)
	default:
		return CandidatePin{}, nil
	}
}

// SnapshotAndPlanCombo converts each configured entry into an explicit plan
// group. Groups retain administrator order; candidates are sorted only inside
// their own entry. Emergency fallback is appended after every configured group.
func SnapshotAndPlanCombo(options SnapshotOptions, combo domain.ModelCombo) (CandidatePlan, PlanDiagnostics, error) {
	input, err := BuildCandidateSnapshot(options)
	if err != nil {
		return CandidatePlan{}, PlanDiagnostics{}, err
	}
	comboModels := make([]string, 0, len(combo.Entries))
	for _, entry := range combo.Entries {
		comboModels = append(comboModels, entry.Model)
	}
	ensureLegacyClineOfferings(&input, comboModels)
	combined := CandidatePlan{}
	seen := make(map[string]bool)
	for index, entry := range combo.Entries {
		entry, err = ResolveComboEntry(entry)
		if err != nil {
			return CandidatePlan{}, PlanDiagnostics{}, err
		}
		modelID := strings.TrimSpace(entry.Model)
		if modelID == "" || entry.Disabled {
			continue
		}
		key := modelID + "\x00" + strings.TrimSpace(entry.Source) + "\x00" + strings.TrimSpace(entry.Provider)
		if seen[key] {
			continue
		}
		seen[key] = true
		pin, err := comboPin(input.Instances, entry)
		if err != nil {
			return CandidatePlan{}, PlanDiagnostics{}, err
		}
		entryInput := input
		entryInput.ModelID = modelID
		entryInput.Pin = pin
		plan := PlanCandidates(entryInput)
		for i := range plan.Candidates {
			plan.Candidates[i].Group = index
		}
		combined.Candidates = append(combined.Candidates, plan.Candidates...)
		combined.Excluded = append(combined.Excluded, plan.Excluded...)
	}
	if combo.EmergencyFallback {
		fallback := PlanFallbackChain(input, ModelFallbackChain("free"))
		groupOffset := len(combo.Entries)
		for i := range fallback.Candidates {
			fallback.Candidates[i].Group += groupOffset
		}
		combined.Candidates = append(combined.Candidates, fallback.Candidates...)
		combined.Excluded = append(combined.Excluded, fallback.Excluded...)
	}
	for i := range combined.Candidates {
		combined.Candidates[i].Order = i + 1
	}
	return combined, diagnosticsFor(combo.ID, combined), nil
}

// DispatchAttempts expands each planner candidate into explicit credential
// attempts while preserving candidate/group order. Public providers receive one
// credential-less attempt; credential-required candidates are excluded earlier.
func DispatchAttempts(plan CandidatePlan) []dispatch.CandidateAttempt {
	attempts := make([]dispatch.CandidateAttempt, 0, len(plan.Candidates))
	order := 0
	for _, candidate := range plan.Candidates {
		credentials := candidate.Credentials
		if len(credentials) == 0 {
			credentials = []domain.CredentialID{""}
		}
		for _, credentialID := range credentials {
			order++
			proxyURL := ""
			if candidate.Egress.Proxy != nil {
				proxyURL = candidate.Egress.Proxy.String()
			}
			attempts = append(attempts, dispatch.CandidateAttempt{
				Order: order, Group: candidate.Group,
				LogicalModelID: candidate.ModelID, UpstreamModelID: candidate.UpstreamModelID,
				ProviderID: candidate.ProviderID, ProviderName: candidate.ProviderName,
				AdapterID: candidate.AdapterID, CredentialID: credentialID,
				EgressID: candidate.Egress.Endpoint, EgressMode: string(candidate.Egress.Mode),
				EgressSource: candidate.Egress.Source, EgressProxyURL: proxyURL,
				Pinned: candidate.Pinned,
			})
		}
	}
	return attempts
}
