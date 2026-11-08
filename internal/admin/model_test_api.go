package admin

// 管理后台「模型可用性测试」API 三个入口共用一个小型 best-effort 测试器：
//
//	EP3 POST /admin/api/models/test  {model}                    → 任一模型单测
//	EP4 POST /admin/api/combos/test  {id, model?}               → 逐条目测组合
//	    （/admin/api/providers/models 见文件尾 HandleProviderModels —— EP2）
//
// QuickModelTest 是共享入口（EP3/EP4 内核）：max_tokens=16 + 单条 "hi" user 消息，
// 复用既有路由判定（provider 冷却/预算表是只读，测试旁路粘性会话与日志）。
// 上游失败 ≠ API 失败：一律 200 包裹 {success:true, data:{ok:false, error:...}}。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"routenest/internal/routing"
	"routenest/internal/zen"
	"strings"
	"sync"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/combos"
	"routenest/internal/config"
	"routenest/internal/egress"
	"routenest/internal/i18n"
	"routenest/internal/platform"
	"routenest/internal/providers"
	"routenest/internal/proxycore"
)

const (
	// QuickTestPoolTimeout 组合条目测试里 Cline 账号池路径的 ctx 超时（EP4 要求 ~20s）。
	QuickTestPoolTimeout = 20 * time.Second
	// quickTestProviderTimeoutSec EP4 里 provider 路径的兜底超时秒数（providers.Call 用
	// provider.TimeoutSec 建独立 client，不接 ctx；测试时夹紧到 20s）。
	quickTestProviderTimeoutSec = 20
	// quickTestMaxAccounts 一次测试最多尝试的账号数（轮询换号上限，避免测试烧池）。
	quickTestMaxAccounts = 2
)

// miniTestParams 一条安全预算测试请求体（与 handleProviderTest 同口径）。
// max_tokens 不能太小（issue #32）：推理模型会把小预算整份耗在 reasoning 上、
// 正文为空，上游必然回 500「empty response content」，测试就误报模型不可用；
// 且 16 恰好等于 minUpstreamMaxTokens，不会被 buildUpstreamBody 夹大兜底。
func miniTestParams(model string) map[string]any {
	return map[string]any{
		"model":      model,
		"max_tokens": 1024,
		"messages":   []any{map[string]any{"role": "user", "content": "hi"}},
	}
}

// ---- 同模型并行测试的批量守卫 --------------------------------------------

// quickTestLocks model → 互斥锁（仅内存态，不持久化）：并行 UI 测试同一模型时
// 串行化，避免多份 16-token 探测同时砸向上游/账号池造成伪 429。
var (
	quickTestMu    sync.Mutex
	quickTestLocks = map[string]*sync.Mutex{}
)

func quickTestLock(model string) *sync.Mutex {
	quickTestMu.Lock()
	defer quickTestMu.Unlock()
	mu := quickTestLocks[model]
	if mu == nil {
		mu = &sync.Mutex{}
		quickTestLocks[model] = mu
	}
	return mu
}

// ---- 共享测试器 ---------------------------------------------------------

// QuickModelTest 任一模型的 best-effort 小补全测试（EP3 入口；EP4 明细见
// RunQuickModelTest）。路由：自定义 provider → zen（免费）→ Cline 账号池，
// 与 combos.CallSingleModel 的派发语义一致。
func QuickModelTest(model string, forcedProvider *CustomProvider) map[string]any {
	return RunQuickModelTest(model, forcedProvider, "", 0)
}

