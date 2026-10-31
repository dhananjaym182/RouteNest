package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/dispatch"
	"cline-go-proxy/internal/domain"
	"cline-go-proxy/internal/egress"
	"cline-go-proxy/internal/platform"
	"cline-go-proxy/internal/protocol/openaichat"
	"cline-go-proxy/internal/providers"
	clineupstream "cline-go-proxy/internal/upstream/cline"
	"cline-go-proxy/internal/zen"
)

const (
	// DefaultMaxTokens 是上游请求的默认/上限输出 token 数。
	//
	// 从 128000 降到 32000：日志实测 poolside/laguna-s-2.1:free 直接 400 ——
	// "maximum context length is 262144 tokens, however you requested about 263503
	// tokens (88799 text input, 46704 tool input, 128000 in the output)"。
	// 也就是说 128k 的输出预留本身就能把请求顶出上下文窗口，让本来可用的模型直接失败，
	// 并让其余模型陷入超长预填充/静默。可用 ROUTENEST_MAX_TOKENS（旧名 CLINE2API_MAX_TOKENS 仍兜底）调整。
	DefaultMaxTokens       = 32000
	DefaultReasoningEffort = "high"
	fallbackDefaultModel   = "z-ai/glm-5.3-flash"
	FreeModelPrimary       = "z-ai/glm-5.3-flash"
	FreeModelFallback      = "deepseek/deepseek-v4-flash"
	freeModelLastResort    = "cline-free/longcat-2.0"
)

// MinUpstreamMaxTokens 上游对输出 token 的硬下限：Cline 免费模型经 OpenRouter
// 转发时（如 meta/muse-spark），max_output_tokens < 16 会被上游直接 400。
const MinUpstreamMaxTokens = 16

// EnvWithLegacy resolves a tunable's environment variable from its
// ROUTENEST_-prefixed name, falling back to the pre-rename CLINE2API_-prefixed
// name so existing deployments keep working.
func EnvWithLegacy(name string) string {
	if v, ok := os.LookupEnv("ROUTENEST_" + name); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return os.Getenv("CLINE2API_" + name)
}

