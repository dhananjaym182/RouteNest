package combos

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"routenest/internal/accounts"
	"routenest/internal/compaction"
	"routenest/internal/domain"
	"routenest/internal/egress"
	"routenest/internal/models"
	"routenest/internal/platform"
	"routenest/internal/providers"
	"routenest/internal/routing"
	"routenest/internal/zen"
	"strings"
	"time"
)

const (
	servedByParam     = "__served_by"
	providerNameParam = "__served_by_name"
)

// ============================================================================
// 组合运行：有序回退
// ============================================================================

// Run 是一次组合派发的完整结果。
type Run struct {
	Resp     *http.Response
	Acc      *domain.Account
	Upstream string
	Model    string // 实际服务的模型
	Trace    []domain.AttemptLog
	Err      error
}

// Retryable 判断某次条目尝试的失败是否可重试（应继续尝试下一个条目）。
//
// 可重试：限流(429)/配额(402)/网关重定向(421)/瞬态 5xx/超时/传输错误/模型不存在(404)。
// 不可重试：客户端/配置类错误 400/401/403 —— 重试下一个模型没有意义，直接失败。
func Retryable(err error) bool {
	if err == nil {
		return false
	}
	if IsAccountUnavailable(err) {
		return true // 账号不可用：可能换号/换条目即可恢复
	}
	var apiErr *egress.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 400, 401, 403:
			return false
		case 402, 408, 409, 421, 425, 429:
			return true
		case 404:
			return true
		default:
			return apiErr.StatusCode >= 500
		}
	}
	// 其余（zen 网络/上游错误、未知错误）按瞬态处理，交给下一个条目。
	return true
}

// AttemptStatus 从错误里尽量提取上游 HTTP 状态码（供尝试轨迹展示）。
func AttemptStatus(err error) int {
	var apiErr *egress.APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode
	}
	return 0
}

// RouteKind 判定组合条目的派发路径：zen / reject（付费 zen）/ cline，以及
// zen-degraded（zen 处于故障转移窗口，条目运行时不可用应跳过）。
//
// 与 zen.RouteModel 的唯一区别：zen.RouteModel 在 zen 故障转移窗口会把 zen 免费模型降级
// 为 "cline"，而组合**不能**沿用该降级 —— 组合没有全局 free 链可改写，若把 zen
// 模型 ID 原样发给 Cline 会得到 400 "invalid model format"，进而被误判为永久失败
// 并中止整条组合。故这里显式区分「同名 cline 冲突」（走 cline，与 zen.RouteModel 一致）
// 与「zen 降级」（走 zen-degraded → 跳过条目）。
func RouteKind(model string) string {
	switch zen.RouteModel(model) {
	case "reject":
		return "reject"
	case "zen":
		return "zen"
	}
	// zen.RouteModel == "cline"
	zm, ok := zen.ResolveInfo(model)
	if !ok || !zen.IsFreeModel(zm) {
		return "cline" // 普通 cline 模型
	}
	if ClineModelWithIDExists(model) {
		return "cline" // 同名 cline 冲突：cline 优先（与 zen.RouteModel 一致）
	}
	return "zen-degraded"
}

// ClineModelWithIDExists 判断池内是否存在与给定模型同名的非 zen 模型
// （自定义冲突场景，zen.RouteModel 会把同名 zen 模型让给 cline）。
func ClineModelWithIDExists(model string) bool {
	id := strings.TrimPrefix(strings.TrimSpace(model), "opencode/")
	_, ok := models.Find(func(pm domain.Model) bool { return !zen.IsSource(pm) && pm.ID == id })
	return ok
}

// ClinePoolModelExists 判断模型是否真能由 Cline 账号池服务：排除 zen 与自定义
// provider 来源。source=cline 固定必须用它（严格）——provider 同步进池的条目
// 走 Cline 会得到 400 "invalid model format"，进而被组合误判为永久失败。
func ClinePoolModelExists(model string) bool {
	id := strings.TrimPrefix(strings.TrimSpace(model), "opencode/")
	_, ok := models.Find(func(pm domain.Model) bool {
		return !zen.IsSource(pm) && pm.Source != "provider" && pm.ID == id
	})
	return ok
}

