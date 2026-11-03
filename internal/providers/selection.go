package providers

import (
	"sort"
	"strings"
	"sync"
	"time"

	"routenest/internal/connections"
)

// ResolveForModel 找到该模型所有可用 provider（启用中、未冷却），按
// Priority 升序（quality 轮询策略下改为实测成绩优先）返回**指针副本**，调用
// 方在锁外使用。同一个模型配置了多个 provider 时形成冗余组：首选失败可立即
// 换组内下一家重试，全部失败才降级到回退链上的其他模型。冷却中与预算超限的
// provider 不入候选，路由在派发前就避开配额墙。
func ResolveForModel(model string) []*CustomProvider {
	Load()
	Mu.Lock()
	defer Mu.Unlock()
	var cs []providerCand
	now := time.Now()
	for _, p := range CustomProviders {
		if !p.Enabled {
			continue
		}
		for _, m := range p.ModelIDs {
			if m != model {
				continue
			}
			if until, cool := Cooldowns[p.ID+":"+model]; cool && now.Before(until) {
				break // 该 provider 的该模型冷却中
			}
			// 预算上限：超限的 provider 不入候选，路由在派发前就避开配额墙
			if providerBudgetExceeded(p.ID, model, p.RpmLimit, p.DailyLimit, p.TokenLimit, p.TokenWindow) {
				break
			}
			// Bound connections: provider stays out of candidates only when EVERY
			// pooled connection is missing/expired/pending reauth (fallback chain takes over).
			if len(connections.ProviderConnIDs(p)) > 0 && !connections.HasUsableConnection(p) {
				break
			}
			cs = append(cs, providerCand{p: *cloneProvider(p)})
			break
		}
	}
	cs = orderProviderCands(cs, model)
	out := make([]*CustomProvider, 0, len(cs))
	for i := range cs {
		out = append(out, &cs[i].p)
	}
	return out
}

// ResolveOne 找到该模型的首选 provider（启用中、未冷却、优先级最小）。
func ResolveOne(model string) *CustomProvider {
	cs := ResolveForModel(model)
	if len(cs) == 0 {
		return nil
	}
	return cs[0]
}

// providerFallbackModels 该模型的所有 provider 候选按优先级排序的模型链：
// 原模型优先，然后是 modelChain（配置链）中启用了 provider 的模型。
func Serves(model string) bool {
	return ResolveOne(model) != nil
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

// NameServesModel 判断是否存在**启用中**的 provider，其 Name 匹配（去空白、
// 大小写不敏感）且模型列表包含该模型。校验用：不关心冷却/预算（那是运行时的事）。
func NameServesModel(name, model string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, p := range List() {
		if !p.Enabled || !strings.EqualFold(strings.TrimSpace(p.Name), name) {
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

// ResolveNamedForModel 与 ResolveForModel 同规则（启用、未冷却、
// 未超预算、服务该模型），但只保留 Name 匹配给定名称（去空白、大小写不敏感）的
// provider。用于组合条目把模型钉到某一家上游。
func ResolveNamedForModel(model, name string) []*CustomProvider {
	Load()
	Mu.Lock()
	defer Mu.Unlock()
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	var cs []providerCand
	now := time.Now()
	for _, p := range CustomProviders {
		if !p.Enabled || !strings.EqualFold(strings.TrimSpace(p.Name), name) {
			continue
		}
		for _, m := range p.ModelIDs {
			if m != model {
				continue
			}
			if until, cool := Cooldowns[p.ID+":"+model]; cool && now.Before(until) {
				break // 该 provider 的该模型冷却中
			}
			if providerBudgetExceeded(p.ID, model, p.RpmLimit, p.DailyLimit, p.TokenLimit, p.TokenWindow) {
				break
			}
			cs = append(cs, providerCand{p: *cloneProvider(p)})
			break
		}
	}
	cs = orderProviderCands(cs, model)
	out := make([]*CustomProvider, 0, len(cs))
	for i := range cs {
		out = append(out, &cs[i].p)
	}
	return out
}
