package providers

import (
	"log"
	"routenest/internal/models"
	"sort"
)

// SyncModels 把启用中 provider 声明的模型同步进账号池（Source="provider"）：
// 模型管理页 / /v1/models / 默认模型下拉 / 回退链均以池为唯一数据源，同步后
// provider 模型与 Cline / zen 模型享有同样的列表展示、启停与自动回退语义。
// 规则：
//   - 仅同步 Enabled provider 的 modelIds；同 ID 已有其他来源条目（remote/zen/
//     用户自定义）时不重复添加，路由仍由 ResolveOne 按 ID 命中
//   - 池内已有 provider 条目时只刷新归属与计费，保留 Disabled / Context /
//     Output / MetaLocked（用户手动改过的值不被覆盖）
//   - provider 删除 / 停用 / 移除模型后，对应池内条目自动清理；指向已清理
//     模型的默认模型一并清空
func SyncModels() {
	provs := List() // Mu 自持自放；之后再拿 accounts.PoolMu，无反向持锁路径
	claimed := map[string]models.ProviderClaim{}
	var ids []string
	for _, p := range provs {
		if !p.Enabled {
			continue
		}
		for _, id := range p.ModelIDs {
			if id == "" {
				continue
			}
			if _, ok := claimed[id]; !ok {
				// Classify ids at seed time (same semantics as Cline/Zen sync):
				// ":free"/"-free" suffixes, a standalone "free" word anywhere in
				// the id, the zen-seed allowlist, and the provider-level free
				// flag all land in the pool as free immediately, without the
				// import wizard. Only a parsed non-zero price (catalog path)
				// can override to paid.
				c := models.ProviderClaim{ID: id, Provider: p.Name, Cost: "pass"}
				if tier, sig := classifyUpstreamModel(upstreamModelEntry{ID: id}, p.Free); tier == "free" {
					c.Cost, c.TierSignal = "free", sig
				}
				claimed[id] = c
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids) // 稳定顺序，避免 map 遍历乱序导致池内顺序抖动
	claims := make([]models.ProviderClaim, 0, len(ids))
	for _, id := range ids {
		claims = append(claims, claimed[id])
	}
	if !models.ProjectProvider(claims) {
		return
	}
	log.Printf("provider models synced: %d model(s) from %d enabled provider(s)", len(claimed), len(provs))
}
