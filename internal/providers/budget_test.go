package providers

import (
	"path/filepath"
	"testing"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/connections"
	"routenest/internal/providerstate"
)

func TestRecordProviderTokenUsageAccruesMonthly(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	backend := installTestBudget(t, func() time.Time { return now })
	provider := CustomProvider{ID: "prov_tok", TokenLimit: 10_000, TokenWindow: "monthly"}
	admitTestUsage(t, provider, "m", "attempt-1", "tokens-1", now, 1200)
	admitTestUsage(t, provider, "m", "attempt-2", "tokens-2", now, 800)
	admitTestUsage(t, provider, "m", "attempt-3", "tokens-3", now, 1000)

	state, err := backend.Snapshot(t.Context(), provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Tokens[now.Format("2006-01")]; got != 3000 {
		t.Fatalf("month usage = %d, want 3000", got)
	}
	if err := RecordTokenUsage(provider.ID, "attempt-1", "negative", -3); err == nil {
		t.Fatal("negative usage must be rejected")
	}
	if err := RecordTokenUsage("", "attempt-1", "missing-provider", 1); err == nil {
		t.Fatal("empty provider ID must be rejected")
	}
}

func TestRecordProviderTokenUsageDailyWindow(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	backend := installTestBudget(t, func() time.Time { return now })
	provider := CustomProvider{ID: "prov_day", TokenLimit: 1000, TokenWindow: "daily"}
	admitTestUsage(t, provider, "m", "attempt-1", "tokens-1", now, 700)
	admitTestUsage(t, provider, "m", "attempt-2", "tokens-2", now, 300)

	state, err := backend.Snapshot(t.Context(), provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Tokens[now.Format("2006-01-02")]; got != 1000 {
		t.Fatalf("day usage = %d, want 1000", got)
	}
	if got := state.Tokens[now.Format("2006-01")]; got != 1000 {
		t.Fatalf("month audit usage = %d, want 1000", got)
	}
	if !providerBudgetExceeded(provider.ID, "m", 0, 0, 1000, "daily") {
		t.Fatal("daily token limit was not enforced")
	}
	if providerBudgetExceeded(provider.ID, "m", 0, 0, 2000, "monthly") {
		t.Fatal("larger monthly token limit was incorrectly exhausted")
	}
}

func TestRecordProviderTokenUsageMonthRollover(t *testing.T) {
	now := time.Date(2026, 1, 31, 23, 59, 0, 0, time.UTC)
	backend := installTestBudget(t, func() time.Time { return now })
	provider := CustomProvider{ID: "prov_roll", TokenLimit: 100, TokenWindow: "monthly"}
	admitTestUsage(t, provider, "m", "january-attempt", "january-tokens", now, 100)
	if !providerBudgetExceeded(provider.ID, "m", 0, 0, 100, "monthly") {
		t.Fatal("current month usage must exhaust the quota")
	}

	now = now.Add(time.Minute)
	if providerBudgetExceeded(provider.ID, "m", 0, 0, 100, "monthly") {
		t.Fatal("previous month usage carried into the new UTC month")
	}
	admitTestUsage(t, provider, "m", "february-attempt", "february-tokens", now, 50)
	state, err := backend.Snapshot(t.Context(), provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, stale := state.Tokens["2026-01"]; stale || state.Tokens["2026-02"] != 50 {
		t.Fatalf("month rollover state = %+v", state.Tokens)
	}
}

func TestProviderBudgetExhaustsDailyAndResetsNextDay(t *testing.T) {
	now := time.Date(2026, 11, 7, 23, 59, 0, 0, time.UTC)
	backend := installTestBudget(t, func() time.Time { return now })
	provider := CustomProvider{ID: "prov_d", TokenLimit: 10_000, TokenWindow: "daily"}
	admitTestUsage(t, provider, "m", "day-one", "day-one-tokens", now, 10_000)
	if !providerBudgetExceeded(provider.ID, "m", 0, 0, 10_000, "daily") {
		t.Fatal("same-day usage did not exhaust daily quota")
	}

	now = now.Add(time.Minute)
	if providerBudgetExceeded(provider.ID, "m", 0, 0, 10_000, "daily") {
		t.Fatal("previous-day usage carried into the new UTC day")
	}
	admitTestUsage(t, provider, "m", "day-two", "day-two-tokens", now, 12_000)
	if !providerBudgetExceeded(provider.ID, "m", 0, 0, 10_000, "daily") {
		t.Fatal("new-day usage did not exhaust daily quota")
	}
	state, err := backend.Snapshot(t.Context(), provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, stale := state.Tokens["2026-11-07"]; stale {
		t.Fatalf("expired daily counter was retained: %+v", state.Tokens)
	}
}

func TestProviderBudgetEmptyWindowDefaultsMonthly(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	installTestBudget(t, func() time.Time { return now })
	provider := CustomProvider{ID: "prov_e", TokenLimit: 500}
	admitTestUsage(t, provider, "m", "attempt", "tokens", now, 500)
	if !providerBudgetExceeded(provider.ID, "m", 0, 0, 500, "") {
		t.Fatal("empty token window did not default to monthly")
	}
}

func TestResolveSkipsTokenQuotaExhausted(t *testing.T) {
	now := time.Now().UTC()
	installTestBudget(t, func() time.Time { return now })
	installProviders(t,
		&CustomProvider{ID: "prov_q", Name: "Q", ModelIDs: []string{"m"}, Enabled: true, TokenLimit: 1000},
		&CustomProvider{ID: "prov_qd", Name: "QD", ModelIDs: []string{"m"}, Enabled: true, TokenLimit: 1000, TokenWindow: "daily"},
		&CustomProvider{ID: "prov_ok", Name: "OK", ModelIDs: []string{"m"}, Enabled: true, TokenLimit: 1000},
		&CustomProvider{ID: "prov_free", Name: "FREE", ModelIDs: []string{"m"}, Enabled: true},
	)
	if err := providerstate.Publish(providerstate.NewStore(func() time.Time { return now })); err != nil {
		t.Fatal(err)
	}
	admitTestUsage(t, *Find("prov_q"), "m", "q-attempt", "q-tokens", now, 1000)
	admitTestUsage(t, *Find("prov_qd"), "m", "qd-attempt", "qd-tokens", now, 1000)

	got := ResolveForModel("m")
	ids := map[string]bool{}
	for _, provider := range got {
		ids[provider.ID] = true
	}
	if len(ids) != 2 || !ids["prov_ok"] || !ids["prov_free"] || ids["prov_q"] || ids["prov_qd"] {
		t.Fatalf("resolved providers = %+v, want prov_ok + prov_free", ids)
	}
}

func TestResolveZeroTokenLimitNeverSkips(t *testing.T) {
	now := time.Now().UTC()
	installTestBudget(t, func() time.Time { return now })
	installProviders(t, &CustomProvider{ID: "prov_z", Name: "Z", ModelIDs: []string{"m"}, Enabled: true})
	provider := Find("prov_z")
	admitTestUsage(t, *provider, "m", "attempt", "tokens", now, 99_999_999)
	if got := ResolveForModel("m"); len(got) != 1 || got[0].ID != "prov_z" {
		t.Fatalf("resolved = %+v, want prov_z kept", got)
	}
}

func TestProviderHealthSnapshotTokenFields(t *testing.T) {
	now := time.Now().UTC()
	installTestBudget(t, func() time.Time { return now })
	installProviders(t,
		&CustomProvider{ID: "prov_h", Name: "H", TokenLimit: 5_000_000},
		&CustomProvider{ID: "prov_hd", Name: "HD", TokenLimit: 100, TokenWindow: "daily"},
		&CustomProvider{ID: "prov_h2", Name: "H2"},
	)
	admitTestUsage(t, *Find("prov_h"), "m", "h-attempt", "h-tokens", now, 12345)
	admitTestUsage(t, *Find("prov_hd"), "m", "hd-attempt", "hd-tokens", now, 4567)

	health := providerHealthSnapshot()
	if health["prov_h"].Tokens != 12345 {
		t.Fatalf("prov_h tokens = %d, want 12345", health["prov_h"].Tokens)
	}
	if health["prov_hd"].Tokens != 4567 {
		t.Fatalf("prov_hd tokens = %d, want 4567", health["prov_hd"].Tokens)
	}
	if health["prov_h2"].Tokens != 0 {
		t.Fatalf("prov_h2 tokens = %d, want 0", health["prov_h2"].Tokens)
	}
	if limit, _ := health["prov_h"].Limits["tokens"].(int64); limit != 5_000_000 {
		t.Fatalf("prov_h limits.tokens = %v", health["prov_h"].Limits["tokens"])
	}
	if window, _ := health["prov_h"].Limits["tokenWindow"].(string); window != "monthly" {
		t.Fatalf("prov_h token window = %v", health["prov_h"].Limits["tokenWindow"])
	}
	if window, _ := health["prov_hd"].Limits["tokenWindow"].(string); window != "daily" {
		t.Fatalf("prov_hd token window = %v", health["prov_hd"].Limits["tokenWindow"])
	}
}

func TestSaveCustomProviderTokenWindowNormalization(t *testing.T) {
	tmp := t.TempDir()
	oldProvPath, oldPoolPath, oldConnPath := FilePath, accounts.PoolPath, connections.FilePath
	FilePath = filepath.Join(tmp, ".routenest-providers.json")
	accounts.PoolPath = filepath.Join(tmp, ".routenest-accounts.json")
	connections.FilePath = filepath.Join(tmp, ".routenest-connections.json")
	oldProvs, oldLoaded := CustomProviders, Loaded
	oldPool := accounts.Pool
	provRecheckMu.Lock()
	oldSyncPath, oldSyncState, oldOwner := providerSyncFilePath, providerRecheckStat, providerRuntimeStateOwner
	oldSyncLoaded := provRecheckLoaded
	providerSyncFilePath = filepath.Join(tmp, ".routenest-providers-sync.json")
	providerRecheckStat = map[string]*providerRecheckState{}
	provRecheckLoaded = true
	providerRuntimeStateOwner = nil
	provRecheckMu.Unlock()
	t.Cleanup(func() {
		FilePath, accounts.PoolPath, connections.FilePath = oldProvPath, oldPoolPath, oldConnPath
		CustomProviders, Loaded, accounts.Pool = oldProvs, oldLoaded, oldPool
		provRecheckMu.Lock()
		providerSyncFilePath, providerRecheckStat, providerRuntimeStateOwner = oldSyncPath, oldSyncState, oldOwner
		provRecheckLoaded = oldSyncLoaded
		provRecheckMu.Unlock()
	})

	save := func(provider CustomProvider) (CustomProvider, error) {
		return SaveCustomProvider(provider)
	}

	got, err := save(CustomProvider{Name: "A", BaseURL: "https://a.example.com", TokenLimit: 100})
	if err != nil {
		t.Fatalf("empty window: %v", err)
	}
	if got.TokenWindow != "monthly" {
		t.Fatalf("empty tokenWindow = %q, want monthly", got.TokenWindow)
	}

	got, err = save(CustomProvider{Name: "B", BaseURL: "https://b.example.com", TokenLimit: 100, TokenWindow: "daily"})
	if err != nil {
		t.Fatalf("daily window: %v", err)
	}
	if got.TokenWindow != "daily" {
		t.Fatalf("daily tokenWindow = %q, want daily", got.TokenWindow)
	}

	if _, err := save(CustomProvider{Name: "C", BaseURL: "https://c.example.com", TokenWindow: "weekly"}); err == nil {
		t.Fatal("unknown tokenWindow must be rejected")
	}
}