// EntryRouteKind 在 RouteKind 之上叠加条目的上游固定（Source/Provider）。
//
//   - Source == "" → RouteKind(Model)（默认自动派发，行为不变）。
//   - "cline"      → 模型真能由 Cline 账号池服务时 "cline"（跳过自定义 provider），
//     否则 "unavailable"（跳过，避免 provider-only 模型被发给 Cline 触发 400 中止）。
//   - "provider"   → "cline"（provider 名称在 cline 分支里强制指定）。
//   - "zen"        → 显式固定压过 AUTO 的同名 cline 冲突降级：可解析且为免费 zen
//     模型时返回 "zen"，zen→cline 故障转移窗口内返回 "zen-degraded"，否则
//     "unavailable"（跳过）。
//   - 其他（未知，通常已被 ValidateCombo 拦截）→ 回退到自动派发，避免运行时误伤。
func EntryRouteKind(e domain.ComboEntry) string {
	base := RouteKind(e.Model)
	switch strings.TrimSpace(e.Source) {
	case "":
		return base
	case "cline":
		// 严格判定：模型必须真能由 Cline 账号池服务。provider-only 模型（含同步
		// 进池的 source=provider 行）走 Cline 会 400，故按运行时不可用跳过该条目，
		// 而不是把 provider 的模型 ID 原样发给 Cline 触发整条组合中止。
		if !ClinePoolModelExists(e.Model) {
			return "unavailable"
		}
		return "cline"
	case "provider":
		return "cline"
	case "zen":
		// 显式 zen 固定压过 AUTO 的「同名 cline 冲突」降级：这正是用 zen 固定来
		// 消歧的用途。只按 zen 自身状态判定，与 validateComboEntryPin 保持一致。
		zm, ok := zen.ResolveInfo(e.Model)
		if !ok || !zen.IsFreeModel(zm) {
			return "unavailable"
		}
		if cfg := zen.GetConfig(); cfg.Failover && zen.FailedNow() {
			return "zen-degraded"
		}
		return "zen"
	default:
		return base
	}
}

