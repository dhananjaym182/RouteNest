package providers

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"routenest/internal/connections"
	"routenest/internal/httpx"
	"routenest/internal/i18n"
	"routenest/internal/platform"
	"routenest/internal/protocol/openaichat"
	"routenest/internal/providerreg"
	"routenest/internal/transport"
)

// ============================================================================
// Admin API handlers
// ============================================================================

func HandleList(w http.ResponseWriter, r *http.Request) {
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{
		"providers":     List(),
		"health":        providerHealthSnapshot(),
		"accountHealth": accountRecentOutcomes(),
	}})
}

func HandleSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		httpx.WriteAPI(w, http.StatusMethodNotAllowed, httpx.Response{Error: i18n.TAPI(r, "method_not_allowed")})
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: err.Error()})
		return
	}
	defer r.Body.Close()
	var p CustomProvider
	if err := json.Unmarshal(body, &p); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "invalid_json"})
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	p.BaseURL = strings.TrimSpace(strings.TrimRight(p.BaseURL, "/"))
	p.APIKey = strings.TrimSpace(p.APIKey)
	if p.Name == "" || p.BaseURL == "" {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "name and baseURL are required"})
		return
	}
	if !strings.HasPrefix(p.BaseURL, "http://") && !strings.HasPrefix(p.BaseURL, "https://") {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "baseURL must start with http:// or https://"})
		return
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
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: proxyErr.Error()})
		return
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
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: `tokenWindow must be "monthly" or "daily"`})
		return
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
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"provider": saved}})
}

func HandleDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		httpx.WriteAPI(w, http.StatusMethodNotAllowed, httpx.Response{Error: i18n.TAPI(r, "method_not_allowed")})
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "invalid_json"})
		return
	}
	if !Delete(req.ID) {
		httpx.WriteAPI(w, http.StatusNotFound, httpx.Response{Error: "provider not found"})
		return
	}
	SyncModels() // 清理该 provider 声明的池内模型条目
	clearProviderRecheck(req.ID)
	log.Printf("admin: provider deleted id=%s", req.ID)
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true})
}

// HandleTest 发一条 min-token 请求验证 provider 可用性。
// 两侧重载：
//   - {id, model?}            → 已保存的 provider（model 非空时覆盖默认 ModelIDs[0]）
//   - {baseURL, apiKey?, headers?, model?} → 未保存配置的临时测试：构造临时
//     CustomProvider 走同一 Call 路径，不落盘、不产生新状态。
//
// 上游失败 ≠ API 失败：一律 200 包裹 {success:true, data:{ok:false, error:...}}。
func HandleTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		httpx.WriteAPI(w, http.StatusMethodNotAllowed, httpx.Response{Error: i18n.TAPI(r, "method_not_allowed")})
		return
	}
	var req struct {
		ID      string            `json:"id"`
		BaseURL string            `json:"baseURL"`
		APIKey  string            `json:"apiKey"`
		Headers map[string]string `json:"headers"`
		Model   string            `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "invalid_json"})
		return
	}
	model := strings.TrimSpace(req.Model)

	if strings.TrimSpace(req.BaseURL) != "" {
		// 未保存配置路径：一次性 provider（不进 registry，不落盘）。
		baseURL := strings.TrimSpace(strings.TrimRight(req.BaseURL, "/"))
		if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
			httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "baseURL must start with http:// or https://"})
			return
		}
		if model == "" {
			// 临时 provider 未声明模型列表，空 model 无从发起补全，需给出明确说明。
			httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{
				"ok": false, "model": "", "durationMs": 0,
				"error": "model is required when testing an unsaved provider",
			}})
			return
		}
		target := &CustomProvider{
			BaseURL:    baseURL,
			APIKey:     strings.TrimSpace(req.APIKey),
			Headers:    req.Headers,
			TimeoutSec: 20, // 探测用短超时
		}
		httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: runProviderMiniTest(target, model)})
		return
	}

	Load()
	Mu.Lock()
	var target *CustomProvider
	for _, p := range CustomProviders {
		if p.ID == req.ID {
			cp := *cloneProvider(p)
			target = &cp
			break
		}
	}
	Mu.Unlock()
	if target == nil {
		httpx.WriteAPI(w, http.StatusNotFound, httpx.Response{Error: "provider not found"})
		return
	}
	if model == "" && len(target.ModelIDs) > 0 {
		model = target.ModelIDs[0]
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: runProviderMiniTest(target, model)})
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

func HandlePresets(w http.ResponseWriter, r *http.Request) {
	type presetOut struct {
		Key           string                     `json:"key"`
		Name          string                     `json:"name"`
		BaseURL       string                     `json:"baseURL"`
		ModelIDs      []string                   `json:"modelIDs,omitempty"`
		ModelsFetcher *providerreg.ModelsFetcher `json:"modelsFetcher,omitempty"`
		Headers       map[string]string          `json:"headers,omitempty"`
		Notes         string                     `json:"notes,omitempty"`
		FreeTier      bool                       `json:"freeTier"`
		Kind          string                     `json:"kind"`
		LoginFlow     bool                       `json:"loginFlow,omitempty"`
		AuthFlow      string                     `json:"authFlow,omitempty"`
	}
	reg := providerreg.Builtin()
	out := make([]presetOut, 0, reg.Len())
	for _, k := range reg.IDs() {
		p, _ := reg.Get(k)
		kind := p.Kind
		if kind == "" {
			if p.FreeTier {
				kind = "freeTier"
			} else {
				kind = "other"
			}
		}
		out = append(out, presetOut{Key: k, Name: p.Name, BaseURL: p.BaseURL, Headers: p.Headers, Notes: p.Notes, FreeTier: p.FreeTier, ModelIDs: p.ModelIDs, ModelsFetcher: p.ModelsFetcher, Kind: kind, LoginFlow: p.LoginFlow, AuthFlow: p.AuthFlow})
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"presets": out}})
}
