package providers

import (
	"context"
	"routenest/internal/budget"
	"routenest/internal/domain"
	"sort"
	"strings"
	"time"
)

// Provider 健康快照：持久化配额用量、兼容冷却恢复和进程内近期成败热度。
// 只有 score ring 在重启后重新学习；预算与运行时可用性来自 durable stores。

type modelCooldown struct {
	Model string    `json:"model"`
	Until time.Time `json:"until"`
}

// providerBlock is one configured limit that is refusing requests right now.
// Model is empty when the limit covers the whole provider (daily requests and
// tokens); the per-minute limit is counted per model, so it names its model.
type providerBlock struct {
	Model   string    `json:"model,omitempty"`
	Kind    string    `json:"kind"` // rpm | daily_requests | daily_tokens | monthly_tokens
	Limit   int64     `json:"limit"`
	Usage   int64     `json:"usage"`
	ResetAt time.Time `json:"resetAt"`
}

type providerHealth struct {
	Daily        int64           `json:"daily"`
	Rpm          int             `json:"rpm"`
	Tokens       int64           `json:"tokens"` // 本配额窗口（月/日）已归集 token 数
	Cooldowns    []modelCooldown `json:"cooldowns"`
	Blocks       []providerBlock `json:"blocks"`               // 已用尽的配置上限；与冷却不同，由额度窗口重置解除
	RecoveryAt   string          `json:"recoveryAt,omitempty"` // 最早恢复（排序用）
	Rate         float64         `json:"rate"`
	Samples      int             `json:"samples"`
	AvgLatencyMs float64         `json:"avgLatencyMs"`
	Recent       []int           `json:"recent"` // 时间升序的最近成败 1/0
	Limits       map[string]any  `json:"limits"`
}

const providerRecentWindow = 16

// providerHealthSnapshot 按 providerID 汇总健康数据。预算快照在 Mu 之外
// 读取；成绩环只短暂持有 scoreMu，避免与派发路径形成嵌套。
func providerHealthSnapshot() map[string]*providerHealth {
	Load()
	Mu.Lock()
	ids := make([]string, 0, len(CustomProviders))
	limits := map[string]map[string]any{}
	windows := map[string]string{} // providerID → token 配额窗口（"monthly"/"daily"）
	policies := map[string]budget.Policy{}
	cooldowns := map[string]time.Time{}
	for k, v := range Cooldowns {
		cooldowns[k] = v
	}
	for _, p := range CustomProviders {
		ids = append(ids, p.ID)
		window := p.TokenWindow
		if window != "daily" {
			window = "monthly" // 空/未知统一按月窗展示
		}
		windows[p.ID] = window
		policies[p.ID] = providerBudgetPolicy(*p)
		limits[p.ID] = map[string]any{"rpm": p.RpmLimit, "daily": p.DailyLimit, "tokens": p.TokenLimit, "tokenWindow": window}
	}
	Mu.Unlock()

	out := map[string]*providerHealth{}
	for _, id := range ids {
		out[id] = &providerHealth{Cooldowns: []modelCooldown{}, Blocks: []providerBlock{}, Recent: []int{}, Limits: limits[id]}
	}
	health := func(id string) *providerHealth {
		if h := out[id]; h != nil {
			return h
		}
		return nil
	}

	now := time.Now()
	today := now.UTC().Format("2006-01-02")
	if budget.HasDefault() {
		for _, id := range ids {
			state, _ := budget.Default().Snapshot(context.Background(), id)
			state.Purge(now)
			h := health(id)
			for _, attempts := range state.RPM {
				h.Rpm += len(attempts)
			}
			h.Daily = state.DailyRequests[today]
			tokenKey := now.UTC().Format("2006-01")
			if windows[id] == "daily" {
				tokenKey = today
			}
			h.Tokens = state.Tokens[tokenKey]
			h.Blocks = budgetBlocks(state, id, policies[id], now)
		}
	}

	for key, until := range cooldowns {
		id := strings.SplitN(key, ":", 2)[0]
		h := health(id)
		if h == nil || !now.Before(until) {
			continue
		}
		model := key
		if i := strings.Index(key, ":"); i >= 0 {
			model = key[i+1:]
		}
		h.Cooldowns = append(h.Cooldowns, modelCooldown{Model: model, Until: until})
	}
	for _, h := range out {
		sort.Slice(h.Cooldowns, func(i, j int) bool { return h.Cooldowns[i].Until.Before(h.Cooldowns[j].Until) })
		var earliest time.Time
		if len(h.Cooldowns) > 0 {
			earliest = h.Cooldowns[0].Until
		}
		if len(h.Blocks) > 0 && (earliest.IsZero() || h.Blocks[0].ResetAt.Before(earliest)) {
			earliest = h.Blocks[0].ResetAt
		}
		if !earliest.IsZero() {
			h.RecoveryAt = earliest.UTC().Format(time.RFC3339)
		}
	}

	scoreMu.Lock()
	cut := now.Add(-providerScoreWindow)
	type sample struct {
		at  time.Time
		ok  bool
		lat int64
	}
	recent := map[string][]sample{}
	for key, s := range providerScores {
		id := strings.SplitN(key, ":", 2)[0]
		h := health(id)
		if h == nil {
			continue
		}
		for _, o := range s.ring {
			if o.at.IsZero() || o.at.Before(cut) {
				continue
			}
			h.Samples++
			if o.ok {
				recent[id] = append(recent[id], sample{at: o.at, ok: true, lat: o.latMS})
			} else {
				recent[id] = append(recent[id], sample{at: o.at, ok: false})
			}
		}
	}
	scoreMu.Unlock()

	for id, samples := range recent {
		h := health(id)
		sort.Slice(samples, func(i, j int) bool { return samples[i].at.Before(samples[j].at) })
		var okN, latSum, latN int64
		for _, sm := range samples {
			if sm.ok {
				okN++
				latSum += sm.lat
				latN++
			}
		}
		h.Samples = len(samples)
		if len(samples) > 0 {
			h.Rate = float64(okN) / float64(len(samples))
		} else {
			h.Rate = providerNeutralSuccess
		}
		if latN > 0 {
			h.AvgLatencyMs = float64(latSum) / float64(latN)
		}
		if len(samples) > providerRecentWindow {
			samples = samples[len(samples)-providerRecentWindow:]
		}
		for _, sm := range samples {
			v := 0
			if sm.ok {
				v = 1
			}
			h.Recent = append(h.Recent, v)
		}
	}
	return out
}