// envRoutingInt reads a positive integer override by new/legacy env-name pair.
func envRoutingInt(name string, def int) int {
	if v := strings.TrimSpace(EnvWithLegacy(name)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// envRoutingString reads a string override by new/legacy env-name pair.
func envRoutingString(name string) string {
	return strings.TrimSpace(EnvWithLegacy(name))
}

// MaxOutputTokens 是客户端请求中 max_tokens 的上限。
// 客户端显式要更大的输出（例如 128k）也会被夹到这里：预留过大的输出空间会把输入+输出
// 顶出上游上下文窗口（实测 400），收益却接近没有。
func MaxOutputTokens() int {
	return envRoutingInt("MAX_TOKENS", DefaultMaxTokens)
}

// ReasoningEffort 返回下发给上游的推理强度。默认保持 "high"，
// 可用 ROUTENEST_REASONING_EFFORT（旧名 CLINE2API_REASONING_EFFORT 仍兜底）
// =low|medium|high|none 覆盖：推理模型在大上下文下的思考阶段可能是长时间静默的
// 来源之一，这是用户侧最简单的延迟开关。
func ReasoningEffort() string {
	if v := envRoutingString("REASONING_EFFORT"); v != "" {
		return v
	}
	return DefaultReasoningEffort
}

// ClampMaxTokens 把请求的输出上限夹到 [1, MaxOutputTokens()]。
func ClampMaxTokens(n int) int {
	limit := MaxOutputTokens()
	if n <= 0 || n > limit {
		return limit
	}
	return n
}

// FreeModelChain 是 model="free" 时的降级顺序。
// 顺序依据 Artificial Analysis Intelligence Index v4.1.1：
// glm-5.3-flash 57 > deepseek-v4-flash 0731 52 > longcat-2.0 34。
var FreeModelChain = []string{FreeModelPrimary, FreeModelFallback, freeModelLastResort}

// builtinModels 是内置默认模型列表（不可删除），仅作为离线 / 未同步时的 fallback。
// 同步 Cline 官方推荐模型成功后，AllModels 以远程模型为主。
var builtinModels = []domain.Model{
	{ID: "z-ai/glm-5.3-flash", Provider: "z-ai", Cost: "free", Status: "active", Custom: false},
	{ID: "cline-free/longcat-2.0", Provider: "cline-free", Cost: "free", Status: "active", Custom: false},
	{ID: "cline-pass/glm-5.2", Provider: "zai", Cost: "pass", Status: "active", Custom: false},
	{ID: "cline-pass/deepseek-v4-flash", Provider: "deepseek", Cost: "pass", Status: "active", Custom: false},
	{ID: "cline-pass/qwen3.7-max", Provider: "qwen", Cost: "pass", Status: "active", Custom: false},
	{ID: "deepseek/deepseek-v4-flash", Provider: "deepseek", Cost: "free", Status: "active", Custom: false},
	{ID: "poolside/laguna-s-2.1:free", Provider: "poolside", Cost: "free", Status: "active", Custom: false},
}

// AllModels 返回可用模型列表：
//   - 已同步远程模型：Cline 远程（Source=remote）+ opencode 同步（Source=zen）+ 用户自定义
//   - 未同步 / 离线：内置 fallback（Cline + zen 种子表）+ 用户自定义
func AllModels() []domain.Model {
	p := accounts.LoadPool()
	accounts.PoolMu.Lock()
	defer accounts.PoolMu.Unlock()

	var custom []domain.Model
	var remote []domain.Model
	var zenModels []domain.Model
	for _, m := range p.Models {
		switch m.Source {
		case "remote":
			remote = append(remote, m)
		case "zen":
			zenModels = append(zenModels, m)
		default:
			custom = append(custom, m)
		}
	}

	if len(remote) > 0 || len(zenModels) > 0 || zen.RemoteActive() {
		result := make([]domain.Model, 0, len(remote)+len(zenModels)+len(custom))
		result = append(result, remote...)
		result = append(result, zenModels...)
		result = append(result, custom...)
		return result
	}

	builtin := make([]domain.Model, 0, len(builtinModels)+len(zen.SeedModels))
	builtin = append(builtin, builtinModels...)
	builtin = append(builtin, zen.BuiltinModels()...)

	result := make([]domain.Model, 0, len(builtin)+len(custom))
	result = append(result, builtin...)
	result = append(result, custom...)
	return result
}

// BuildModelsList 组装客户端发现用的模型列表（/v1/models、/models）：
// 池内模型（已停用的排除，onlyFree 时仅免费）+ 启用中的具名模型组合。
//
// 组合不插入 accounts.Pool.Models（避免污染上游归属 / 同步 / 删除 / Cline-Zen 分流判定），
// 只在此发现层追加，owned_by="combo" 便于客户端区分。组合可能跨免费/付费源，故
// 不受 onlyFree 过滤影响（组合是管理员显式选择的集合，不是单一计费模型）。
func BuildModelsList(onlyFree bool) []map[string]any {
	all := AllModels()
	list := make([]map[string]any, 0, len(all))
	for _, m := range all {

		if m.Disabled {
			continue
		}

		if ExcludedForListing(m.ID) {
			continue
		}
		if onlyFree && !IsFreeModelEntry(m) {
			continue
		}
		ownedBy := "cline"
		if m.Source == "zen" || m.Provider == "opencode" {
			ownedBy = "opencode"
		}
		entry := map[string]any{
			"id":       m.ID,
			"object":   "model",
			"created":  time.Now().UnixMilli(),
			"owned_by": ownedBy,
		}
		if m.Context > 0 {
			entry["context_length"] = m.Context
		}
		if m.Output > 0 {
			entry["max_output_tokens"] = m.Output
		}
		if len(m.ThinkingLevels) > 0 {
			entry["thinking_levels"] = m.ThinkingLevels
		}
		if len(m.InputModalities) > 0 {
			entry["input_modalities"] = m.InputModalities
		}
		if len(m.OutputModalities) > 0 {
			entry["output_modalities"] = m.OutputModalities
		}
		list = append(list, entry)
	}
	for _, c := range EnabledCombosProvider() {
		if ExcludedForListing(c.ID) {
			continue
		}
		list = append(list, map[string]any{
			"id":       c.ID,
			"object":   "model",
			"created":  time.Now().UnixMilli(),
			"owned_by": "combo",
		})
	}
	list = append(list, AliasListingEntries()...)
	return list
}

// DefaultModel 返回用户设置的默认模型；未设置时优先回退到第一个远程 free 模型，
// 否则用内置 fallback。
func DefaultModel() string {
	p := accounts.LoadPool()
	accounts.PoolMu.Lock()
	defer accounts.PoolMu.Unlock()

	if p.DefaultModel != "" {
		defaultDisabled := false
		for _, m := range p.Models {
			if m.ID == p.DefaultModel && m.Disabled {
				defaultDisabled = true
				break
			}
		}
		if !defaultDisabled && (!RemoteModelsActive() || LiveModelSet(p)[p.DefaultModel]) {
			return p.DefaultModel
		}
	}

	for _, m := range p.Models {
		if m.Source == "remote" && m.Cost == "free" && !m.Disabled {
			return m.ID
		}
	}

	if RemoteModelsActive() {
		for _, m := range p.Models {
			if m.Status == "active" && !m.Disabled {
				return m.ID
			}
		}
	}

	for _, m := range builtinModels {
		if m.Cost == "free" {
			return m.ID
		}
	}
	return fallbackDefaultModel
}

var PassThroughKeys = []string{
	"tools", "tool_choice", "parallel_tool_calls", "functions", "function_call",
	"temperature", "top_p", "top_k", "stop", "presence_penalty", "frequency_penalty",
	"response_format", "user", "n", "logit_bias", "seed", "logprobs", "top_logprobs",
	"stream_options", "metadata",
}

// IsFreeModelEntry 判断模型是否免费（/models 过滤用）：
// Cost 直接标记 free，或 zen 来源且命中免费判定（种子白名单兜底）。
func IsFreeModelEntry(m domain.Model) bool {
	if m.Cost == "free" {
		return true
	}
	return zen.IsSource(m) && zen.IsFreeModel(m)
}

// ModelMaxOutputLimit 返回模型已知的最大输出 token 硬上限（0=未知，不封顶）。
// 已知硬限制表优先（gemini-3.8-flash 等）；其次取池中该模型的 Output 元数据
// （remote 同步 / 管理页设置 / 用户自定义模型）。zen 模型的 Output 是压缩预算
// 用的估值（未知模型默认 32768），作为硬上限会误伤长输出，跳过。
func ModelMaxOutputLimit(model string) int {
	if meta, ok := LookupClineModelMeta(model); ok && meta.Output > 0 {
		return meta.Output
	}
	if strings.TrimSpace(model) == "" {
		return 0
	}
	p := accounts.LoadPool()
	accounts.PoolMu.Lock()
	defer accounts.PoolMu.Unlock()
	for _, m := range p.Models {
		if m.ID == model && !zen.IsSource(m) && m.Output > 0 {
			return m.Output
		}
	}
	return 0
}

func BuildUpstreamBody(params map[string]any, stream bool) map[string]any {
	sessionID := fmt.Sprintf("sess_%d", time.Now().UnixMilli())

	maxTokens := MaxOutputTokens()
	source := ""
	if mt, ok := openaichat.NumericInt(params["max_tokens"]); ok {
		maxTokens, source = ClampMaxTokens(mt), "max_tokens"
	} else if mt, ok := openaichat.NumericInt(params["max_completion_tokens"]); ok {
		maxTokens, source = ClampMaxTokens(mt), "max_completion_tokens"
	}

	if maxTokens < MinUpstreamMaxTokens {
		if source != "" {
			log.Printf("  clamp %s=%d -> %d (upstream requires >= %d)", source, maxTokens, MaxOutputTokens(), MinUpstreamMaxTokens)
		}
		maxTokens = MaxOutputTokens()
	}

	model := DefaultModel()
	if m, ok := params["model"].(string); ok && m != "" {
		model = m
	}

	if limit := ModelMaxOutputLimit(model); limit > 0 && maxTokens > limit {
		log.Printf("  clamp max_tokens=%d -> %d (model %q output limit)", maxTokens, limit, model)
		maxTokens = limit
	}

	body := map[string]any{
		"model":            model,
		"max_tokens":       maxTokens,
		"session_id":       sessionID,
		"reasoning_effort": ReasoningEffort(),
	}

	if msgsRaw, ok := params["messages"]; ok {
		if msgsArr, ok := msgsRaw.([]any); ok {
			body["messages"] = openaichat.SanitizeMessages(msgsArr)
		} else {
			body["messages"] = msgsRaw
		}
	}

	if stream {
		body["stream"] = true
	}

	if re, ok := params["reasoning_effort"].(string); ok && re != "" {
		body["reasoning_effort"] = re
	} else if re, ok := params["ReasoningEffort"].(string); ok && re != "" {
		body["reasoning_effort"] = re
	}

	if re, _ := body["reasoning_effort"].(string); re == "none" || re == "" {
		delete(body, "reasoning_effort")
	}

	for _, key := range PassThroughKeys {
		if val, ok := params[key]; ok {
			body[key] = val
		}
	}

	openaichat.SanitizeTools(body)

	return body
}

type ClineAccountUnavailableError struct {
	err error
}

func (e *ClineAccountUnavailableError) Error() string {
	return e.err.Error()
}

func (e *ClineAccountUnavailableError) Unwrap() error {
	return e.err
}

type FreeModelUnavailableError struct {
	Message string
}

func (e *FreeModelUnavailableError) Error() string {
	return e.Message
}

// IsDeterministicRequestError 判断 err 是否为「换账号/换模型重试也无法自愈」的
// 上游确定性 400：invalid-request 语义（invalid_request_error 类型、invalid_request
// 伪码、网关字段校验 "Invalid input"，如工具可选字段被序列化为 null）。
// 上下文超限 400 显式排除（IsContextLengthError）：那类错误有压缩+重试的自愈路径，
// 必须保留现有的 truncate/retry 行为。限流 429/5xx 不在此列。
func IsDeterministicRequestError(err error) bool {
	if err == nil || IsContextLengthError(err) {
		return false
	}
	var apiErr *egress.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		return false
	}
	msg := strings.ToLower(apiErr.Message)
	return strings.Contains(msg, "invalid_request_error") ||
		strings.Contains(msg, "invalid_request") ||
		strings.Contains(msg, "invalid input")
}

// ServedByParam 是 CallClineAPI 族回写在 params 上的「实际服务方」标记：
// 自定义 provider 成功服务时写入 zen.UpstreamProvider。仅用于请求日志归因——
// BuildUpstreamBody / providers.Call / zen.Call 只拷贝白名单键，不会外发。
const ServedByParam = "__served_by"

// ProviderNameParam 与 ServedByParam 配套：自定义 provider 成功服务时回写实际
// 命中之家的显示名，供 stampUpstream 归因到请求日志的 providerName 字段。
const ProviderNameParam = "__served_by_name"

func CallClineAPI(params map[string]any, stream bool) (*http.Response, *domain.Account, error) {
	model, _ := params["model"].(string)
	delete(params, ServedByParam)
	delete(params, ProviderNameParam)
	delete(params, providers.ProviderIDParam)

	if bare, pin := providers.SplitPin(model); pin != "" {
		params["model"] = bare
		if ConfigProvider().OnlyFree && !ModelFreeOnlyEligible(bare) {
			return nil, nil, fmt.Errorf("model %q is not eligible while onlyFree is enabled (access class: %s)", bare, ModelRouteAccess(bare).Class)
		}
		resp, perr, attempted, provName := providers.CallAPINamed(params, stream, pin)
		switch {
		case attempted && perr == nil:
			params[ServedByParam] = zen.UpstreamProvider
			params[ProviderNameParam] = provName
			return resp, nil, nil
		case !attempted:
			return nil, nil, fmt.Errorf("no enabled provider named %q serves model %q", pin, bare)
		default:
			return nil, nil, perr
		}
	}
	if model == "free" {
		resp, acc, err := CallFreeClineAPI(params, stream)
		if err != nil {

			lastChainModel, _ := params["model"].(string)
			fbResp, _ := ClineFailoverToZen(params, stream)
			if fbResp != nil {
				return fbResp, nil, nil
			}
			if lastChainModel != "" {
				params["model"] = lastChainModel
			}
		}
		return resp, acc, err
	}

	if zm, ok := zen.ResolveInfo(model); ok && zen.IsFreeModel(zm) {
		log.Printf("  zen failover: rewriting zen model %q to cline free chain", model)
		return CallFreeClineAPI(params, stream)
	}

	sorted := accounts.SortModelsByAvailability(ModelFallbackChain(model))
	chain := make([]string, 0, len(sorted)+1)
	chain = append(chain, model)
	for _, m := range sorted {
		if m != model {
			chain = append(chain, m)
		}
	}
	if ConfigProvider().OnlyFree {
		eligible := make([]string, 0, len(chain))
		for _, m := range chain {
			if ModelFreeOnlyEligible(m) {
				eligible = append(eligible, m)
			} else {
				log.Printf("  onlyFree: skipping model %q (access class %s)", m, ModelRouteAccess(m).Class)
			}
		}
		chain = eligible
		if len(chain) == 0 {
			return nil, nil, fmt.Errorf("onlyFree is enabled and model %q has no verified free route", model)
		}
	}
	// 会话亲和（管理员开关，默认关闭）：同一会话在 TTL 内钉到同一 Cline 账号。
	var affinitySk string
	if ConfigProvider().SessionAffinity.Enabled {
		affinitySk = providers.SessionKeyFromParams(params)
	}

	rounds, credCap, roundInterval := egress.RetryRoundSettings()
	for round := 0; round < rounds; round++ {
		if round > 0 && roundInterval > 0 {
			log.Printf("  retry round %d/%d for %q after %s cooldown window", round+1, rounds, model, roundInterval)
			time.Sleep(roundInterval)
		}
		for _, m := range chain {

			if pResp, pErr, attempted, provName := providers.CallAPI(WithModel(params, m), stream); attempted {
				if pErr == nil {
					if m != model {
						log.Printf("  model fallback: %q unavailable, serving via provider %q", model, m)
					}
					params["model"] = m
					params[ServedByParam] = zen.UpstreamProvider
					params[ProviderNameParam] = provName
					return pResp, nil, nil
				}

				if cause, stopped := egress.StopCause(pErr); stopped {
					return nil, nil, cause
				}
			}

			pickAcc := accounts.PickAccountForModelStrict
			if m != model {
				pickAcc = accounts.PickAccountForModelLeastUsed
			}
			attempts := 0
			for {
				if credCap > 0 && attempts >= credCap {
					break
				}
				acc, pinned := accounts.PickAffinityAccount(affinitySk, m)
				if !pinned {
					acc = pickAcc(m)
				}
				if acc == nil {
					break
				}
				attempts++
				resp, usedAcc, err := CallClineAPIWithAccountCtx(context.Background(), acc, WithModel(params, m), stream)
				accounts.ObserveAffinityOutcome(affinitySk, usedAcc, err)
				if err == nil {
					if m != model {
						log.Printf("  model fallback: %q cooling on all accounts, serving via %q", model, m)
					}
					params["model"] = m
					return resp, usedAcc, nil
				}
				var accountErr *ClineAccountUnavailableError
				if errors.As(err, &accountErr) {
					continue
				}

				if stop, cooldown, matched := egress.RuleDecision(err); matched {
					if cooldown && usedAcc != nil {
						SetModelCooldown(usedAcc, m, egress.RuleCooldownUntil(egress.ErrorStatus(err)))
					}
					if stop {
						egress.LogRuleHit("stop", egress.ErrorStatus(err), "pool model "+m)
						return nil, usedAcc, err
					}
					egress.LogRuleHit("continue", egress.ErrorStatus(err), "pool model "+m)
				}
				apiErr, ok := err.(*egress.APIError)
				if ok && apiErr.StatusCode == http.StatusPaymentRequired {

					SetModelCooldown(usedAcc, m, time.Now().Add(30*time.Minute))
					log.Printf("  model %q on %s: insufficient credits (402), cooling for 30m, trying next account", m, usedAcc.Email)
					continue
				}
				if !ok || apiErr.StatusCode != http.StatusTooManyRequests {

					if !hasAnyFallbackLeft(model, m) {
						return nil, usedAcc, err
					}
					break
				}

			}
		}
	}

	if model != "free" && !isFreeAliasModel(model) {
		fp := WithModel(params, "free")
		if resp, acc, err := CallFreeClineAPI(fp, stream); err == nil {
			log.Printf("  auto fallback: all configured options failed for %q, served by free chain", model)
			if fm, ok := fp["model"].(string); ok && fm != "" {
				params["model"] = fm
			}
			if v, ok := fp[ServedByParam].(string); ok {
				params[ServedByParam] = v
			} else {
				delete(params, ServedByParam)
			}
			if v, ok := fp[ProviderNameParam].(string); ok {
				params[ProviderNameParam] = v
			} else {
				delete(params, ProviderNameParam)
				delete(params, providers.ProviderIDParam)
			}
			return resp, acc, nil
		}
	}

	fbResp, _ := ClineFailoverToZen(params, stream)
	if fbResp != nil {
		return fbResp, nil, nil
	}
	if model != "" {
		params["model"] = model
	}
	if hasActiveAccounts() {
		return nil, nil, &FreeModelUnavailableError{Message: fmt.Sprintf("model %q is cooling on all accounts and no fallback model is available", model)}
	}
	return nil, nil, fmt.Errorf("no active accounts available. Use --login or admin API to add accounts")
}

// WithModel 返回带指定模型名的参数副本（避免污染调用方 map）。
func WithModel(params map[string]any, model string) map[string]any {
	cp := make(map[string]any, len(params)+1)
	for k, v := range params {
		cp[k] = v
	}
	cp["model"] = model
	return cp
}

// isFreeAliasModel 判断是否为 "free" 别名或链内免费模型（避免重复兜底）。
func isFreeAliasModel(model string) bool {
	return dispatch.IsFreeAlias(model, defaultFreeChain())
}

// defaultFreeChain 动态派生默认回退链（管理员未配置 modelChain 时）：
// 优先取 Cline 远程同步的免费模型（有效、不下架），再补内置常量链。
// 已下架的内置模型（如 longcat-2.0，不在远程同步列表）自动排除，不再写死；
// 从未同步成功（离线）时回退内置常量链。
func defaultFreeChain() []string {
	remote := RemoteModelsActive()
	p := accounts.LoadPool()
	known := make(map[string]bool, len(p.Models))
	var remoteFree []string
	for _, m := range p.Models {
		known[m.ID] = true
		if remote && m.Source == "remote" && m.Cost == "free" && m.Status == "active" {
			remoteFree = append(remoteFree, m.ID)
		}

		if m.Source == "provider" && m.Cost == "free" && m.Status == "active" && !m.Disabled {
			remoteFree = append(remoteFree, m.ID)
		}
	}
	chain := make([]string, 0, len(remoteFree)+len(FreeModelChain))
	seen := make(map[string]bool, len(remoteFree)+len(FreeModelChain))
	for _, m := range remoteFree {
		if !seen[m] {
			seen[m] = true
			chain = append(chain, m)
		}
	}
	for _, m := range FreeModelChain {
		if seen[m] || (remote && !known[m]) {
			continue
		}
		seen[m] = true
		chain = append(chain, m)
	}
	if len(chain) == 0 {
		return FreeModelChain
	}
	return chain
}

// LiveModelSet 返回当前仍然「在线」的模型 ID 集合：池内全部模型（远程同步
// remote / opencode zen 同步 / 管理员自定义，已手动停用的除外）+ 启用中自定义
// provider 声明的模型。
// 调用方须持有 accounts.PoolMu；providers 走独立的 providersMu，仓库内不存在反向持锁路径。
func LiveModelSet(p *domain.AccountPool) map[string]bool {
	live := make(map[string]bool, len(p.Models))
	disabled := make(map[string]bool)
	for _, m := range p.Models {
		if m.Disabled {
			disabled[m.ID] = true
			continue
		}
		live[m.ID] = true
	}

	for _, sp := range providers.List() {
		if !sp.Enabled {
			continue
		}
		for _, id := range sp.ModelIDs {
			if disabled[id] {
				continue
			}
			live[id] = true
		}
	}
	return live
}

// IsModelDisabled 判断模型是否被管理员在管理页手动停用（Disabled 标记）。
// 支持 "opencode/" 前缀别名（与 zen.RouteModel 的匹配方式一致）。
// 停用语义：不进客户端模型列表、直接请求在入口处拒绝、默认模型与回退链跳过。
func IsModelDisabled(id string) bool {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, "opencode/")
	if id == "" {
		return false
	}
	p := accounts.LoadPool()
	accounts.PoolMu.Lock()
	defer accounts.PoolMu.Unlock()
	for _, m := range p.Models {
		if m.ID == id && m.Disabled {
			return true
		}
	}
	return false
}