// CallSingleModel 通过既有 provider/账号路径调用**单个**模型，不触发全局 ModelChain
// 或 free 链：这是组合回退的安全基础。
//
//   - zen 免费模型 → zen.Call（含匿名免费层处理）
//   - 自定义 provider → providers.CallAPI（冗余组 + 粘性会话）；条目固定到
//     某一 provider 时改走 providers.CallAPINamed（只看该家，不回退账号池）
//   - 其余 → Cline 账号池（模型级冷却、402/429 换号，账号轮询）
//
// 返回：resp / 使用的账号（若有）/ 上游归因 / 实际服务的自定义 provider 显示名
// （upstream=="provider" 时非空，cline/opencode 为空）/ error。
func CallSingleModel(params map[string]any, entry domain.ComboEntry, stream bool, sessionID string) (*http.Response, *domain.Account, string, string, error) {
	model := strings.TrimSpace(entry.Model)
	ep := WithModel(params, model)
	delete(ep, servedByParam)
	delete(ep, providerNameParam)
	delete(ep, providers.ProviderIDParam)

	switch EntryRouteKind(entry) {
	case "reject":
		return nil, nil, zen.UpstreamOpenCode, "", fmt.Errorf("model %q is a paid opencode model; only free models are proxied", model)
	case "zen-degraded":
		// zen 处于故障转移窗口：绝不把 zen 免费模型 ID 原样发给 Cline（会被判
		// 400 "invalid model format"，进而被组合误判为永久失败）。按运行时不可用
		// 跳过该条目 —— 返回可重试错误，组合继续尝试下一个条目。
		return nil, nil, zen.UpstreamOpenCode, "", NewUnavailableError(
			fmt.Sprintf("model %q temporarily unavailable (opencode zen degraded)", model),
		)
	case "unavailable":
		// 条目固定的上游当前无法服务该模型（zen 固定到非免费/非 zen 模型，或
		// cline 固定到 provider-only 模型等）：按运行时不可用跳过，返回可重试错误，
		// 让组合继续尝试下一个条目而不是中止。
		return nil, nil, zen.UpstreamOpenCode, "", NewUnavailableError(
			fmt.Sprintf("model %q is not available on the pinned source", model),
		)
	case "zen":
		if zm, ok := zen.ResolveInfo(model); ok {
			if out := compaction.MaybeCompact(ep, zm, sessionID); out.Changed {
				log.Printf("  combo %s", out.Note)
			}
		}
		resp, err := zen.Call(ep, stream)
		// 上游返回「上下文超限」400：压缩后重试一次
		if err != nil && routing.IsContextLengthError(err) {
			if zm, ok := zen.ResolveInfo(model); ok {
				log.Printf("  combo: context length exceeded, compacting and retrying")
				if out := compaction.FallbackTruncate(ep, zm); out.Changed {
					log.Printf("  combo %s", out.Note)
					resp, err = zen.Call(ep, stream)
				}
			}
		}
		if err != nil {
			return nil, nil, zen.UpstreamOpenCode, "", err
		}
		return resp, nil, zen.UpstreamOpenCode, "", nil
	}

	// cline 侧：默认先自定义 provider（该模型若接入 provider），失败后走账号池。
	switch strings.TrimSpace(entry.Source) {
	case "provider":
		// 显式固定到某一家 provider：只看它，失败也**不**回退到 Cline 账号池
		// （用户明确指定了唯一上游）。
		resp, perr, attempted, provName := providers.CallAPINamed(ep, stream, entry.Provider)
		if !attempted {
			return nil, nil, zen.UpstreamProvider, "", NewUnavailableError(
				fmt.Sprintf("model %q is not served by provider %q", model, strings.TrimSpace(entry.Provider)),
			)
		}
		if perr != nil {
			// 固定 provider 失败：包装成可重试的「跳过」，避免 400/401/403（如
			// 一把被轮换的 API key）被 Retryable 判为永久错误而中止整条组合。
			return nil, nil, zen.UpstreamProvider, provName, NewUnavailableError(
				fmt.Sprintf("pinned provider %q failed for %q: %s", strings.TrimSpace(entry.Provider), model, platform.Truncate(perr.Error(), 200)),
			)
		}
		return resp, nil, zen.UpstreamProvider, provName, nil
	case "cline":
		// 显式固定到 Cline 账号池：跳过自定义 provider 尝试，直接走账号池。
	default:
		if resp, perr, attempted, provName := providers.CallAPI(ep, stream); attempted {
			if perr == nil {
				return resp, nil, zen.UpstreamProvider, provName, nil
			}
			// provider 已逐个记录失败原因；继续用账号池兜底同一模型。
		}
	}

	tried := map[string]bool{}
	for {
		acc := accounts.PickAccountForModelStrict(model)
		if acc == nil {
			return nil, nil, zen.UpstreamCline, "", NewUnavailableError(fmt.Sprintf("model %q has no available account", model))
		}
		if tried[acc.AccountID] {
			// 防呆：账号轮询下也不应无限重试同一个账号（fill 策略等边界）。
			return nil, acc, zen.UpstreamCline, "", NewUnavailableError(fmt.Sprintf("model %q unavailable on all eligible accounts", model))
		}
		tried[acc.AccountID] = true

		resp, usedAcc, err := CallClineAPIWithAccount(context.Background(), acc, ep, stream)
		if err == nil {
			return resp, usedAcc, zen.UpstreamCline, "", nil
		}
		if IsAccountUnavailable(err) {
			continue // token 失效等：换下一个账号
		}
		// 确定性 payload 400（invalid-request 语义，如工具可选字段 null 被网关硬拒）：
		// 换账号不可能自愈，立即上抛 —— 不在账号池上空转注定失败的上游往返。
		// （上下文超限 400 除外：RunCombo 层有压缩+重试路径。）
		if IsDeterministicRequestError(err) {
			return nil, usedAcc, zen.UpstreamCline, "", err
		}
		if apiErr, ok := err.(*egress.APIError); ok {
			switch apiErr.StatusCode {
			case 402:
				// 余额不足是账号级问题：冷却该账号该模型后换号，仍试同一模型。
				SetModelCooldown(usedAcc, model, time.Now().Add(30*time.Minute))
				log.Printf("  combo: model %q on %s: insufficient credits (402), cooling 30m, next account", model, TruncateEmail(usedAcc.Email))
				continue
			case 429:
				continue // 模型级冷却已记录，换号
			}
		}
		return nil, usedAcc, zen.UpstreamCline, "", err
	}
}

