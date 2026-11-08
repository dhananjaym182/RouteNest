package providers

import (
	"log"
	"net/http"
	"strings"

	"routenest/internal/egress"
)

// ============================================================================
// Fallback 集成：自定义 provider 参与 modelFallbackChain
// ============================================================================

// CallAPI 先看粘贴会话是否已把该会话钉在本模型某个健康
// provider 上（钉住的不换模型、不随机切换）；未钉住时按冗余组逐家尝试，
// 任一成功即把该会话钉到它身上。全部失败返回最后一个 error，调用方决定
// 是否降级到回退链。模型没有可用 provider 时 attempted=false。
// 第 4 个返回值是实际服务本次尝试的 provider 显示名（成功=命中那家；
// attempted 失败=最后尝试那家；attempted=false 为空），供请求日志归因。
func CallAPI(params map[string]any, stream bool) (*http.Response, error, bool, string) {
	model, _ := params["model"].(string)
	// P3 粘贴会话：同一会话（首个 user 消息哈希）在 TTL 内钉到同一家 provider，
	// 中途不因组内竞争或优先级重算而换源；钉住的 provider 冷却/超限/停用后
	// 立即解钉融入组内故障转移。
	sk := SessionKeyFromParams(params)
	if sk != "" {
		if pin := stickyGet(sk); pin != nil && pin.model == model {
			if p := pinProviderPresent(pin.providerID, model); p != nil {
				resp, err := Call(p, params, stream)
				if err == nil {
					stickySet(sk, pin.providerID, model)
					params[ProviderIDParam] = pin.providerID
					return resp, nil, true, p.Name
				}
				log.Printf("  provider attempt failed name=%s model=%q (pinned): %v", p.Name, model, err)
				if stop, cooldown, matched := egress.RuleDecision(err); matched {
					if cooldown {
						SetCooldown(p.ID, model, egress.RuleCooldownUntil(egress.ErrorStatus(err)))
					}
					if stop {
						stickyClear(sk)
						params[ProviderIDParam] = pin.providerID
						egress.LogRuleHit("stop", egress.ErrorStatus(err), "provider "+p.Name+" (sticky)")
						return nil, egress.WrapStop(err), true, p.Name
					}
				}
			}
			stickyClear(sk) // 解钉：失败/不满足条件时融入正常组内转移
		}
	}
	provs := ResolveForModel(model)
	if len(provs) == 0 {
		return nil, nil, false, ""
	}
	var lastErr error
	var lastName string
	var lastID string
	for _, p := range provs {
		resp, err := Call(p, params, stream)
		if err == nil {
			if sk != "" {
				stickySet(sk, p.ID, model)
			}
			params[ProviderIDParam] = p.ID
			return resp, nil, true, p.Name
		}
		log.Printf("  provider attempt failed name=%s model=%q: %v", p.Name, model, err)
		lastErr = err
		lastName = p.Name
		lastID = p.ID
		if stop, cooldown, matched := egress.RuleDecision(err); matched {
			if cooldown {
				SetCooldown(p.ID, model, egress.RuleCooldownUntil(egress.ErrorStatus(err)))
			}
			if stop {
				params[ProviderIDParam] = p.ID
				egress.LogRuleHit("stop", egress.ErrorStatus(err), "provider "+p.Name)
				return nil, egress.WrapStop(err), true, p.Name
			}
			egress.LogRuleHit("continue", egress.ErrorStatus(err), "provider "+p.Name)
		}
	}
	params[ProviderIDParam] = lastID
	return nil, lastErr, true, lastName
}

// CallAPINamed accepts an exact provider ID or a legacy name-group pin.
// An ID-pinned failure never falls through to another provider instance.
// 故意不读取/写入粘性会话——显式固定应当压过自动粘贴，且固定到 A 家的调用不应
// 污染另一家的粘贴状态。无匹配候选时 attempted=false（调用方决定是否兜底）。
// 供请求日志归因。
func CallAPINamed(params map[string]any, stream bool, name string) (*http.Response, error, bool, string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil, false, ""
	}
	model, _ := params["model"].(string)
	provs := ResolveNamedForModel(model, name)
	if len(provs) == 0 {
		return nil, nil, false, ""
	}
	var lastErr error
	var lastName string
	var lastID string
	for _, p := range provs {
		resp, err := Call(p, params, stream)
		if err == nil {
			params[ProviderIDParam] = p.ID
			return resp, nil, true, p.Name
		}
		log.Printf("  provider attempt failed name=%s model=%q (pinned): %v", p.Name, model, err)
		lastErr = err
		lastName = p.Name
		lastID = p.ID
		// A pinned-provider failure is already fatal to the caller; rules can
		// only add the cooldown side effect here, not change the flow.
		if _, cooldown, matched := egress.RuleDecision(err); matched && cooldown {
			SetCooldown(p.ID, model, egress.RuleCooldownUntil(egress.ErrorStatus(err)))
			egress.LogRuleHit("cooldown", egress.ErrorStatus(err), "provider "+p.Name+" (pinned)")
		}
	}
	params[ProviderIDParam] = lastID
	return nil, lastErr, true, lastName
}
