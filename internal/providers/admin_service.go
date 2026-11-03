package providers

// Owner operations behind the provider admin endpoints. Request parsing and
// response envelopes live in internal/admin; these functions hold the rules,
// persistence and probes.

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"strings"
	"time"

	"routenest/internal/connections"
	"routenest/internal/domain"
	"routenest/internal/platform"
	"routenest/internal/protocol/openaichat"
	"routenest/internal/providerreg"
	"routenest/internal/transport"
)

// ErrProviderNotFound is returned when no saved provider has the requested ID.
var ErrProviderNotFound = errors.New("provider not found")

// Overview is the provider list shown in the admin console.
type Overview struct {
	Providers     []CustomProvider
	Health        map[string]*providerHealth
	AccountHealth map[string][]int
}

// ProviderOverview returns the saved providers with their health and recent
// account outcomes.
// logs is the request-log snapshot the caller provides; account health is derived from it.
func ProviderOverview(logs []domain.RequestLog) Overview {
	return Overview{
		Providers:     List(),
		Health:        providerHealthSnapshot(),
		AccountHealth: accountRecentOutcomes(logs),
	}
}

// AccountOutcomes returns recent success and failure history per account.
func AccountOutcomes(logs []domain.RequestLog) map[string][]int {
	return accountRecentOutcomes(logs)
}

// SaveCustomProvider normalises and validates p, stores it, and refreshes the
// model projection. A validation failure returns an error whose message is
// meant for the operator.
func SaveCustomProvider(p CustomProvider) (CustomProvider, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.BaseURL = strings.TrimSpace(strings.TrimRight(p.BaseURL, "/"))
	p.APIKey = strings.TrimSpace(p.APIKey)
	if p.Name == "" || p.BaseURL == "" {
		return CustomProvider{}, errors.New("name and baseURL are required")
	}
	if !strings.HasPrefix(p.BaseURL, "http://") && !strings.HasPrefix(p.BaseURL, "https://") {
		return CustomProvider{}, errors.New("baseURL must start with http:// or https://")
	}
	// 清洗模型 ID 与请求头
	var models []string
	seen := map[string]bool{}
	for _, m := range p.ModelIDs {
		m = strings.TrimSpace(m)
		if m != "" && !seen[m] {
			seen[m] = true
			models = append(models, m)
		}
	}
	p.ModelIDs = models
	hdrs := map[string]string{}
	for k, v := range p.Headers {
		k = strings.TrimSpace(k)
		if k != "" {
			hdrs[k] = strings.TrimSpace(v)
		}
	}
	p.Headers = hdrs
	if p.TimeoutSec < 0 || p.TimeoutSec > 600 {
		p.TimeoutSec = 0
	}
	normalizedProxy, proxyErr := transport.NormalizeProxySpec(p.ProxyURL)
	if proxyErr != nil {
		return CustomProvider{}, proxyErr
	}
	p.ProxyURL = normalizedProxy
	if p.Weight < 0 {
		p.Weight = -1 // explicit exclusion from weighted_round_robin
	}
	if p.Weight > 1_000_000 {
		p.Weight = 1_000_000
	}
	if p.RpmLimit < 0 {
		p.RpmLimit = 0
	}
	if p.DailyLimit < 0 {
		p.DailyLimit = 0
	}
	if p.TokenLimit < 0 {
		p.TokenLimit = 0
	}
	// token 配额窗口：空默认 monthly；未知值拒绝，避免静默落到错误粒度。
	p.TokenWindow = strings.ToLower(strings.TrimSpace(p.TokenWindow))
	if p.TokenWindow == "" {
		p.TokenWindow = "monthly"
	} else if p.TokenWindow != "monthly" && p.TokenWindow != "daily" {
		return CustomProvider{}, errors.New(`tokenWindow must be "monthly" or "daily"`)
	}
	// Wave-4 pool hygiene: keep only connection IDs that still exist.
	seenConn := map[string]bool{}
	var cids []string
	for _, id := range p.ConnectionIDs {
		id = strings.TrimSpace(id)
		if id == "" || seenConn[id] || !connections.ConnectionExists(id) {
			continue
		}
		seenConn[id] = true
		cids = append(cids, id)
	}
	p.ConnectionIDs = cids
	if p.ConnectionID != "" && !connections.ConnectionExists(p.ConnectionID) {
		p.ConnectionID = ""
	}
	saved := Upsert(&p)
	SyncModels() // 模型列表 / 回退链立即反映变更
	log.Printf("admin: provider saved name=%s models=%d enabled=%v", saved.Name, len(saved.ModelIDs), saved.Enabled)
	return saved, nil
}

// DeleteCustomProvider removes a saved provider and its model and recheck state.
func DeleteCustomProvider(id string) error {
	if !Delete(id) {
		return ErrProviderNotFound
	}
	SyncModels() // 清理该 provider 声明的池内模型条目
	clearProviderRecheck(id)
	log.Printf("admin: provider deleted id=%s", id)
	return nil
}