// filterStaleModels 从回退链里剔除已下架的模型。
//
// 上游免费/推荐模型每日调整，写死或旧配置里的模型会随下架而失效：继续尝试
// 只会白白多一次必败的上游往返（404/400 + 冷却），排在链头时每个请求都要先
// 撞一次失败。判定来源：远程同步成功的模型列表（含 zen/自定义）+ 启用中
// provider 的 modelIds。
//
// 远程同步从未成功（离线冷启动）时不判定、原样保留；剔空时也原样保留
// （宁可按原链试一次，也不要空链直接报错）。
func filterStaleModels(chain []string) []string {
	if len(chain) == 0 || !RemoteModelsActive() {
		return chain
	}
	p := accounts.LoadPool()
	accounts.PoolMu.Lock()
	live := LiveModelSet(p)
	accounts.PoolMu.Unlock()
	out, dropped := dispatch.FilterLive(chain, true, live)
	if len(dropped) == 0 {
		return out
	}
	log.Printf("  model chain: dropped delisted model(s) %v -> %v", dropped, out)
	return out
}

// hasAnyFallbackLeft 判断点名模型之后是否还有候选（决定 500 是否透传）。
func hasAnyFallbackLeft(requested, current string) bool {
	return dispatch.HasFallbackAfter(ModelFallbackChain(requested), current)
}

