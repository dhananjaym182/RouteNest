package routing

import (
	"log"
	"strings"
	"time"

	"routenest/internal/dispatch"
	"routenest/internal/domain"
	"routenest/internal/models"
	"routenest/internal/providers"
	"routenest/internal/zen"
)

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
	var result []domain.Model
	models.Read(func(list []domain.Model) {
		result = allModelsFrom(list)
	})
	return result
}

// allModelsFrom computes AllModels for a list read under the pool lock.
func allModelsFrom(list []domain.Model) []domain.Model {

	var custom []domain.Model
	var remote []domain.Model
	var zenModels []domain.Model
	for _, m := range list {
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
	var result string
	models.View(func(p *domain.AccountPool) {
		result = defaultModelFrom(p)
	})
	return result
}

// defaultModelFrom resolves the default model for a pool read under the lock.
func defaultModelFrom(p *domain.AccountPool) string {
	if p.DefaultModel != "" {
		defaultDisabled := false
		for _, m := range p.Models {
			if m.ID == p.DefaultModel && m.Disabled {
				defaultDisabled = true
				break
			}
		}
		if !defaultDisabled && (!RemoteModelsActive() || LiveModelSet(p.Models)[p.DefaultModel]) {
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
	m, ok := models.Find(func(m domain.Model) bool {
		return m.ID == model && !zen.IsSource(m) && m.Output > 0
	})
	if !ok {
		return 0
	}
	return m.Output
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
	known := map[string]bool{}
	var remoteFree []string
	// One locked pass collects the ids and the free classifications.
	models.Read(func(list []domain.Model) {
		for _, m := range list {
			known[m.ID] = true
			if remote && m.Source == "remote" && m.Cost == "free" && m.Status == "active" {
				remoteFree = append(remoteFree, m.ID)
			}
			if m.Source == "provider" && m.Cost == "free" && m.Status == "active" && !m.Disabled {
				remoteFree = append(remoteFree, m.ID)
			}
		}
	})
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
// provider 声明的模型。list 是调用方在池锁内取得的模型列表；provider 列表
// 自有锁，此处不额外持池锁。
func LiveModelSet(list []domain.Model) map[string]bool {
	live := make(map[string]bool, len(list))
	disabled := make(map[string]bool)
	for _, m := range list {
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
	_, disabled := models.Find(func(m domain.Model) bool { return m.ID == id && m.Disabled })
	return disabled
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
	var live map[string]bool
	models.Read(func(list []domain.Model) { live = LiveModelSet(list) })
	out, dropped := dispatch.FilterLive(chain, true, live)
	if len(dropped) == 0 {
		return out
	}
	log.Printf("  model chain: dropped delisted model(s) %v -> %v", dropped, out)
	return out
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

// IsFreeModelID 判断模型是否为 free 计费（用于按模型统计和模型级冷却）。
func IsFreeModelID(model string) bool {
	for _, m := range AllModels() {
		if m.ID == model {
			return m.Cost == "free"
		}
	}

	return strings.HasSuffix(model, ":free") || strings.Contains(model, "/free/")
}
