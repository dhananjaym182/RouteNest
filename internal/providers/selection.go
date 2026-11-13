package providers

import (
	"sort"
	"strings"
	"sync"
	"time"

	"routenest/internal/config"
	"routenest/internal/connections"
	"routenest/internal/domain"
)

// ResolveForModel 找到该模型所有可用 provider（启用中、未冷却），按
// Priority 升序（quality 轮询策略下改为实测成绩优先）返回**指针副本**，调用
// 方在锁外使用。同一个模型配置了多个 provider 时形成冗余组：首选失败可立即
// 换组内下一家重试，全部失败才降级到回退链上的其他模型。冷却中与预算超限的
// provider 不入候选，路由在派发前就避开配额墙。
func ResolveForModel(model string) []*CustomProvider {
	return resolveForModel(model, false)
}

// ResolveForModelProbe resolves candidates for an explicit admin probe. It
// keeps provider/offering configuration checks but deliberately bypasses
// runtime cooldowns, connection cooling and configured-budget admission gates.
func ResolveForModelProbe(model string) []*CustomProvider {
	return resolveForModel(model, true)
}

func resolveForModel(model string, probe bool) []*CustomProvider {
	Load()
	Mu.Lock()
	defer Mu.Unlock()
	var cs []providerCand
	now := time.Now()
	legacyOwners := providerLegacyOwnerCountsLocked()
	for _, p := range CustomProviders {
		if !providerEligibleForProbeLocked(p, model, legacyOwners) {
			continue
		}
		if !probe {
			if until, cool := Cooldowns[p.ID+":"+model]; cool && now.Before(until) {
				continue // 该 provider 的该模型冷却中
			}
			// 预算上限：超限的 provider 不入候选，路由在派发前就避开配额墙
			if providerBudgetExceeded(p.ID, model, p.RpmLimit, p.DailyLimit, p.TokenLimit, p.TokenWindow) {
				continue
			}
			// Bound connections: provider stays out of candidates only when EVERY
			// pooled connection is missing/expired/pending reauth (fallback chain takes over).
			if len(connections.ProviderConnIDs(p)) > 0 && !connections.HasUsableConnection(p) {
				continue
			}
		}
		cs = append(cs, providerCand{p: *cloneProvider(p)})
	}
	cs = orderProviderCands(cs, model)
	out := make([]*CustomProvider, 0, len(cs))
	for i := range cs {
		out = append(out, &cs[i].p)
	}
	return out
}

func providerEligibleForProbeLocked(p *CustomProvider, model string, legacyOwners map[string]int) bool {
	if p == nil || !p.Enabled {
		return false
	}
	listed := false
	for _, candidate := range p.ModelIDs {
		if candidate == model {
			listed = true
			break
		}
	}
	return listed && providerOfferingEligibleScoped(*p, model, providerMayInheritLegacy(*p, model, legacyOwners))
}

// ResolveOne 找到该模型的首选 provider（启用中、未冷却、优先级最小）。
func ResolveOne(model string) *CustomProvider {
	cs := ResolveForModel(model)
	if len(cs) == 0 {
		return nil
	}
	return cs[0]
}

// ---- 候选排序 --------------------------------------------------------

// providerCand 候选 provider（包级定义，排序函数复用）。
type providerCand struct {
	p CustomProvider
}

// sortProviderCands 候选排序：默认/未知策略按 Priority 升序（并列按创建时间）；
// quality 策略按实测成绩优先 —— 窗口内成功率高者在前，等同程按平均时延升序，
// 仍平按配置优先级。无样本的候选拿中性值，不因无数据而插队压过实测优秀者。
func sortProviderCands(cs []providerCand, model string) {
	if liveConfig().Strategy == "quality" {
		sort.SliceStable(cs, func(i, j int) bool {
			ri, li := providerScoreStats(cs[i].p.ID, model)
			rj, lj := providerScoreStats(cs[j].p.ID, model)
			if ri != rj {
				return ri > rj
			}
			if li > 0 && lj > 0 && li != lj {
				return li < lj
			}
			if cs[i].p.Priority != cs[j].p.Priority {
				return cs[i].p.Priority < cs[j].p.Priority
			}
			return cs[i].p.CreatedAt.Before(cs[j].p.CreatedAt)
		})
		return
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].p.Priority != cs[j].p.Priority {
			return cs[i].p.Priority < cs[j].p.Priority
		}
		return cs[i].p.CreatedAt.Before(cs[j].p.CreatedAt)
	})
}

// ---- 加权轮询（smooth weighted round-robin）--------------------------------
//
// weighted_round_robin 策略下，候选按整数权重比例分布，而不是按优先级轮流。
// 权重为 0 视为 1（未设置），负值把候选从该策略排除。每个模型维护一份
// current-weight 状态，请求顺序在长期收敛到权重比例，且同轮内稳定可测。
var (
	providerWRRMu    sync.Mutex
	providerWRRState = map[string]float64{}
)

func effectiveProviderWeight(p CustomProvider) int {
	if p.Weight == 0 {
		return 1
	}
	return p.Weight
}