// ModelFallbackChain 显式模型的降级序列：点名模型优先，其后是管理员配置的
// 回退链（modelChain）；未配置时动态派生默认链（排除已下架模型）。均去重。
// 用户显式配置的链**不做列表过滤**：官方列表之外常有实测仍可用的模型
// （列表只是参考不是白名单），失效条目由请求时的模型级冷却与故障转移兜底；
// 只有动态派生的免费链才按 LiveModelSet 过滤下架条目。点名模型本身始终
// 保留 —— 它是客户端显式要求的（可能由 zen / provider 服务，不归本仓判定）。
func ModelFallbackChain(requested string) []string {
	configured := ConfigProvider().ModelChain
	if len(configured) == 0 {
		configured = filterStaleModels(defaultFreeChain())
	}
	return dispatch.BuildChain(requested, configured)
}

// hasActiveAccounts 池中是否存在 active 状态的账号。
func hasActiveAccounts() bool {
	p := accounts.LoadPool()
	accounts.PoolMu.Lock()
	defer accounts.PoolMu.Unlock()
	for _, a := range p.Accounts {
		if a.Status == "active" {
			return true
		}
	}
	return false
}

func CallFreeClineAPI(params map[string]any, stream bool) (*http.Response, *domain.Account, error) {

	configured := ConfigProvider().ModelChain
	chain := configured
	if len(configured) == 0 {
		chain = defaultFreeChain()
	}

	chain = filterStaleModels(chain)
	chain = accounts.SortModelsByAvailability(chain)
	if ConfigProvider().OnlyFree {
		eligible := make([]string, 0, len(chain))
		for _, m := range chain {
			if ModelFreeOnlyEligible(m) {
				eligible = append(eligible, m)
			} else {
				log.Printf("  onlyFree: skipping free-chain model %q (access class %s)", m, ModelRouteAccess(m).Class)
			}
		}
		chain = eligible
	}
	delete(params, ServedByParam)
	delete(params, ProviderNameParam)
	delete(params, providers.ProviderIDParam)
	var affinitySk string
	if ConfigProvider().SessionAffinity.Enabled {
		affinitySk = providers.SessionKeyFromParams(params)
	}
	var lastErr error
	lastWas429 := false
	for _, model := range chain {
		params["model"] = model

		if pResp, pErr, attempted, provName := providers.CallAPI(params, stream); attempted {
			if pErr == nil {
				params[ServedByParam] = zen.UpstreamProvider
				params[ProviderNameParam] = provName
				return pResp, nil, nil
			}

			if cause, stopped := egress.StopCause(pErr); stopped {
				return nil, nil, cause
			}
		}
		// "free" 链是纯降级路径：全部用「最久未用优先」挑账号，摊平用量。
		// 会话亲和开启时优先复用绑定的账号（free 别名请求正是上游 session
		// 连续性最有价值的路径）。
		var freeAffinity *domain.Account
		if affinitySk != "" {
			if bound, ok := accounts.PickAffinityAccount(affinitySk, model); ok {
				freeAffinity = bound
			}
		}
		for {
			acc := freeAffinity
			freeAffinity = nil
			if acc == nil {
				acc = accounts.PickAccountForModelLeastUsed(model)
			}
			if acc == nil {
				break
			}

			resp, usedAcc, err := CallClineAPIWithAccountCtx(context.Background(), acc, params, stream)
			accounts.ObserveAffinityOutcome(affinitySk, usedAcc, err)
			if err == nil {
				return resp, usedAcc, nil
			}
			var accountErr *ClineAccountUnavailableError
			if errors.As(err, &accountErr) {
				continue
			}

			if stop, cooldown, matched := egress.RuleDecision(err); matched {
				if cooldown && usedAcc != nil {
					SetModelCooldown(usedAcc, model, egress.RuleCooldownUntil(egress.ErrorStatus(err)))
				}
				if stop {
					egress.LogRuleHit("stop", egress.ErrorStatus(err), "free-chain model "+model)
					return nil, usedAcc, err
				}
				egress.LogRuleHit("continue", egress.ErrorStatus(err), "free-chain model "+model)
			}
			lastErr = err
			lastWas429 = false
			if apiErr, ok := err.(*egress.APIError); ok && (apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode == http.StatusPaymentRequired) {

				if apiErr.StatusCode == http.StatusPaymentRequired {
					SetModelCooldown(acc, model, time.Now().Add(30*time.Minute))
					log.Printf("  model %q on %s: insufficient credits (402), cooling for 30m, trying next account", model, acc.Email)
				}
				lastWas429 = true
				continue
			}

			if IsDeterministicRequestError(err) {
				return nil, usedAcc, err
			}

			break
		}
	}
	if lastErr != nil {
		if lastWas429 {

			return nil, nil, &FreeModelUnavailableError{Message: "no eligible accounts available for free models"}
		}
		return nil, nil, lastErr
	}
	return nil, nil, &FreeModelUnavailableError{Message: "no eligible accounts available for free models"}
}

