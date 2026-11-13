package providers

import (
	"fmt"
	"testing"
	"time"

	"routenest/internal/domain"
	"routenest/internal/providerstate"
)

func TestProviderHealthSnapshot(t *testing.T) {
	now := time.Now()
	installTestBudget(t, func() time.Time { return now })
	installProviders(t,
		&CustomProvider{ID: "provA", Name: "A", DailyLimit: 100, RpmLimit: 10},
		&CustomProvider{ID: "provB", Name: "B"},
	)
	Mu.Lock()
	Cooldowns = map[string]time.Time{
		"provA:gpt-x": now.Add(5 * time.Minute),
		"provA:gpt-y": now.Add(2 * time.Minute),
		"provB:m":     now.Add(-time.Minute), // 已过期，不应出现
		"gone:m":      now.Add(time.Minute),  // provider 已删除，忽略
	}
	Mu.Unlock()

	providerA := *Find("provA")
	providerA.RpmLimit, providerA.DailyLimit = 0, 0
	for i := 0; i < 41; i++ {
		if _, err := AdmitDispatch(t.Context(), providerA, "gpt-x", fmt.Sprintf("old-%d", i), now.Add(-70*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := AdmitDispatch(t.Context(), providerA, "gpt-x", "current", now.Add(-10*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := AdmitDispatch(t.Context(), *Find("provB"), "m", "b-current", now.Add(-5*time.Second)); err != nil {
		t.Fatal(err)
	}

	scoreMu.Lock()
	oldScores := providerScores
	providerScores = map[string]*providerScore{}
	s := &providerScore{}
	for i, ok := range []bool{true, true, false} {
		s.ring[i] = providerOutcome{at: now.Add(-time.Duration(i) * time.Second), ok: ok, latMS: 100}
	}
	s.count = 3
	providerScores["provA:gpt-x"] = s
	scoreMu.Unlock()
	t.Cleanup(func() {
		scoreMu.Lock()
		providerScores = oldScores
		scoreMu.Unlock()
	})

	h := providerHealthSnapshot()
	a := h["provA"]
	if a == nil {
		t.Fatalf("missing provA health: %+v", h)
	}
	if a.Daily != 42 || a.Rpm != 1 {
		t.Fatalf("provA daily/rpm = %d/%d, want 42/1", a.Daily, a.Rpm)
	}
	if len(a.Cooldowns) != 2 || a.Cooldowns[0].Model != "gpt-y" {
		t.Fatalf("provA cooldowns = %+v (want earliest gpt-y first)", a.Cooldowns)
	}
	if a.RecoveryAt == "" {
		t.Fatal("provA recoveryAt empty")
	}
	if a.Samples != 3 || a.Rate < 0.66 || a.Rate > 0.67 {
		t.Fatalf("provA samples/rate = %d/%v", a.Samples, a.Rate)
	}
	if len(a.Recent) != 3 || a.Recent[0] != 0 || a.Recent[2] != 1 {
		t.Fatalf("provA recent = %v (want ascending: oldest=failed first, newest=success last)", a.Recent)
	}
	if lim, _ := a.Limits["daily"].(int64); lim != 100 {
		if v, _ := a.Limits["daily"].(int); v != 100 {
			t.Fatalf("provA limits = %+v", a.Limits)
		}
	}
	b := h["provB"]
	if b == nil || b.Rpm != 1 || len(b.Cooldowns) != 0 {
		t.Fatalf("provB health = %+v", b)
	}
}

func TestAccountRecentOutcomes(t *testing.T) {
	logs := []RequestLog{
		{ID: "3", AccountID: "acc1", StartedAt: time.Now(), Completed: true},
		{ID: "2", AccountID: "acc1", StartedAt: time.Now().Add(-time.Minute), Completed: false, Error: "x"},
		{ID: "1", AccountID: "acc1", StartedAt: time.Now().Add(-2 * time.Minute), Completed: true},
		{ID: "0", AccountID: "", StartedAt: time.Now()},
	}
	got := accountRecentOutcomes(logs)
	acc := got["acc1"]
	if len(acc) != 3 || acc[0] != 1 || acc[1] != 0 || acc[2] != 1 {
		t.Fatalf("acc1 outcomes = %v, want ascending [1 0 1]", acc)
	}
	if _, ok := got[""]; ok {
		t.Fatal("anonymous account should be skipped")
	}
}

func TestProviderCooldownResetAndListPayload(t *testing.T) {
	installProviders(t, &CustomProvider{ID: "provR", Name: "R"})
	if err := providerstate.Publish(providerstate.NewStore(time.Now)); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Minute)
	SetCooldown("provR", "m1", until)
	SetCooldown("provR", "m2", until)
	SetCooldown("other", "m", until)
	scope := func(providerID, model string) domain.AvailabilityScope {
		return domain.AvailabilityScope{ProviderID: domain.ProviderInstanceID(providerID), ModelID: model}
	}

	// 只清 m1
	if removed := ResetCooldowns("provR", "m1"); removed != 1 {
		t.Fatalf("scoped reset removed %d, want 1", removed)
	}
	Mu.Lock()
	_, m1 := Cooldowns["provR:m1"]
	_, m2 := Cooldowns["provR:m2"]
	_, other := Cooldowns["other:m"]
	Mu.Unlock()
	if m1 || !m2 || !other {
		t.Fatalf("after scoped reset m1=%v m2=%v other=%v", m1, m2, other)
	}
	if got := providerstate.Default().Blocking(scope("provR", "m1")); len(got) != 0 {
		t.Fatalf("scoped reset retained normalized observation: %+v", got)
	}
	if got := providerstate.Default().Blocking(scope("provR", "m2")); len(got) != 1 {
		t.Fatalf("scoped reset removed sibling observation: %+v", got)
	}

	// 清整个 provider
	if removed := ResetCooldowns("provR", ""); removed != 1 {
		t.Fatalf("provider reset removed %d, want 1 (m2)", removed)
	}
	Mu.Lock()
	remaining := len(Cooldowns)
	Mu.Unlock()
	if remaining != 1 {
		t.Fatalf("cooldowns left = %d, want only other:m", remaining)
	}
	if got := providerstate.Default().Blocking(scope("provR", "m2")); len(got) != 0 {
		t.Fatalf("provider reset retained normalized observation: %+v", got)
	}
	if got := providerstate.Default().Blocking(scope("other", "m")); len(got) != 1 {
		t.Fatalf("provider reset removed unrelated observation: %+v", got)
	}

	ResetRoutingState()
	if got := providerstate.Default().Blocking(scope("other", "m")); len(got) != 0 {
		t.Fatalf("routing reset retained normalized observation: %+v", got)
	}

	// 列表视图带 health 字段
	ov := ProviderOverview(nil)
	if len(ov.Providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(ov.Providers))
	}
	if ov.Health["provR"] == nil {
		t.Fatalf("health missing provR: %v", ov.Health)
	}
}

func TestProviderCooldownResetClearsEveryMatchingCooldownScopeOnly(t *testing.T) {
	installProviders(t, &CustomProvider{ID: "prov_scope_reset", Name: "Scoped reset"})
	now := time.Now()
	store := providerstate.NewStore(func() time.Time { return now })
	if err := providerstate.Publish(store); err != nil {
		t.Fatal(err)
	}
	providerID := domain.ProviderInstanceID("prov_scope_reset")
	modelScope := domain.AvailabilityScope{ProviderID: providerID, ModelID: "m1"}
	credentialScope := domain.AvailabilityScope{ProviderID: providerID, CredentialID: "api-key:prov_scope_reset", ModelID: "m1"}
	egressScope := domain.AvailabilityScope{ProviderID: providerID, CredentialID: "api-key:prov_scope_reset", ModelID: "m1", EgressID: "proxy-1"}
	broadEgressScope := domain.AvailabilityScope{ProviderID: providerID, EgressID: "proxy-2"}
	authScope := domain.AvailabilityScope{ProviderID: providerID, CredentialID: "api-key:auth", ModelID: "m1"}
	siblingScope := domain.AvailabilityScope{ProviderID: providerID, CredentialID: "api-key:sibling", ModelID: "m2"}

	SetCooldown(string(providerID), "m1", now.Add(time.Hour))
	for _, observation := range []domain.AvailabilityObservation{
		{Kind: domain.ObservationQuotaExhausted, Scope: modelScope, Until: now.Add(time.Hour), ResetAt: now.Add(time.Hour), Source: "configured_window", Confidence: domain.ConfidenceExplicit},
		{Kind: domain.ObservationRateLimited, Scope: credentialScope, Until: now.Add(time.Hour), Source: "retry-after", Confidence: domain.ConfidenceExplicit},
		{Kind: domain.ObservationTransientFailure, Scope: egressScope, Until: now.Add(time.Hour), Source: "status", Confidence: domain.ConfidenceInferred},
		{Kind: domain.ObservationRateLimited, Scope: broadEgressScope, Until: now.Add(time.Hour), Source: "retry-after", Confidence: domain.ConfidenceExplicit},
		{Kind: domain.ObservationAuthExpired, Scope: authScope, Source: "status", Confidence: domain.ConfidenceExplicit},
		{Kind: domain.ObservationRateLimited, Scope: siblingScope, Until: now.Add(time.Hour), Source: "retry-after", Confidence: domain.ConfidenceExplicit},
	} {
		if err := store.Apply(observation); err != nil {
			t.Fatal(err)
		}
	}

	if removed := ResetCooldowns(string(providerID), "m1"); removed != 1 {
		t.Fatalf("removed = %d, want one legacy cooldown", removed)
	}
	remaining := store.Snapshot(providerID)
	for _, observation := range remaining {
		if observation.Scope == credentialScope || observation.Scope == egressScope || observation.Scope == broadEgressScope ||
			(observation.Scope == modelScope && observation.Source == "legacy_provider_cooldown") {
			t.Errorf("matching cooldown survived reset: %+v", observation)
		}
	}
	if !hasAvailabilityObservation(remaining, modelScope, domain.ObservationQuotaExhausted, "configured_window") {
		t.Errorf("configured budget evidence was cleared by cooldown reset: %+v", remaining)
	}
	if !hasAvailabilityObservation(remaining, authScope, domain.ObservationAuthExpired, "status") {
		t.Errorf("permanent auth evidence was cleared by cooldown reset: %+v", remaining)
	}
	if !hasAvailabilityObservation(remaining, siblingScope, domain.ObservationRateLimited, "retry-after") {
		t.Errorf("sibling model cooldown was cleared by model reset: %+v", remaining)
	}
}

func hasAvailabilityObservation(observations []domain.AvailabilityObservation, scope domain.AvailabilityScope, kind domain.ObservationKind, source string) bool {
	for _, observation := range observations {
		if observation.Scope == scope && observation.Kind == kind && observation.Source == source {
			return true
		}
	}
	return false
}