// RunQuickModelTest QuickModelTest 的展开形式：
//   - forcedProvider 非空 → 只测这一家 provider（EP4 source=provider）。
//   - pinSource "provider" → 只走 forcedProvider；"cline" → 只走账号池；
//     "zen" → 只走 zen；"" 自动（provider → zen → 池，combos.CallSingleModel 语义）。
//   - poolTimeout > 0 时账号池调用带 ctx 超时（EP4 每条目 ~20s）。
//
// 返回 map 键：ok / upstream("provider"|"opencode"|"cline") / model / durationMs /
// error? / inputTokens? / outputTokens?。上游失败也返回（ok=false），不 panic。
func RunQuickModelTest(model string, forcedProvider *CustomProvider, pinSource string, poolTimeout time.Duration) map[string]any {
	model = strings.TrimSpace(model)
	start := time.Now()
	result := map[string]any{"ok": false, "model": model}
	setResult := func(upstream, errMsg string) {
		result["upstream"] = upstream
		result["durationMs"] = time.Since(start).Milliseconds()
		result["ok"] = errMsg == ""
		if errMsg != "" {
			result["error"] = platform.Truncate(errMsg, 300)
		}
	}
	recordSuccess := func(upstream string, resp *http.Response) {
		setResult(upstream, "")
		if resp == nil {
			return
		}
		if in, out, valid := readMiniTestUsage(resp); valid {
			result["inputTokens"] = in
			result["outputTokens"] = out
		}
	}

	if model == "" {
		setResult("", "model is required")
		return result
	}

	// Resolve client namespaces before dispatch. An explicit pin stays strict,
	// but provider probes continue to bypass selection cooldown and budgets.
	entry := ComboEntry{Model: model, Source: strings.TrimSpace(pinSource)}
	if forcedProvider != nil {
		if entry.Source == "" {
			entry.Source = "provider"
		}
		if entry.Source == "provider" {
			entry.Provider = forcedProvider.ID
			if entry.Provider == "" {
				entry.Provider = forcedProvider.Name
			}
		}
	}
	resolved, err := routing.ResolveComboEntry(entry)
	if err != nil {
		setResult("", err.Error())
		return result
	}
	model, pinSource = resolved.Model, resolved.Source
	if pinSource == "provider" && forcedProvider == nil {
		forcedProvider = FindEnabledProviderByName(resolved.Provider)
		if forcedProvider == nil {
			setResult(zen.UpstreamProvider, fmt.Sprintf("provider %q not found or disabled", resolved.Provider))
			return result
		}
	}

	// Serialize probes for the same offering, keeping sibling providers separate.
	lk := quickTestLock(routing.ComboEntryModelReference(resolved))
	lk.Lock()
	defer lk.Unlock()

	params := miniTestParams(model)

	// ---- provider 路径 ----
	tryProvider := func(p *CustomProvider) {
		result["providerId"] = p.ID
		result["providerName"] = p.Name
		resp, err := providers.Call(p, params, false)
		if err != nil {
			setResult(zen.UpstreamProvider, err.Error())
			return
		}
		recordSuccess(zen.UpstreamProvider, resp)
	}

	// ---- zen 路径 ----
	tryZen := func() {
		if !zen.GetConfig().Enabled {
			setResult(zen.UpstreamOpenCode, "OpenCode provider is disabled")
			return
		}
		zm, ok := zen.ResolveInfo(model)
		if !ok || !zen.IsFreeModel(zm) {
			setResult(zen.UpstreamOpenCode, fmt.Sprintf("model %q is not an available free zen model", model))
			return
		}
		resp, err := zen.Call(params, false)
		if err != nil {
			setResult(zen.UpstreamOpenCode, err.Error())
			return
		}
		recordSuccess(zen.UpstreamOpenCode, resp)
	}

	// ---- Cline 账号池路径 ----
	tryPool := func() {
		resp, err := quickPoolAttempt(model, params, poolTimeout)
		if err != nil {
			setResult(zen.UpstreamCline, err.Error())
			return
		}
		recordSuccess(zen.UpstreamCline, resp)
	}

	switch strings.TrimSpace(pinSource) {
	case "provider":
		if forcedProvider != nil {
			tryProvider(forcedProvider)
			return result
		}
		setResult(zen.UpstreamProvider, "provider not resolvable for test")
		return result
	case "cline":
		tryPool()
		return result
	case "zen":
		tryZen()
		return result
	}

	// ---- 自动派发（pinSource=""）：与 combos.CallSingleModel 的 auto 分支同序 ----
	if forcedProvider != nil {
		tryProvider(forcedProvider)
		return result
	}

	kind := combos.RouteKind(model)
	switch kind {
	case "reject":
		setResult(zen.UpstreamOpenCode, fmt.Sprintf("model %q is a paid opencode model; only free models are proxied", model))
		return result
	case "zen-degraded":
		setResult(zen.UpstreamOpenCode, fmt.Sprintf("model %q temporarily unavailable (opencode zen degraded)", model))
		return result
	case "zen":
		tryZen()
		return result
	}

	// cline 侧：先自定义 provider（若有），失败/缺席再走账号池。
	var lastProviderErr error
	for _, p := range providers.ResolveForModel(model) {
		resp, err := providers.Call(p, params, false)
		if err == nil {
			result["providerId"] = p.ID
			result["providerName"] = p.Name
			recordSuccess(zen.UpstreamProvider, resp)
			return result
		}
		lastProviderErr = err
		log.Printf("  model test: provider attempt failed name=%s model=%q: %v", p.Name, model, err)
	}
	if lastProviderErr != nil {
		log.Printf("  model test: model %q provider fallback to account pool: %v", model, lastProviderErr)
	}
	tryPool()
	return result
}