// budgetBlocks lists the configured limits that are refusing requests for one
// provider, soonest reset first. It asks budget.Exhaustion, the same function
// that decides admission, so the dashboard cannot disagree with the router.
func budgetBlocks(state budget.State, providerID string, policy budget.Policy, now time.Time) []providerBlock {
	out := []providerBlock{}
	add := func(model string, e *budget.ExhaustionError) {
		out = append(out, providerBlock{Model: model, Kind: e.Kind, Limit: e.Limit, Usage: e.Usage, ResetAt: e.ResetAt})
	}
	// With no model, the per-minute limit (counted per model) is skipped, so
	// what remains is the provider-wide request and token limits.
	if e := budget.Exhaustion(state, providerID, "", policy, now); e != nil {
		add("", e)
	}
	if policy.RPMLimit > 0 {
		for model := range state.RPM {
			if e := budget.Exhaustion(state, providerID, model, policy, now); e != nil && e.Kind == "rpm" {
				add(model, e)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ResetAt.Equal(out[j].ResetAt) {
			return out[i].ResetAt.Before(out[j].ResetAt)
		}
		return out[i].Model < out[j].Model
	})
	return out
}

// accountRecentOutcomes 从请求日志提取每个账号最近 N 次成败（时间升序）。
func accountRecentOutcomes(logs []domain.RequestLog) map[string][]int {
	result := map[string][]int{}
	type acc struct {
		items []int
	}
	counts := map[string]*acc{}
	for _, e := range logs { // 新→旧
		if e.AccountID == "" {
			continue
		}
		a := counts[e.AccountID]
		if a == nil {
			a = &acc{}
			counts[e.AccountID] = a
		}
		if len(a.items) >= providerRecentWindow {
			continue
		}
		v := 0
		if e.Completed && e.Error == "" {
			v = 1
		}
		a.items = append(a.items, v)
	}
	for id, a := range counts {
		out := make([]int, 0, len(a.items))
		for i := len(a.items) - 1; i >= 0; i-- {
			out = append(out, a.items[i])
		}
		result[id] = out
	}
	return result
}