func CallClineAPIWithAccount(acc *domain.Account, params map[string]any, stream bool) (*http.Response, *domain.Account, error) {
	return CallClineAPIWithAccountCtx(context.Background(), acc, params, stream)
}

func CallClineAPIWithAccountCtx(ctx context.Context, acc *domain.Account, params map[string]any, stream bool) (*http.Response, *domain.Account, error) {
	token, err := accounts.EnsureAccountToken(acc)
	if err != nil {

		return nil, acc, &ClineAccountUnavailableError{err: fmt.Errorf("account %s token failed: %w", acc.Email, err)}
	}

	body := BuildUpstreamBody(params, stream)
	sessionID, _ := body["session_id"].(string)

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, acc, fmt.Errorf("marshal body: %w", err)
	}

	client := &clineupstream.Client{
		HTTP:         &http.Client{Transport: egress.Client.Transport},
		BaseURL:      accounts.ClineAPIBase,
		ExtraHeaders: ConfigProvider().Headers,
	}

	toolCount := 0
	if tools, ok := params["tools"]; ok {
		if t, ok := tools.([]any); ok {
			toolCount = len(t)
		}
	}
	log.Printf("  upstream: account=%s stream=%v tools=%d msgs=%d max_tokens=%v effort=%v",
		TruncateEmail(acc.Email), stream, toolCount, getMsgCount(params), body["max_tokens"], body["reasoning_effort"])

	resp, err := client.Chat(ctx, token, sessionID, bodyJSON)
	if err != nil {
		acc.Status = "cooldown"
		acc.CooldownUntil = time.Now().Add(5 * time.Minute)
		accounts.SavePool()
		return nil, acc, &ClineAccountUnavailableError{err: fmt.Errorf("upstream request: %w", err)}
	}

	if resp.StatusCode == 401 {
		resp.Body.Close()

		if err := accounts.RefreshAccountToken(acc); err == nil {
			token = acc.AccessToken
			resp, err = client.Chat(ctx, token, sessionID, bodyJSON)
			if err != nil {
				acc.Status = "cooldown"
				acc.CooldownUntil = time.Now().Add(5 * time.Minute)
				accounts.SavePool()
				return nil, acc, &ClineAccountUnavailableError{err: fmt.Errorf("upstream retry: %w", err)}
			}
			if resp.StatusCode == 401 {
				resp.Body.Close()
				acc.Status = "expired"
				accounts.SavePool()
				return nil, acc, &ClineAccountUnavailableError{err: fmt.Errorf("account %s token expired permanently", acc.Email)}
			}
		} else {
			return nil, acc, &ClineAccountUnavailableError{err: fmt.Errorf("account %s refresh failed: %w", acc.Email, err)}
		}
	}

	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		bodyStr := string(bodyBytes)

		if resp.StatusCode == 429 {
			model, _ := body["model"].(string)
			until := parseCooldownUntil(bodyStr)
			if model != "" {
				SetModelCooldown(acc, model, until)
			} else {
				acc.Status = "cooldown"
				acc.CooldownUntil = until
				accounts.SavePool()
			}
		}

		if model, _ := body["model"].(string); model != "" && IsModelGoneError(resp.StatusCode, bodyStr) {
			MarkModelGone(model)
		}
		return nil, acc, egress.NewAPIError(resp.StatusCode, platform.Truncate(bodyStr, 500))
	}

	acc.LastUsed = time.Now()
	acc.UsageCount++
	accounts.SavePool()
	return resp, acc, nil
}