// quickPoolAttempt 账号池小补全尝试：最多试 quickTestMaxAccounts 个账号（严格
// 挑选 + 402/换号 + 429/换号 + 确定性 400 立即失败），与 combos.CallSingleModel 的池
// 分支同语义但不无限循环、不走模型冷却后的兜底挑选。
func quickPoolAttempt(model string, params map[string]any, timeout time.Duration) (*http.Response, error) {
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	tried := map[string]bool{}
	var lastErr error
	for i := 0; i < quickTestMaxAccounts; i++ {
		acc := accounts.PickAccountForModelStrict(model)
		if acc == nil {
			return nil, fmt.Errorf("model %q has no available account in the pool", model)
		}
		if tried[acc.AccountID] {
			break // 防呆：轮询边界不应撞同一个账号
		}
		tried[acc.AccountID] = true
		resp, usedAcc, err := routing.CallClineAPIWithAccountCtx(ctx, acc, params, false)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		var acctErr *routing.ClineAccountUnavailableError
		if errors.As(err, &acctErr) {
			continue // token 失效等：换下一个账号
		}
		if routing.IsDeterministicRequestError(err) {
			return nil, err // 换账号不会自愈的 payload 400
		}
		if apiErr, ok := err.(*egress.APIError); ok {
			switch apiErr.StatusCode {
			case http.StatusPaymentRequired: // 402：冷却该账号该模型后换号
				if usedAcc != nil {
					routing.SetModelCooldown(usedAcc, model, time.Now().Add(30*time.Minute))
				}
				continue
			case http.StatusTooManyRequests: // 429：模型级冷却已记录，换号
				continue
			}
		}
		return nil, err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("model %q unavailable on all eligible accounts", model)
	}
	return nil, lastErr
}

// readMiniTestUsage 从测试响应读取 token 用量（复用响应归一化与解析）。
func readMiniTestUsage(resp *http.Response) (prompt, completion int64, valid bool) {
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	var obj map[string]any
	if json.Unmarshal(raw, &obj) == nil {
		if d, ok := obj["data"].(map[string]any); ok {
			obj = d
		}
		obj = proxycore.NormalizeOpenAIResponse(obj)
	}
	if u := proxycore.ParseTokenUsage(obj["usage"]); u.Valid {
		return u.Prompt, u.Completion, true
	}
	return 0, 0, false
}

// ---- EP3：单模型测试 ----------------------------------------------------

// POST /admin/api/models/test  body: {model}
// data: QuickModelTest 结果（ok / upstream / model / durationMs / error? /
// inputTokens? / outputTokens?）。上游失败仍是 200 + ok:false。
func HandleAdminModelTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: i18n.TAPI(r, "method_not_allowed")})
		return
	}
	var req struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPI(w, http.StatusBadRequest, apiResponse{Error: i18n.TAPI(r, "invalid_json")})
		return
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		writeAPI(w, http.StatusBadRequest, apiResponse{Error: i18n.TAPI(r, "model_id_required")})
		return
	}
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Data: QuickModelTest(model, nil)})
}

// ---- EP4：组合测试 ------------------------------------------------------