// orderProviderCandsWeighted 执行一次 smooth weighted round-robin 单选：
// 每个候选 current += weight，选 current 最大者放首位（并列取基线序前者），
// 其 current -= total，其余候选保持基线（优先级）顺序用于故障转移。
// 负权候选被剔除；全部非正时返回 nil（调用方落入回退链）。
func orderProviderCandsWeighted(cs []providerCand, model string) []providerCand {
	remaining := make([]providerCand, 0, len(cs))
	total := 0
	for _, c := range cs {
		w := effectiveProviderWeight(c.p)
		if w <= 0 {
			continue
		}
		total += w
		remaining = append(remaining, c)
	}
	if total == 0 {
		return nil
	}
	sort.SliceStable(remaining, func(i, j int) bool {
		if remaining[i].p.Priority != remaining[j].p.Priority {
			return remaining[i].p.Priority < remaining[j].p.Priority
		}
		return remaining[i].p.CreatedAt.Before(remaining[j].p.CreatedAt)
	})

	providerWRRMu.Lock()
	defer providerWRRMu.Unlock()
	if len(providerWRRState) > 10000 {
		providerWRRState = map[string]float64{}
	}
	best := 0
	var bestVal float64
	for i := range remaining {
		key := remaining[i].p.ID + ":" + model
		providerWRRState[key] += float64(effectiveProviderWeight(remaining[i].p))
		if i == 0 || providerWRRState[key] > bestVal {
			best, bestVal = i, providerWRRState[key]
		}
	}
	providerWRRState[remaining[best].p.ID+":"+model] -= float64(total)

	out := make([]providerCand, 0, len(remaining))
	out = append(out, remaining[best])
	for i := range remaining {
		if i != best {
			out = append(out, remaining[i])
		}
	}
	return out
}

// orderProviderCands 按当前路由策略产出候选顺序；weighted_round_robin 会重排
// 并可能剔除负权候选，其余策略沿用 sortProviderCands 的排序语义。
func orderProviderCands(cs []providerCand, model string) []providerCand {
	if liveConfig().Strategy == "weighted_round_robin" {
		return orderProviderCandsWeighted(cs, model)
	}
	sortProviderCands(cs, model)
	return cs
}

// NameServesModel accepts a stable provider ID or a legacy display-name pin.
// An ID wins over matching display names, including while its provider is
// disabled or does not serve the model. Validation ignores cooldown and budget.
func NameServesModel(name, model string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	list := List()
	idPin := GeneratedProviderIDPin(name) || config.ProviderModelIDReserved(name)
	for _, p := range list {
		if p.ID == name {
			idPin = true
			break
		}
	}
	for _, p := range list {
		if !p.Enabled || !providerPinMatches(p, name, idPin) {
			continue
		}
		for _, id := range p.ModelIDs {
			if id == model {
				return true
			}
		}
	}
	return false
}

// GeneratedProviderIDPin identifies the IDs assigned by this registry. They
// remain ID pins after deletion rather than acquiring display-name meaning.
func GeneratedProviderIDPin(pin string) bool {
	return strings.HasPrefix(strings.ToLower(pin), "prov_") && domain.ProviderInstanceID(pin).Valid()
}

func providerPinMatches(provider CustomProvider, pin string, idPin bool) bool {
	if idPin {
		return provider.ID == pin
	}
	return strings.EqualFold(strings.TrimSpace(provider.Name), pin)
}

// SplitPin parses the request-level qualified name "model@ProviderName"
// used to deliberately pick which provider serves a model id that multiple
// providers expose. LastIndexOf so ids containing '@' keep working; the pin is
// only honored when non-empty.
func SplitPin(model string) (bare, pin string) {
	i := strings.LastIndex(model, "@")
	if i <= 0 {
		return model, ""
	}
	bare, pin = model[:i], strings.TrimSpace(model[i+1:])
	if pin == "" {
		return model, ""
	}
	return bare, pin
}

// ResolveNamedForModel resolves an exact provider ID before legacy name groups.
// Eligibility cannot turn an unavailable ID pin into a display-name fallback.
// Model IDs, including embedded '@' characters, are passed through unchanged.
func ResolveNamedForModel(model, name string) []*CustomProvider {
	return resolveNamedForModel(model, name, false)
}

// ResolveNamedForModelProbe is the strict pinned-provider counterpart to
// ResolveForModelProbe. Identity, enabled state, model membership and offering
// validity remain enforced; transient runtime and budget gates are not.
func ResolveNamedForModelProbe(model, name string) []*CustomProvider {
	return resolveNamedForModel(model, name, true)
}

func resolveNamedForModel(model, name string, probe bool) []*CustomProvider {
	Load()
	Mu.Lock()
	defer Mu.Unlock()
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	idPin := GeneratedProviderIDPin(name) || config.ProviderModelIDReserved(name)
	for _, p := range CustomProviders {
		if p.ID == name {
			idPin = true
			break
		}
	}
	var cs []providerCand
	now := time.Now()
	legacyOwners := providerLegacyOwnerCountsLocked()
	for _, p := range CustomProviders {
		if !providerPinMatches(*p, name, idPin) {
			continue
		}
		if probe {
			if !providerEligibleForProbeLocked(p, model, legacyOwners) {
				continue
			}
		} else {
			// 禁用、不提供该模型、被下架、冷却中或预算用尽都排除；原因见 pinBlockLocked，
			// PinUnavailableReason 用同一判断向调用方解释。
			if pinBlockLocked(p, model, now, legacyOwners) != "" {
				continue
			}
		}
		cs = append(cs, providerCand{p: *cloneProvider(p)})
	}
	cs = orderProviderCands(cs, model)
	out := make([]*CustomProvider, 0, len(cs))
	for i := range cs {
		out = append(out, &cs[i].p)
	}
	return out
}

// AnyAccessPolicyGrant reports whether any enabled provider is set to let its
// models through Free-only routing. Routing checks it first so that the
// default, where no provider is, costs nothing extra.
func AnyAccessPolicyGrant() bool {
	Load()
	Mu.Lock()
	defer Mu.Unlock()
	for _, p := range CustomProviders {
		if p != nil && p.Enabled && p.AccessPolicy.Effective().Grants() {
			return true
		}
	}
	return false
}
