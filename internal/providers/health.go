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

type providerHealth struct {
	Daily        int64           `json:"daily"`
	Rpm          int             `json:"rpm"`
	Tokens       int64           `json:"tokens"` // 本配额窗口（月/日）已归集 token 数
	Cooldowns    []modelCooldown `json:"cooldowns"`
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
		limits[p.ID] = map[string]any{"rpm": p.RpmLimit, "daily": p.DailyLimit, "tokens": p.TokenLimit, "tokenWindow": window}
	}
	Mu.Unlock()

	out := map[string]*providerHealth{}
	for _, id := range ids {
		out[id] = &providerHealth{Cooldowns: []modelCooldown{}, Recent: []int{}, Limits: limits[id]}
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
		if len(h.Cooldowns) > 0 {
			h.RecoveryAt = h.Cooldowns[0].Until.UTC().Format(time.RFC3339)
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