// FindEnabledProviderByName accepts stable IDs first, then legacy display names.
// A disabled or deleted identity cannot fall back to a sibling's display name.
// Probes deliberately ignore provider selection cooldowns and budget limits.
func FindEnabledProviderByName(name string) *CustomProvider {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	list := providers.List()
	for _, p := range list {
		if p.ID == name {
			if !p.Enabled {
				return nil
			}
			cp := p
			return &cp
		}
	}
	if config.ProviderModelIDReserved(name) || providers.GeneratedProviderIDPin(name) {
		return nil
	}
	for _, p := range list {
		if p.Enabled && strings.EqualFold(strings.TrimSpace(p.Name), name) {
			cp := p
			return &cp
		}
	}
	return nil
}

// comboEntryTest 按条目的上游固定语义测一个条目（不改动组合本身）。
func comboEntryTest(entry ComboEntry) map[string]any {
	resolved, err := routing.ResolveComboEntry(entry)
	if err != nil {
		return map[string]any{
			"ok": false, "model": strings.TrimSpace(entry.Model), "upstream": "", "durationMs": 0,
			"error": err.Error(),
		}
	}
	entry = resolved
	m := strings.TrimSpace(entry.Model)
	switch strings.TrimSpace(entry.Source) {
	case "provider":
		name := strings.TrimSpace(entry.Provider)
		p := FindEnabledProviderByName(name)
		if p == nil {
			return map[string]any{
				"ok": false, "model": m, "upstream": zen.UpstreamProvider, "durationMs": 0,
				"error": fmt.Sprintf("provider %q not found or disabled", name),
			}
		}
		cp := *p
		if cp.TimeoutSec <= 0 || cp.TimeoutSec > quickTestProviderTimeoutSec {
			cp.TimeoutSec = quickTestProviderTimeoutSec // 测试专用超时夹紧
		}
		return RunQuickModelTest(m, &cp, "provider", QuickTestPoolTimeout)
	case "cline":
		return RunQuickModelTest(m, nil, "cline", QuickTestPoolTimeout)
	case "zen":
		return RunQuickModelTest(m, nil, "zen", QuickTestPoolTimeout)
	default:
		return QuickModelTest(m, nil)
	}
}

// comboTestResult preserves the stored identity around a possibly narrowed probe.
func comboTestResult(entry, probeEntry ComboEntry) map[string]any {
	out := comboEntryTest(probeEntry)
	// Keep the stored identity for UI result association; dispatch uses the raw ID.
	out["model"] = strings.TrimSpace(entry.Model)
	out["source"] = strings.TrimSpace(entry.Source)
	if name := strings.TrimSpace(entry.Provider); name != "" {
		out["provider"] = name
	}
	return out
}

// POST /admin/api/combos/test  body: {id, model?}
// data: {comboId, results:[{model, source, ok, durationMs, upstream, error?,
// inputTokens?, outputTokens?}], allOk}。禁用条目跳过；model 给定时只测
// 匹配该模型的第一条可用条目。测试不改动组合（纯读）。
func HandleComboTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: i18n.TAPI(r, "method_not_allowed")})
		return
	}
	var req struct {
		ID    string `json:"id"`
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPI(w, http.StatusBadRequest, apiResponse{Error: i18n.TAPI(r, "invalid_json")})
		return
	}
	combo, ok := combos.FindCombo(req.ID)
	if !ok {
		writeAPI(w, http.StatusNotFound, apiResponse{Error: i18n.TAPI(r, "combo_not_found")})
		return
	}
	filter := strings.TrimSpace(req.Model)
	filterEntry := ComboEntry{Model: filter}
	if filter != "" {
		var err error
		filterEntry, err = routing.ResolveComboEntry(filterEntry)
		if err != nil {
			writeAPI(w, http.StatusBadRequest, apiResponse{Error: err.Error()})
			return
		}
	}

	results := make([]map[string]any, 0, len(combo.Entries))
	for _, entry := range combo.Entries {
		if entry.Disabled {
			continue // 禁用条目不测
		}
		m := strings.TrimSpace(entry.Model)
		if m == "" {
			continue
		}
		probeEntry := entry
		if filter != "" {
			resolved, err := routing.ResolveComboEntry(entry)
			if err != nil {
				if m != filter {
					continue
				}
			} else {
				if resolved.Model != filterEntry.Model || filterEntry.Source != "" && resolved.Source != filterEntry.Source {
					continue
				}
				if filterEntry.Source == "provider" && resolved.Provider != filterEntry.Provider {
					actual := FindEnabledProviderByName(resolved.Provider)
					wanted := FindEnabledProviderByName(filterEntry.Provider)
					if actual == nil || wanted == nil || actual.ID != wanted.ID {
						continue
					}
				}
				if filterEntry.Source == "provider" {
					// Carry the matched identity into the probe so a legacy name
					// cannot select a sibling after a concurrent rename or disable.
					probeEntry = resolved
					probeEntry.Provider = filterEntry.Provider
					if wanted := FindEnabledProviderByName(filterEntry.Provider); wanted != nil {
						probeEntry.Provider = wanted.ID
					}
				}
			}
		}
		results = append(results, comboTestResult(entry, probeEntry))
	}
	if filter != "" && len(results) == 0 {
		writeAPI(w, http.StatusNotFound, apiResponse{Error: fmt.Sprintf("enabled entry %q not found in combo %q", filter, combo.ID)})
		return
	}
	if len(results) == 0 {
		writeAPI(w, http.StatusBadRequest, apiResponse{Error: "combo has no enabled entries"})
		return
	}
	allOk := true
	for _, res := range results {
		if o, _ := res["ok"].(bool); !o {
			allOk = false
		}
	}
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Data: map[string]any{
		"comboId": combo.ID,
		"results": results,
		"allOk":   allOk,
	}})
}