// CompactionModel 取候选条目中最小的有效上下文窗口作为组合压缩预算：
// 组合里最窄的模型决定上限，压缩后的历史对所有条目都安全。未知模型落到
// EffectiveCompactContext 的默认封顶。
func CompactionModel(combo domain.ModelCombo) domain.Model {
	var chosen domain.Model
	best := 0
	for _, e := range combo.Entries {
		m := strings.TrimSpace(e.Model)
		if m == "" || e.Disabled {
			continue
		}
		zm := compaction.ModelForCompaction(m)
		ctx := compaction.EffectiveCompactContext(zm)
		if best == 0 || ctx < best {
			best = ctx
			chosen = zm
		}
	}
	if best == 0 {
		return domain.Model{Context: compaction.DefaultCompactContextCeiling}
	}
	chosen.Context = best
	return chosen
}

// RunCombo 按配置顺序执行组合，返回首个成功的上游响应；失败/跳过的条目记入轨迹。
func RunCombo(params map[string]any, combo domain.ModelCombo, stream bool, sessionID string) Run {
	trace := make([]domain.AttemptLog, 0, len(combo.Entries))
	tried := make(map[string]bool, len(combo.Entries))
	failed := 0
	var lastErr error
	// 组合级主动压缩：以候选模型中最小的有效上下文为预算，一次压缩写回共享
	// params（CallSingleModel 压缩的是 map 副本，条目之间不会传递）。
	if out := compaction.MaybeCompact(params, CompactionModel(combo), sessionID); out.Changed {
		log.Printf("  combo %q %s", combo.ID, out.Note)
	}

	// 上下文超限的「截断重试」整条组合只做一次：截断后的 params 在后续条目间共享。
	truncatedForRun := false

	for i, entry := range combo.Entries {
		m := strings.TrimSpace(entry.Model)
		if m == "" {
			continue
		}
		order := i + 1
		if entry.Disabled {
			trace = append(trace, domain.AttemptLog{Model: m, Order: order, Outcome: "skipped", Reason: "entry disabled by admin",
				Source: strings.TrimSpace(entry.Source), Provider: strings.TrimSpace(entry.Provider)})
			continue
		}
		key := EntryKey(entry)
		if tried[key] {
			trace = append(trace, domain.AttemptLog{Model: m, Order: order, Outcome: "skipped", Reason: "duplicate entry",
				Source: strings.TrimSpace(entry.Source), Provider: strings.TrimSpace(entry.Provider)})
			continue
		}
		if routing.ModelDisabled(m) {
			trace = append(trace, domain.AttemptLog{Model: m, Order: order, Outcome: "skipped", Reason: "model disabled by admin",
				Source: strings.TrimSpace(entry.Source), Provider: strings.TrimSpace(entry.Provider)})
			continue
		}
		if GetConfig().OnlyFree {
			bare, _ := providers.SplitPin(m)
			if !routing.ModelFreeOnlyEligible(bare) {
				trace = append(trace, domain.AttemptLog{Model: m, Order: order, Outcome: "skipped",
					Reason: fmt.Sprintf("not eligible under onlyFree (access class: %s)", routing.ModelRouteAccess(bare).Class),
					Source: strings.TrimSpace(entry.Source), Provider: strings.TrimSpace(entry.Provider)})
				continue
			}
		}
		tried[key] = true

		start := time.Now()
		resp, acc, upstream, provName, err := CallSingleModel(params, entry, stream, sessionID)
		elapsed := time.Since(start).Milliseconds()
		// 上下文超限（400 + context-length error）：整条组合还没截断过时截断一次并
		// 重试**当前**条目。模型不是 zen 时用零值 Model 兜底 —— FallbackTruncate /
		// MaybeCompact 的零值默认（200k+封顶）仍会给出合理预算。
		if err != nil && routing.IsContextLengthError(err) && !truncatedForRun {
			truncatedForRun = true
			log.Printf("  combo %q: entry %d %q context length exceeded, truncating and retrying", combo.ID, order, m)
			if out := compaction.FallbackTruncate(params, compaction.ModelForCompaction(m)); out.Changed {
				log.Printf("  combo %s", out.Note)
				start = time.Now()
				resp, acc, upstream, provName, err = CallSingleModel(params, entry, stream, sessionID)
				elapsed = time.Since(start).Milliseconds()
			}
			// 截断后（或截断未生效）仍报上下文超限：按可重试错误继续下一个条目，
			// 不让 400 经 Retryable 判为永久错误而中止整条组合。
		}
		if err == nil {
			params["model"] = m
			if upstream == zen.UpstreamProvider {
				params[servedByParam] = zen.UpstreamProvider
				params[providerNameParam] = provName
			} else {
				delete(params, servedByParam)
				delete(params, providerNameParam)
				delete(params, providers.ProviderIDParam)
			}
			trace = append(trace, domain.AttemptLog{
				Model: m, Order: order, Upstream: upstream, Outcome: "success", DurationMs: elapsed,
				Source: strings.TrimSpace(entry.Source), Provider: strings.TrimSpace(entry.Provider),
				ProviderName: provName,
			})
			return Run{Resp: resp, Acc: acc, Upstream: upstream, Model: m, Trace: trace}
		}

		failed++
		lastErr = err
		trace = append(trace, domain.AttemptLog{
			Model: m, Order: order, Upstream: upstream, Status: AttemptStatus(err),
			Outcome: "failed", Reason: platform.Truncate(err.Error(), 200), DurationMs: elapsed,
			Source: strings.TrimSpace(entry.Source), Provider: strings.TrimSpace(entry.Provider),
			ProviderName: provName,
		})
		log.Printf("  combo %q: entry %d %q failed (%v)", combo.ID, order, m, err)

		if routing.IsContextLengthError(err) {
			// 上下文超限：即便 HTTP 400（Retryable 判为永久）也继续换下一个条目。
			continue
		}
		if !Retryable(err) {
			// 永久性错误（400/401/403 等）：继续换模型没有意义，直接失败。
			return Run{Trace: trace, Err: err}
		}
	}

	// 默认：组合只走自己的序列，不触发全局回退。
	if combo.EmergencyFallback {
		log.Printf("  combo %q: all entries failed, emergency fallback (opt-in) to free chain", combo.ID)
		fp := WithModel(params, "free")
		if resp, acc, err := CallFreeClineAPI(fp, stream); err == nil {
			fm, _ := fp["model"].(string)
			if fm == "" {
				fm = "free"
			}
			params["model"] = fm
			if v, ok := fp[servedByParam].(string); ok {
				params[servedByParam] = v
			} else {
				delete(params, servedByParam)
			}
			if v, ok := fp[providerNameParam].(string); ok {
				params[providerNameParam] = v // free 链经 provider 服务时同步归因名
				params[providers.ProviderIDParam] = fp[providers.ProviderIDParam]
			} else {
				delete(params, providerNameParam)
				delete(params, providers.ProviderIDParam)
			}
			trace = append(trace, domain.AttemptLog{
				Model: fm, Order: -1, Upstream: zen.UpstreamCline, Outcome: "success",
				Reason: "emergency fallback outside combo (opt-in)",
			})
			return Run{Resp: resp, Acc: acc, Upstream: zen.UpstreamCline, Model: fm, Trace: trace}
		} else {
			lastErr = err
			trace = append(trace, domain.AttemptLog{
				Model: "free", Order: -1, Upstream: zen.UpstreamCline, Outcome: "failed",
				Reason: platform.Truncate(err.Error(), 200), Status: AttemptStatus(err),
			})
		}
	}

	if lastErr == nil {
		lastErr = NewUnavailableError(fmt.Sprintf("combo %q has no eligible entries", combo.ID))
	}
	return Run{Trace: trace, Err: lastErr}
}
