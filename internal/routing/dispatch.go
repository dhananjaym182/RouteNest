package routing

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/domain"
	"routenest/internal/egress"
	"routenest/internal/providers"
	"routenest/internal/zen"
)

func CallClineAPI(params map[string]any, stream bool) (*http.Response, *domain.Account, error) {
	model, _ := params["model"].(string)
	delete(params, ServedByParam)
	delete(params, ProviderNameParam)
	delete(params, providers.ProviderIDParam)

	bare, pin, refErr := ResolveModelReference(model)
	if refErr != nil {
		if legacyBare, legacyPin := providers.SplitPin(model); legacyPin != "" && liveConfig().OnlyFree && !ModelFreeOnlyEligible(legacyBare) {
			return nil, nil, fmt.Errorf("model %q is not eligible while onlyFree is enabled (%s)", model, FreeOnlyRefusalDetail(legacyBare))
		}
		return nil, nil, refErr
	}
	if pin.active() {
		if liveConfig().OnlyFree && !ModelFreeOnlyEligible(model) {
			return nil, nil, fmt.Errorf("model %q is not eligible while onlyFree is enabled (%s)", model, FreeOnlyRefusalDetail(model))
		}
		params["model"] = bare
		params[providers.ProviderIDParam] = string(pin.ProviderID)
		switch pin.ProviderID {
		case "cline":
			return callPinnedClineModel(params, stream, bare)
		case "opencode":
			if !zen.GetConfig().Enabled {
				return nil, nil, &FreeModelUnavailableError{Message: "pinned OpenCode provider is disabled"}
			}
			zm, ok := zen.ResolveInfo(bare)
			if !ok || !zen.IsFreeModel(zm) {
				return nil, nil, &FreeModelUnavailableError{Message: fmt.Sprintf("model %q is not an available free OpenCode model", bare)}
			}
			resp, err := zen.Call(params, stream)
			return resp, nil, err
		}
		params[ServedByParam] = zen.UpstreamProvider
		if provider := providers.Find(string(pin.ProviderID)); provider != nil {
			params[ProviderNameParam] = provider.Name
		}
		providerPin := string(pin.ProviderID)
		if providerPin == "" {
			providerPin = pin.ProviderName
		}
		resp, perr, attempted, provName := providers.CallAPINamed(params, stream, providerPin)
		switch {
		case attempted && perr == nil:
			params[ServedByParam] = zen.UpstreamProvider
			params[ProviderNameParam] = provName
			return resp, nil, nil
		case !attempted:
			return nil, nil, &FreeModelUnavailableError{Message: providers.PinUnavailableReason(bare, providerPin)}
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
	if liveConfig().OnlyFree {
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
	if liveConfig().SessionAffinity.Enabled {
		affinitySk = providers.SessionKeyFromParams(params)
	}

	rounds, credCap, roundInterval := egress.RetryRoundSettings()
	for round := 0; round < rounds; round++ {
		if round > 0 && roundInterval > 0 {
			log.Printf("  retry round %d/%d for %q after %s cooldown window", round+1, rounds, model, roundInterval)
			time.Sleep(roundInterval)
		}
		for _, m := range chain {
			_, chainPin, refErr := ResolveModelReference(m)
			if refErr != nil {
				continue
			}
			if chainPin.active() {
				ep := WithModel(params, m)
				resp, acc, err := CallClineAPI(ep, stream)
				if err == nil {
					copyServingParams(params, ep)
					return resp, acc, nil
				}
				continue
			}

			ep := WithModel(params, m)
			if pResp, pErr, attempted, provName := providers.CallAPI(ep, stream); attempted {
				if pErr == nil {
					if m != model {
						log.Printf("  model fallback: %q unavailable, serving via provider %q", model, m)
					}
					params["model"] = m
					params[ServedByParam] = zen.UpstreamProvider
					params[ProviderNameParam] = provName
					params[providers.ProviderIDParam] = ep[providers.ProviderIDParam]
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
				params[providers.ProviderIDParam] = fp[providers.ProviderIDParam]
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
	return nil, nil, &FreeModelUnavailableError{Message: "no active accounts available. Use --login or admin API to add accounts"}
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

	configured := liveConfig().ModelChain
	chain := configured
	if len(configured) == 0 {
		chain = defaultFreeChain()
	}

	chain = filterStaleModels(chain)
	chain = accounts.SortModelsByAvailability(chain)
	if liveConfig().OnlyFree {
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
	if liveConfig().SessionAffinity.Enabled {
		affinitySk = providers.SessionKeyFromParams(params)
	}
	var lastErr error
	lastWas429 := false
	for _, model := range chain {
		params["model"] = model
		_, pin, refErr := ResolveModelReference(model)
		if refErr != nil {
			lastErr = refErr
			continue
		}
		if pin.active() {
			ep := WithModel(params, model)
			resp, acc, err := CallClineAPI(ep, stream)
			if err == nil {
				copyServingParams(params, ep)
				return resp, acc, nil
			}
			lastErr = err
			continue
		}

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

func copyServingParams(dst, src map[string]any) {
	for _, key := range []string{"model", ServedByParam, ProviderNameParam, providers.ProviderIDParam} {
		if value, ok := src[key]; ok {
			dst[key] = value
		} else {
			delete(dst, key)
		}
	}
}

// A Cline pin can rotate credentials for this model, but never changes the
// model or falls through to a custom provider or OpenCode.
func callPinnedClineModel(params map[string]any, stream bool, model string) (*http.Response, *domain.Account, error) {
	tried := map[string]bool{}
	for {
		account := accounts.PickAccountForModelStrict(model)
		if account == nil || tried[account.AccountID] {
			return nil, nil, &FreeModelUnavailableError{Message: fmt.Sprintf("model %q has no eligible Cline accounts", model)}
		}
		tried[account.AccountID] = true
		resp, used, err := CallClineAPIWithAccountCtx(context.Background(), account, params, stream)
		if err == nil {
			return resp, used, nil
		}
		var unavailable *ClineAccountUnavailableError
		if errors.As(err, &unavailable) {
			continue
		}
		status := egress.ErrorStatus(err)
		if status == http.StatusTooManyRequests || status == http.StatusPaymentRequired {
			if status == http.StatusPaymentRequired && used != nil {
				SetModelCooldown(used, model, time.Now().Add(30*time.Minute))
			}
			continue
		}
		return nil, used, err
	}
}
