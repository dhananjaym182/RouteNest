package providers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProviderHealthSnapshot(t *testing.T) {
	// 保存并恢复全部受影响的全局内存态
	Mu.Lock()
	oldProviders, oldLoaded, oldCooldowns := CustomProviders, Loaded, Cooldowns
	CustomProviders = []*CustomProvider{
		{ID: "provA", Name: "A", DailyLimit: 100, RpmLimit: 10},
		{ID: "provB", Name: "B"},
	}
	Loaded = true
	Cooldowns = map[string]time.Time{
		"provA:gpt-x": time.Now().Add(5 * time.Minute),
		"provA:gpt-y": time.Now().Add(2 * time.Minute),
		"provB:m":     time.Now().Add(-time.Minute), // 已过期，不应出现
		"gone:m":      time.Now().Add(time.Minute),  // provider 已删除，忽略
	}
	oldDaily, oldRPM, oldScores := providerDaily, providerRPM, providerScores
	Mu.Unlock()
	defer func() {
		Mu.Lock()
		CustomProviders, Loaded, Cooldowns = oldProviders, oldLoaded, oldCooldowns
		Mu.Unlock()
		budgetMu.Lock()
		providerDaily, providerRPM = oldDaily, oldRPM
		budgetMu.Unlock()
		scoreMu.Lock()
		providerScores = oldScores
		scoreMu.Unlock()
	}()

	now := time.Now()
	budgetMu.Lock()
	providerDaily = map[string]map[string]int64{"provA": {now.UTC().Format("2006-01-02"): 42}}
	providerRPM = map[string][]time.Time{
		"provA:gpt-x": {now.Add(-10 * time.Second), now.Add(-70 * time.Second)}, // 只有 1 次在窗口内
		"provB:m":     {now.Add(-5 * time.Second)},
	}
	budgetMu.Unlock()

	scoreMu.Lock()
	providerScores = map[string]*providerScore{}
	s := &providerScore{}
	for i, ok := range []bool{true, true, false} {
		s.ring[i] = providerOutcome{at: now.Add(-time.Duration(i) * time.Second), ok: ok, latMS: 100}
	}
	s.count = 3
	providerScores["provA:gpt-x"] = s
	scoreMu.Unlock()

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
	oldSnapshot := RequestLogsSnapshot
	logs := []RequestLog{
		{ID: "3", AccountID: "acc1", StartedAt: time.Now(), Completed: true},
		{ID: "2", AccountID: "acc1", StartedAt: time.Now().Add(-time.Minute), Completed: false, Error: "x"},
		{ID: "1", AccountID: "acc1", StartedAt: time.Now().Add(-2 * time.Minute), Completed: true},
		{ID: "0", AccountID: "", StartedAt: time.Now()},
	}
	RequestLogsSnapshot = func() []RequestLog { return logs }
	t.Cleanup(func() { RequestLogsSnapshot = oldSnapshot })

	got := accountRecentOutcomes()
	acc := got["acc1"]
	if len(acc) != 3 || acc[0] != 1 || acc[1] != 0 || acc[2] != 1 {
		t.Fatalf("acc1 outcomes = %v, want ascending [1 0 1]", acc)
	}
	if _, ok := got[""]; ok {
		t.Fatal("anonymous account should be skipped")
	}
}

func TestProviderCooldownResetAndListPayload(t *testing.T) {
	Mu.Lock()
	oldProviders, oldLoaded, oldCooldowns := CustomProviders, Loaded, Cooldowns
	oldPath := CooldownsPath
	CustomProviders = []*CustomProvider{{ID: "provR", Name: "R"}}
	Loaded = true
	Cooldowns = map[string]time.Time{
		"provR:m1": time.Now().Add(time.Minute),
		"provR:m2": time.Now().Add(time.Minute),
		"other:m":  time.Now().Add(time.Minute),
	}
	CooldownsPath = t.TempDir() + "/.cline-providers-cooldowns.json"
	Mu.Unlock()
	defer func() {
		Mu.Lock()
		CustomProviders, Loaded, Cooldowns = oldProviders, oldLoaded, oldCooldowns
		CooldownsPath = oldPath
		Mu.Unlock()
	}()

	// 只清 m1
	rec := httptest.NewRecorder()
	HandleCooldownReset(rec, httptest.NewRequest(http.MethodPost, "/admin/api/providers/cooldown-reset",
		strings.NewReader(`{"id":"provR","model":"m1"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	Mu.Lock()
	_, m1 := Cooldowns["provR:m1"]
	_, m2 := Cooldowns["provR:m2"]
	_, other := Cooldowns["other:m"]
	Mu.Unlock()
	if m1 || !m2 || !other {
		t.Fatalf("after scoped reset m1=%v m2=%v other=%v", m1, m2, other)
	}

	// 清整个 provider
	rec = httptest.NewRecorder()
	HandleCooldownReset(rec, httptest.NewRequest(http.MethodPost, "/admin/api/providers/cooldown-reset",
		strings.NewReader(`{"id":"provR"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	Mu.Lock()
	remaining := len(Cooldowns)
	Mu.Unlock()
	if remaining != 1 {
		t.Fatalf("cooldowns left = %d, want only other:m", remaining)
	}

	// 缺 id → 400
	rec = httptest.NewRecorder()
	HandleCooldownReset(rec, httptest.NewRequest(http.MethodPost, "/admin/api/providers/cooldown-reset",
		strings.NewReader(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing-id status = %d", rec.Code)
	}

	// 列表接口带 health 字段
	rec = httptest.NewRecorder()
	HandleList(rec, httptest.NewRequest(http.MethodGet, "/admin/api/providers", nil))
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Providers     []map[string]any `json:"providers"`
			Health        map[string]any   `json:"health"`
			AccountHealth map[string][]int `json:"accountHealth"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("payload decode: %v (%s)", err, rec.Body.String())
	}
	if !payload.Success || len(payload.Data.Providers) != 1 {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.Data.Health["provR"] == nil {
		t.Fatalf("health missing provR: %v", payload.Data.Health)
	}
}