type accountTestResult struct {
	AccountID    string `json:"accountId"`
	Email        string `json:"email"`
	OK           bool   `json:"ok"`
	DurationMs   int64  `json:"durationMs"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
	Error        string `json:"error,omitempty"`
}

// parseCooldownUntil 从 429 响应体中解析 "Try again in 1h 1m" 格式的等待时长，
// 返回预计恢复时间；解析失败则回退到 1 小时后。
var cooldownRe = regexp.MustCompile(`(?i)try\s+again\s+in\s+(\d+)\s*h?(?:\s*(\d+))?\s*m?`)

func parseCooldownUntil(body string) time.Time {
	matches := cooldownRe.FindStringSubmatch(body)
	if len(matches) >= 2 {
		hours, _ := strconv.Atoi(matches[1])
		minutes := 0
		if len(matches) >= 3 && matches[2] != "" {
			minutes, _ = strconv.Atoi(matches[2])
		}
		if hours > 0 || minutes > 0 {
			return time.Now().Add(time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute)
		}
	}

	return time.Now().Add(1 * time.Hour)
}

// StartCooldownRecovery 启动后台 goroutine，每 30 秒检查一次 cooldown 账号，
// 对 CooldownUntil 已过期的账号执行探活，成功则自动激活。
func StartCooldownRecovery() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			p := accounts.LoadPool()
			accounts.PoolMu.Lock()
			var toRecover []*domain.Account
			for _, acc := range p.Accounts {
				if acc.Status != "cooldown" {
					continue
				}

				if acc.CooldownUntil.IsZero() || time.Now().After(acc.CooldownUntil) {
					toRecover = append(toRecover, acc)
				}
			}
			accounts.PoolMu.Unlock()

			for _, acc := range toRecover {
				log.Printf("cooldown recovery: testing %s", acc.Email)
				result := TestAccount(acc)
				if result.OK {
					log.Printf("cooldown recovery: %s reactivated", acc.Email)
				} else {
					log.Printf("cooldown recovery: %s still unavailable: %s", acc.Email, result.Error)
				}
			}
		}
	}()
}

// TestAccount sends a minimal "hi" request through a specific account to verify
// it can complete an upstream call. It does not update aggregate token counters
// or request logs; it is a diagnostic-only probe.
func TestAccount(acc *domain.Account) accountTestResult {
	result := accountTestResult{AccountID: acc.AccountID, Email: acc.Email}
	started := time.Now()

	params := map[string]any{
		"model":      DefaultModel(),
		"max_tokens": 1024,
		"stream":     false,
		"messages": []any{
			map[string]any{"role": "user", "content": "hi"},
		},
	}

	resp, _, err := CallClineAPIWithAccount(acc, params, false)
	if err != nil {
		result.DurationMs = time.Since(started).Milliseconds()
		result.Error = platform.Truncate(err.Error(), 200)
		return result
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		result.DurationMs = time.Since(started).Milliseconds()
		result.Error = "read response: " + platform.Truncate(err.Error(), 200)
		return result
	}

	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		result.DurationMs = time.Since(started).Milliseconds()
		result.Error = "decode response: " + platform.Truncate(err.Error(), 200)
		return result
	}
	if data, ok := obj["data"]; ok {
		if d, ok := data.(map[string]any); ok {
			obj = d
		}
	}
	obj = openaichat.NormalizeResponse(obj)
	usage := openaichat.ParseUsage(obj["usage"])

	result.OK = true
	result.DurationMs = time.Since(started).Milliseconds()
	if usage.Valid {
		result.InputTokens = usage.Prompt
		result.OutputTokens = usage.Completion
	}

	if acc.Status != "active" {
		accounts.PoolMu.Lock()
		acc.Status = "active"
		accounts.PoolMu.Unlock()
		accounts.SavePool()
	}
	return result
}

type TokenUsage = openaichat.Usage

func RecordTokenUsage(acc *domain.Account, model string, usage TokenUsage) {
	if acc == nil || !usage.Valid {
		return
	}

	isFree := model != "" && IsFreeModelID(model)
	accounts.PoolMu.Lock()
	acc.PromptTokens += usage.Prompt
	acc.CompletionTokens += usage.Completion
	acc.TotalTokens += usage.Total
	acc.CachedTokens += usage.Cached

	if isFree {
		if acc.ModelStats == nil {
			acc.ModelStats = make(map[string]*domain.ModelStat)
		}
		st := acc.ModelStats[model]
		if st == nil {
			st = &domain.ModelStat{ModelID: model, Cost: "free"}
			acc.ModelStats[model] = st
		}
		st.UsageCount++
		st.PromptTokens += usage.Prompt
		st.CompletionTokens += usage.Completion
		st.TotalTokens += usage.Total
		st.CachedTokens += usage.Cached
	}
	accounts.PoolMu.Unlock()
	accounts.SavePool()
}

// IsFreeModelID 判断模型是否为 free 计费（用于按模型统计和模型级冷却）。
func IsFreeModelID(model string) bool {
	for _, m := range AllModels() {
		if m.ID == model {
			return m.Cost == "free"
		}
	}

	return strings.HasSuffix(model, ":free") || strings.Contains(model, "/free/")
}

// ModelCooldownActive 判断某账号下该模型是否处于模型级冷却中。
func ModelCooldownActive(acc *domain.Account, model string) bool {
	if acc == nil || model == "" {
		return false
	}
	accounts.PoolMu.Lock()
	defer accounts.PoolMu.Unlock()
	until, ok := acc.ModelCooldowns[model]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(acc.ModelCooldowns, model)
		accounts.SavePool()
		return false
	}
	return true
}

// HasAlternativeModel 判断降级链上是否还有未冷却的候选模型。
// 用于避免「所有模型都被冷却」导致整条链不可用。
func HasAlternativeModel(acc *domain.Account, current string) bool {
	for _, m := range ModelFallbackChain(current) {
		if m == "" || m == current {
			continue
		}
		if !ModelCooldownActive(acc, m) {
			return true
		}
	}
	return false
}

// SetModelCooldown 记录模型级冷却（429 或首 token 前失败时调用）：只暂停该模型，账号保持可用。
// fallback 为解析失败时的恢复时长（默认 1 小时）。
func SetModelCooldown(acc *domain.Account, model string, until time.Time) {
	if acc == nil || model == "" {
		return
	}
	accounts.PoolMu.Lock()
	if acc.ModelCooldowns == nil {
		acc.ModelCooldowns = make(map[string]time.Time)
	}
	acc.ModelCooldowns[model] = until
	accounts.PoolMu.Unlock()
	accounts.SavePool()
	log.Printf("model cooldown: account=%s model=%s until=%s", TruncateEmail(acc.Email), model, until.Format("15:04:05"))
}

func TruncateEmail(email string) string {
	if len(email) <= 12 {
		return email
	}
	parts := splitEmail(email)
	if len(parts) == 2 && len(parts[0]) > 3 {
		return parts[0][:3] + "***@" + parts[1]
	}
	if len(email) > 12 {
		return email[:8] + "..."
	}
	return email
}

func splitEmail(email string) []string {
	for i := 0; i < len(email); i++ {
		if email[i] == '@' {
			return []string{email[:i], email[i+1:]}
		}
	}
	return []string{email}
}

func getMsgCount(params map[string]any) int {
	if msgs, ok := params["messages"].([]any); ok {
		return len(msgs)
	}
	return 0
}

// ZenFailoverToCline zen 调用失败后的透明降级：改走 cline 账号池 free 模型链。
// attempted=false 表示未启用故障转移，调用方维持原错误路径。
// 注意：失败计数由 zen.Call 内部标记（每请求恰好一次），此处不再重复计数，
// 否则限流/服务错误路径 + 此处各计一次，故障转移会被过早触发。
func ZenFailoverToCline(params map[string]any, stream bool) (*http.Response, *domain.Account, error, bool) {
	cfg := zen.GetConfig()
	if !cfg.Failover {
		return nil, nil, nil, false
	}
	orig, _ := params["model"].(string)
	log.Printf("  zen failover: %q unavailable upstream, falling back to cline free pool", orig)
	params["model"] = "free"
	resp, acc, err := CallFreeClineAPI(params, stream)
	return resp, acc, err, true
}

// ClineFailoverToZen Cline 侧（含 free 池链）全部耗尽后的反向故障转移：
// 落到 opencode zen 免费模型。与 ZenFailoverToCline 方向相反，形成双向闭环——
// 任一免费上游挂掉，流量自动落到另一条。zen 未启用或处于故障转移窗口
// （连续失败被判定不可达）时不尝试；最多试 3 个免费模型，避免在坏模型上反复烧时间。
func ClineFailoverToZen(params map[string]any, stream bool) (*http.Response, bool) {
	cfg := zen.GetConfig()
	if !cfg.Enabled || zen.FailedNow() {
		return nil, false
	}
	orig, _ := params["model"].(string)
	attempts := 0
	for _, m := range zen.CurrentModels() {
		if !zen.IsFreeModel(m) || m.ID == orig {
			continue
		}
		attempts++
		if attempts > egress.FailoverModelCap() {
			break
		}
		log.Printf("  cline failover: %q exhausted, trying zen free model %q", orig, m.ID)
		params["model"] = m.ID
		resp, err := zen.Call(params, stream)
		if err == nil {
			return resp, true
		}
		log.Printf("  cline failover: zen model %q failed: %v", m.ID, err)
	}
	return nil, false
}
