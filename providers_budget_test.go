package main

import (
	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/connections"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// saveBudgetState 保存/恢复月度 token 预算相关的全部内存态，测试间互不污染。
func saveBudgetState(t *testing.T) {
	t.Helper()
	providersMu.Lock()
	oldProviders, oldLoaded, oldCooldowns := customProviders, providersLoaded, providerCooldowns
	providersMu.Unlock()
	budgetMu.Lock()
	oldDaily, oldRPM, oldTokens := providerDaily, providerRPM, providerTokens
	budgetMu.Unlock()
	t.Cleanup(func() {
		providersMu.Lock()
		customProviders, providersLoaded, providerCooldowns = oldProviders, oldLoaded, oldCooldowns
		providersMu.Unlock()
		budgetMu.Lock()
		providerDaily, providerRPM, providerTokens = oldDaily, oldRPM, oldTokens
		budgetMu.Unlock()
	})
}

// recordProviderTokenUsage 归集月度 token 用量。
func TestRecordProviderTokenUsageAccruesMonthly(t *testing.T) {
	saveBudgetState(t)
	budgetMu.Lock()
	providerTokens = map[string]map[string]int64{}
	budgetMu.Unlock()

	month := time.Now().UTC().Format("2006-01")
	recordProviderTokenUsage("prov_tok", 1200, "monthly")
	recordProviderTokenUsage("prov_tok", 800, "monthly")
	recordProviderTokenUsage("prov_tok", -3, "monthly")   // 异常回报忽略
	recordProviderTokenUsage("", 999, "monthly")          // 无归属忽略
	recordProviderTokenUsage("prov_tok", 1000, "monthly") // 跨家不同计数

	budgetMu.Lock()
	got := providerTokens["prov_tok"][month]
	_, emptyHasNoKey := providerTokens[""][""]
	budgetMu.Unlock()
	if got != 3000 {
		t.Fatalf("month usage = %d, want 3000", got)
	}
	if emptyHasNoKey {
		t.Fatal("empty providerID should not accrue")
	}
}

// 日窗归集：用量落在 UTC 日键（"2006-01-02"），与月键隔离。
func TestRecordProviderTokenUsageDailyWindow(t *testing.T) {
	saveBudgetState(t)
	budgetMu.Lock()
	providerTokens = map[string]map[string]int64{
		"prov_day": {"1999-12-31": 42}, // 昨日键：不参与今日判定
	}
	budgetMu.Unlock()

	day := time.Now().UTC().Format("2006-01-02")
	month := time.Now().UTC().Format("2006-01")
	recordProviderTokenUsage("prov_day", 700, "daily")
	recordProviderTokenUsage("prov_day", 300, "daily")
	recordProviderTokenUsage("prov_day", -5, "daily") // 异常回报忽略

	budgetMu.Lock()
	gotDay := providerTokens["prov_day"][day]
	gotMonth := providerTokens["prov_day"][month]
	budgetMu.Unlock()
	if gotDay != 1000 {
		t.Fatalf("day usage = %d, want 1000", gotDay)
	}
	if gotMonth != 0 {
		t.Fatalf("daily window must not touch month key, got %d", gotMonth)
	}
}

// 月切换后旧月键自然废弃：仅当键相同时累加，新的月份用新键从头计。
func TestRecordProviderTokenUsageMonthRollover(t *testing.T) {
	saveBudgetState(t)
	budgetMu.Lock()
	providerTokens = map[string]map[string]int64{
		"prov_roll": {"1999-12": 42}, // 过期月，不参与当月判定
	}
	budgetMu.Unlock()

	// 预算判定只看当月键：上月 42 不应触发限制
	if done := providerBudgetExceeded("prov_roll", "m", 0, 0, 100, "monthly"); done {
		t.Fatal("last month's usage must not trip this month's quota")
	}
	recordProviderTokenUsage("prov_roll", 100, "monthly")
	if done := providerBudgetExceeded("prov_roll", "m", 0, 0, 100, "monthly"); !done {
		t.Fatal("usage >= limit must trip the quota")
	}
	budgetMu.Lock()
	_, staleKept := providerTokens["prov_roll"]["1999-12"]
	cur := providerTokens["prov_roll"][time.Now().UTC().Format("2006-01")]
	budgetMu.Unlock()
	if !staleKept || cur != 100 {
		t.Fatalf("rollover: stale kept=%v cur=%d, want true/100", staleKept, cur)
	}
}

// 日窗配额：同一天内耗尽即触发；跨 UTC 日后旧日键废弃、重新从零计。
func TestProviderBudgetExhaustsDailyAndResetsNextDay(t *testing.T) {
	saveBudgetState(t)
	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	budgetMu.Lock()
	providerTokens = map[string]map[string]int64{
		"prov_d": {yesterday: 10_000, today: 10_000}, // 昨日已满 + 今日已满
	}
	budgetMu.Unlock()

	// 日窗看今日键：今日 10000 >= 10000 → 超配
	if done := providerBudgetExceeded("prov_d", "m", 0, 0, 10_000, "daily"); !done {
		t.Fatal("same-day usage >= limit must trip daily quota")
	}
	// 月窗看月键：日键上的用量对月窗不可见 → 不超配
	if done := providerBudgetExceeded("prov_d", "m", 0, 0, 10_000, "monthly"); done {
		t.Fatal("daily-window usage must not trip a monthly quota")
	}

	// 模拟跨日：把“今日”用量改标到昨日，今日键清零 → 日窗重置
	budgetMu.Lock()
	providerTokens["prov_d"] = map[string]int64{yesterday: 20_000}
	budgetMu.Unlock()
	if done := providerBudgetExceeded("prov_d", "m", 0, 0, 10_000, "daily"); done {
		t.Fatal("yesterday's exhausted quota must not carry into a new UTC day")
	}
	// 新的一天重新计量：12000 >= 10000 → 超配
	recordProviderTokenUsage("prov_d", 12_000, "daily")
	if done := providerBudgetExceeded("prov_d", "m", 0, 0, 10_000, "daily"); !done {
		t.Fatal("new-day usage >= limit must trip daily quota again")
	}
}

// 空窗口值视同 monthly（与 handleProviderSave 归一化后一致）。
func TestProviderBudgetEmptyWindowDefaultsMonthly(t *testing.T) {
	saveBudgetState(t)
	month := time.Now().UTC().Format("2006-01")
	budgetMu.Lock()
	providerTokens = map[string]map[string]int64{
		"prov_e": {month: 500},
	}
	budgetMu.Unlock()

	if done := providerBudgetExceeded("prov_e", "m", 0, 0, 500, ""); !done {
		t.Fatal("empty tokenWindow must fall back to monthly key")
	}
}

// resolveProvidersForModel：token 配额耗尽的 provider 不入候选。
func TestResolveSkipsTokenQuotaExhausted(t *testing.T) {
	saveBudgetState(t)
	providersMu.Lock()
	customProviders = []*CustomProvider{
		{ID: "prov_q", Name: "Q", ModelIDs: []string{"m"}, Enabled: true, TokenLimit: 1000},
		{ID: "prov_qd", Name: "QD", ModelIDs: []string{"m"}, Enabled: true, TokenLimit: 1000, TokenWindow: "daily"},
		{ID: "prov_ok", Name: "OK", ModelIDs: []string{"m"}, Enabled: true, TokenLimit: 1000},
		{ID: "prov_free", Name: "FREE", ModelIDs: []string{"m"}, Enabled: true}, // 0 = 不启用
	}
	providersLoaded = true
	providerCooldowns = map[string]time.Time{}
	providersMu.Unlock()
	budgetMu.Lock()
	providerTokens = map[string]map[string]int64{
		"prov_q":  {time.Now().UTC().Format("2006-01"): 1000},    // 月窗用满
		"prov_qd": {time.Now().UTC().Format("2006-01-02"): 1000}, // 日窗用满
	}
	budgetMu.Unlock()

	got := resolveProvidersForModel("m")
	names := []string{}
	for _, p := range got {
		names = append(names, p.ID)
	}
	if len(names) != 2 || names[0] == "prov_q" || names[1] == "prov_q" ||
		names[0] == "prov_qd" || names[1] == "prov_qd" {
		t.Fatalf("resolved = %v, want prov_ok + prov_free and no prov_q/prov_qd", names)
	}
}

// 零/未配置 token 上限永不踢出 provider。
func TestResolveZeroTokenLimitNeverSkips(t *testing.T) {
	saveBudgetState(t)
	providersMu.Lock()
	customProviders = []*CustomProvider{
		{ID: "prov_z", Name: "Z", ModelIDs: []string{"m"}, Enabled: true},
	}
	providersLoaded = true
	providerCooldowns = map[string]time.Time{}
	providersMu.Unlock()
	budgetMu.Lock()
	providerTokens = map[string]map[string]int64{
		"prov_z": {time.Now().UTC().Format("2006-01"): 99_999_999}, // 远超任何数
	}
	budgetMu.Unlock()

	if got := resolveProvidersForModel("m"); len(got) != 1 || got[0].ID != "prov_z" {
		t.Fatalf("resolved = %+v, want prov_z kept (tokenLimit 0 = 不启用)", got)
	}
}

// 健康快照暴露本窗口 token 用量与 limits.tokens / limits.tokenWindow。
func TestProviderHealthSnapshotTokenFields(t *testing.T) {
	saveBudgetState(t)
	providersMu.Lock()
	customProviders = []*CustomProvider{
		{ID: "prov_h", Name: "H", TokenLimit: 5_000_000}, // 空窗口 = monthly
		{ID: "prov_hd", Name: "HD", TokenLimit: 100, TokenWindow: "daily"},
		{ID: "prov_h2", Name: "H2"},
	}
	providersLoaded = true
	providerCooldowns = map[string]time.Time{}
	providersMu.Unlock()
	now := time.Now().UTC()
	budgetMu.Lock()
	providerTokens = map[string]map[string]int64{
		"prov_h":  {now.Format("2006-01"): 12345, now.Format("2006-01-02"): 999},
		"prov_hd": {now.Format("2006-01"): 777, now.Format("2006-01-02"): 4567},
	}
	budgetMu.Unlock()

	h := providerHealthSnapshot()
	if h["prov_h"].Tokens != 12345 {
		t.Fatalf("prov_h tokens = %d, want 12345 (month key)", h["prov_h"].Tokens)
	}
	if h["prov_hd"].Tokens != 4567 {
		t.Fatalf("prov_hd tokens = %d, want 4567 (day key)", h["prov_hd"].Tokens)
	}
	if h["prov_h2"].Tokens != 0 {
		t.Fatalf("prov_h2 tokens = %d, want 0", h["prov_h2"].Tokens)
	}
	if lim, _ := h["prov_h"].Limits["tokens"].(int64); lim != 5_000_000 {
		t.Fatalf("prov_h limits.tokens = %v, want 5000000", h["prov_h"].Limits["tokens"])
	}
	if tw, _ := h["prov_h"].Limits["tokenWindow"].(string); tw != "monthly" {
		t.Fatalf("prov_h limits.tokenWindow = %v, want monthly", h["prov_h"].Limits["tokenWindow"])
	}
	if tw, _ := h["prov_hd"].Limits["tokenWindow"].(string); tw != "daily" {
		t.Fatalf("prov_hd limits.tokenWindow = %v, want daily", h["prov_hd"].Limits["tokenWindow"])
	}
	if lim, _ := h["prov_h2"].Limits["tokens"].(int64); lim != 0 {
		t.Fatalf("prov_h2 limits.tokens = %v, want 0", h["prov_h2"].Limits["tokens"])
	}
}

// handleProviderSave 归一化 tokenWindow：空 → "monthly"，"daily"/"monthly" 保留，
// 其余值 400 拒绝。
func TestHandleProviderSaveTokenWindowNormalization(t *testing.T) {
	// 落盘路径全部隔离到临时目录（save 会写 provider 文件并同步模型池）。
	oldProvPath, oldPoolPath, oldConnPath := providersFilePath, accounts.PoolPath, connections.FilePath
	providersFilePath = filepath.Join(t.TempDir(), ".routenest-providers.json")
	accounts.PoolPath = filepath.Join(t.TempDir(), ".routenest-accounts.json")
	connections.FilePath = filepath.Join(t.TempDir(), ".routenest-connections.json")
	oldProvs, oldLoaded := customProviders, providersLoaded
	oldPool := accounts.Pool
	t.Cleanup(func() {
		providersFilePath, accounts.PoolPath, connections.FilePath = oldProvPath, oldPoolPath, oldConnPath
		customProviders, providersLoaded, accounts.Pool = oldProvs, oldLoaded, oldPool
	})

	save := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handleProviderSave(rec, httptest.NewRequest(http.MethodPost, "/admin/api/providers", strings.NewReader(body)))
		return rec
	}
	savedWindow := func(t *testing.T, rec *httptest.ResponseRecorder) string {
		t.Helper()
		var payload struct {
			Success bool `json:"success"`
			Data    struct {
				Provider CustomProvider `json:"provider"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode %s: %v", rec.Body.String(), err)
		}
		return payload.Data.Provider.TokenWindow
	}

	// 空窗口 → monthly
	rec := save(`{"name":"A","baseURL":"https://a.example.com","tokenLimit":100}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty window status = %d body=%s", rec.Code, rec.Body.String())
	}
	if tw := savedWindow(t, rec); tw != "monthly" {
		t.Fatalf("empty tokenWindow = %q, want monthly", tw)
	}

	// daily 保留
	rec = save(`{"name":"B","baseURL":"https://b.example.com","tokenLimit":100,"tokenWindow":"daily"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("daily window status = %d body=%s", rec.Code, rec.Body.String())
	}
	if tw := savedWindow(t, rec); tw != "daily" {
		t.Fatalf("daily tokenWindow = %q, want daily", tw)
	}

	// 未知值 → 400
	rec = save(`{"name":"C","baseURL":"https://c.example.com","tokenWindow":"weekly"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid window status = %d, want 400", rec.Code)
	}
}