// ProbeSavedProvider sends one minimal request through a saved provider. An empty
// model falls back to the provider's first model ID.
func ProbeSavedProvider(id, model string) (map[string]any, error) {
	Load()
	Mu.Lock()
	var target *CustomProvider
	for _, p := range CustomProviders {
		if p.ID == id {
			cp := *cloneProvider(p)
			target = &cp
			break
		}
	}
	Mu.Unlock()
	if target == nil {
		return nil, ErrProviderNotFound
	}
	if model == "" && len(target.ModelIDs) > 0 {
		model = target.ModelIDs[0]
	}
	return runProviderMiniTest(target, model), nil
}

// ProbeUnsavedProvider tests a configuration that is not stored. Nothing is
// persisted. A missing model is reported as a failed probe, not an error.
func ProbeUnsavedProvider(baseURL, apiKey string, headers map[string]string, model string) (map[string]any, error) {
	baseURL = strings.TrimSpace(strings.TrimRight(baseURL, "/"))
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return nil, errors.New("baseURL must start with http:// or https://")
	}
	if model == "" {
		// 临时 provider 未声明模型列表，空 model 无从发起补全，需给出明确说明。
		return map[string]any{
			"ok": false, "model": "", "durationMs": 0,
			"error": "model is required when testing an unsaved provider",
		}, nil
	}
	target := &CustomProvider{
		BaseURL:    baseURL,
		APIKey:     strings.TrimSpace(apiKey),
		Headers:    headers,
		TimeoutSec: 20, // 探测用短超时
	}
	return runProviderMiniTest(target, model), nil
}

// PresetEntry is one built-in provider preset, as shown in the console.
type PresetEntry struct {
	Key           string
	Name          string
	BaseURL       string
	ModelIDs      []string
	ModelsFetcher *providerreg.ModelsFetcher
	Headers       map[string]string
	Notes         string
	FreeTier      bool
	Kind          string
	LoginFlow     bool
	AuthFlow      string
}

// PresetEntries lists the built-in presets in catalogue order.
func PresetEntries() []PresetEntry {
	reg := providerreg.Builtin()
	out := make([]PresetEntry, 0, reg.Len())
	for _, k := range reg.IDs() {
		p, _ := reg.Get(k)
		// Built-in runtime providers participate in the global provider system,
		// not the legacy "add a custom provider" preset picker. The additive
		// provider-system admin API exposes them later in the migration.
		if p.RuntimeBuiltin {
			continue
		}
		kind := p.Kind
		if kind == "" {
			if p.FreeTier {
				kind = "freeTier"
			} else {
				kind = "other"
			}
		}
		out = append(out, PresetEntry{Key: k, Name: p.Name, BaseURL: p.BaseURL, Headers: p.Headers, Notes: p.Notes, FreeTier: p.FreeTier, ModelIDs: p.ModelIDs, ModelsFetcher: p.ModelsFetcher, Kind: kind, LoginFlow: p.LoginFlow, AuthFlow: p.AuthFlow})
	}
	return out
}

// ResetCooldowns clears cooldown entries for a provider, optionally narrowed to
// one model, and returns how many were removed.
func ResetCooldowns(id, model string) int {
	Mu.Lock()
	var removed int
	for key := range Cooldowns {
		pid := strings.SplitN(key, ":", 2)[0]
		if pid != id {
			continue
		}
		if model != "" {
			m := key
			if i := strings.Index(key, ":"); i >= 0 {
				m = key[i+1:]
			}
			if m != model {
				continue
			}
		}
		delete(Cooldowns, key)
		removed++
	}
	if removed > 0 {
		saveProviderCooldownsLocked()
	}
	Mu.Unlock()
	if removed > 0 {
		log.Printf("admin: provider cooldown reset id=%s model=%s removed=%d", id, model, removed)
	}
	return removed
}

// runProviderMiniTest 用 Call 发一条安全预算（1024 tokens / 单条 "hi"）
// 非流式补全，返回测试结果 map：durationMs/model/ok,error?/inputTokens?/outputTokens?。
// 预算不能用 16：推理模型会整份耗在 reasoning 上导致上游空正文假失败（issue #32）。
func runProviderMiniTest(p *CustomProvider, model string) map[string]any {
	params := map[string]any{
		"model":      model,
		"max_tokens": 1024,
		"messages":   []any{map[string]any{"role": "user", "content": "hi"}},
	}
	started := time.Now()
	resp, err := Call(p, params, false)
	result := map[string]any{"durationMs": time.Since(started).Milliseconds(), "model": model}
	if err != nil {
		result["ok"] = false
		result["error"] = platform.Truncate(err.Error(), 300)
		return result
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	var obj map[string]any
	if json.Unmarshal(raw, &obj) == nil {
		if data, ok := obj["data"].(map[string]any); ok {
			obj = data
		}
		obj = openaichat.NormalizeResponse(obj)
	}
	result["ok"] = true
	if u := openaichat.ParseUsage(obj["usage"]); u.Valid {
		result["inputTokens"] = u.Prompt
		result["outputTokens"] = u.Completion
	}
	return result
}