// ---- EP2：provider 模型发现（/admin/api/providers/models）--------------

// POST /admin/api/providers/models  body: {id} 或 {baseURL, apiKey?, headers?}
// 发现 provider 的模型列表（GET {baseURL}/models）。data 成功：
// {models:[{id:"..."}], count:N}；失败：{error:"...", models:[]}（200 包裹）。
// 纯探测，不写任何状态。发现逻辑在 providers.DiscoverModels（唯一入口）。
func HandleProviderModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: i18n.TAPI(r, "method_not_allowed")})
		return
	}
	var req struct {
		ID      string            `json:"id"`
		BaseURL string            `json:"baseURL"`
		APIKey  string            `json:"apiKey"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPI(w, http.StatusBadRequest, apiResponse{Error: i18n.TAPI(r, "invalid_json")})
		return
	}

	// Request values override the stored provider; a non-nil header map replaces
	// the stored headers, and empty header values delete headers.
	baseURL := strings.TrimSpace(strings.TrimRight(req.BaseURL, "/"))
	overrides := providers.DiscoveryOverrides{BaseURL: baseURL, APIKey: strings.TrimSpace(req.APIKey), Headers: req.Headers}
	prov := providers.CustomProvider{BaseURL: baseURL, APIKey: strings.TrimSpace(req.APIKey), Headers: req.Headers}
	found := false
	if req.ID != "" {
		if p := providers.Find(req.ID); p != nil {
			found = true
			prov = *p
		}
	}
	if found {
		if prov.BaseURL == "" && overrides.BaseURL == "" {
			writeAPI(w, http.StatusNotFound, apiResponse{Error: "provider not found"})
			return
		}
	} else {
		// A draft carries its own values; keep its header map so empty values
		// still delete headers.
		overrides = providers.DiscoveryOverrides{Headers: req.Headers}
		if baseURL == "" {
			writeAPI(w, http.StatusBadRequest, apiResponse{Error: "id or baseURL is required"})
			return
		}
	}
	if effective := strings.TrimSpace(strings.TrimRight(firstNonEmpty(overrides.BaseURL, prov.BaseURL), "/")); !strings.HasPrefix(effective, "http://") && !strings.HasPrefix(effective, "https://") {
		writeAPI(w, http.StatusBadRequest, apiResponse{Error: "baseURL must start with http:// or https://"})
		return
	}

	entries, err := providers.DiscoverModels(prov, overrides)
	if err != nil {
		writeAPI(w, http.StatusOK, apiResponse{Success: true, Data: map[string]any{
			"error":  err.Error(),
			"models": []any{},
		}})
		return
	}
	out := make([]map[string]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]string{"id": e.ID})
	}
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Data: map[string]any{
		"models": out,
		"count":  len(out),
	}})
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
