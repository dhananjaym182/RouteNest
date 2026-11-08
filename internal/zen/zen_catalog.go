package zen

import (
	"log"
	"strings"
	"sync"

	"routenest/internal/domain"
	"routenest/internal/models"
)

type Model = domain.Model

// SeedModels 内置 zen 免费模型种子表（含别名），仅作为从未同步成功时的离线 fallback。
// 与 builtinModels（Cline 侧）同一模式：同步成功后以远程列表为准。
// 列表与 2026-09-22 线上 /models 实测的免费模型保持一致。
type SeedModel struct {
	ID      string
	Aliases []string
	Context int
	Output  int
}

// SeedModels 种子表三用途：离线 fallback、免费判定白名单、别名解析。
// Context 默认 1M（2026-09 主流模型普遍 1M 上下文；压缩阈值按此计算，
// 偏小会让压缩过早触发——曾导致 200K 默认值把 1M 模型压到几万 token）。
var SeedModels = []SeedModel{
	{ID: "deepseek-v4-flash-free", Aliases: []string{"deepseek-v4-flash", "deepseek-v4"}, Context: 200000, Output: 128000},
	{ID: "mimo-v2.6-flash-free", Aliases: []string{"mimo-v2.6-flash", "mimo-v2.6", "mimo"}, Context: 200000, Output: 32000},
	{ID: "mimo-v2.5-free", Aliases: []string{"mimo-v2.5"}, Context: 200000, Output: 32000},
	{ID: "ling-3.0-flash-fin-free", Aliases: []string{"ling-3.0-flash", "ling"}, Context: 200000, Output: 32768},
	{ID: "nemotron-3-ultra-free", Aliases: []string{"nemotron-3-ultra", "nemotron"}, Context: 200000, Output: 128000},
	{ID: "nemotron-3.5-lightning-free", Aliases: []string{"nemotron-3.5-lightning"}, Context: 200000, Output: 32768},
	{ID: "jev-1.13-free", Context: 200000, Output: 32768},
	{ID: "muse-spark-1.3-contributor-free", Context: 200000, Output: 32768},
	{ID: "muse-spark-1.2-contributor-free", Context: 200000, Output: 32768},
	{ID: "big-pickle", Context: 200000, Output: 32000},
}

// BuiltinModels 把种子表转成 Model 条目（离线 fallback 用，Source="seed"）。
func BuiltinModels() []Model {
	out := make([]Model, 0, len(SeedModels))
	for _, m := range SeedModels {
		out = append(out, Model{
			ID:       m.ID,
			Provider: "opencode",
			Cost:     "free",
			Status:   "active",
			Custom:   false,
			Source:   "seed",
			Context:  m.Context,
			Output:   m.Output,
		})
	}
	return out
}

// RemoteEnabled：zen 官方 /models 同步成功过 → 以远程列表为准，
// 种子表中已下架的模型自动休眠（不再出现在列表和路由里）。与 remoteModelsEnabled 同一模式。
var (
	RemoteEnabled   bool
	RemoteEnabledMu sync.Mutex
)

func RemoteActive() bool {
	RemoteEnabledMu.Lock()
	defer RemoteEnabledMu.Unlock()
	return RemoteEnabled
}

// IsSource 判断模型来源是否属于 opencode 体系（同步条目 "zen" / 内置种子 "seed"）。
func IsSource(m Model) bool {
	return m.Source == "zen" || m.Source == "seed"
}

// CurrentModels 返回当前生效的 zen 模型（pool 中 zen 来源条目；
// 从未同步成功时回退到种子表）。
func CurrentModels() []Model {
	var result []Model
	models.View(func(pool *domain.AccountPool) {
		for _, model := range models.FilterRemovedModelRecords(pool, pool.Models) {
			if IsSource(model) {
				result = append(result, model)
			}
		}
		if len(result) == 0 && !RemoteActive() && !models.CatalogInitialized(pool) {
			result = models.FilterRemovedModelRecords(pool, BuiltinModels())
		}
	})
	return result
}

// ResolveInfo 解析模型名到当前生效的 zen 模型。支持别名与 "opencode/" 前缀。
// 别名优先于精确 ID 匹配之后、但优先级高于付费同名 ID（种子的 free 别名不会被覆盖）。
func ResolveInfo(id string) (Model, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Model{}, false
	}
	models := CurrentModels()

	// 别名表：seed 模型的别名 → 正式 ID（用种子表数据补全上下文）
	contextOf := func(m Model) Model { return m }
	for _, sm := range SeedModels {
		for _, a := range sm.Aliases {
			if a == id {
				// 种子模型必须在当前生效列表里才算数（否则就是被下架了）
				for _, m := range models {
					if m.ID == sm.ID && m.Cost == "free" {
						return contextOf(m), true
					}
				}
				break
			}
		}
	}

	tryOne := func(name string) (Model, bool) {
		for _, m := range models {
			if m.ID == name {
				return m, true
			}
		}
		return Model{}, false
	}
	if m, ok := tryOne(id); ok {
		return m, true
	}
	if strings.HasPrefix(id, "opencode/") {
		if m, ok := tryOne(strings.TrimPrefix(id, "opencode/")); ok {
			return m, true
		}
	}
	return Model{}, false
}

// IsFreeModel 判定 zen 模型是否免费（用于路由：非免费的 zen 模型直接拒绝）。
// 种子白名单兜底：官方免费模型即使同步条目漏标 -free 后缀也不会被误拒。
func IsFreeModel(m Model) bool {
	if m.Cost == "free" || strings.HasSuffix(m.ID, "-free") {
		return true
	}
	for _, sm := range SeedModels {
		if sm.ID == m.ID {
			return true
		}
	}
	return false
}

// RouteModel 三态路由："zen" / "reject" / "cline"。
// 故障转移开启且 zen 连续失败期间，免费模型请求临时改走 cline 账号池。
func RouteModel(id string) string {
	m, ok := ResolveInfo(id)
	if !ok {
		return "cline"
	}
	if !IsFreeModel(m) {
		return "reject"
	}
	// 极端情况：同名 ID 同时是 cline 模型（自定义冲突）→ 让给 cline
	wanted := strings.TrimPrefix(strings.TrimSpace(id), "opencode/")
	if _, clash := models.Find(func(pm Model) bool { return !IsSource(pm) && pm.ID == wanted }); clash {
		return "cline"
	}

	cfg := GetConfig()
	if cfg.Failover && FailedNow() {
		log.Printf("  failover: zen degraded, %q routed to cline pool", id)
		return "cline"
	}
	return "zen"
}
