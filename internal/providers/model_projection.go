package providers

import (
	"log"
	"sort"
	"time"

	"routenest/internal/accounts"
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
	type claim struct {
		name   string
		cost   string
		signal string // id-only classifier hit, stamped as TierSignal provenance
	}
	claimed := map[string]claim{}
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
				c := claim{name: p.Name, cost: "pass"}
				if tier, sig := classifyUpstreamModel(upstreamModelEntry{ID: id}, p.Free); tier == "free" {
					c.cost, c.signal = "free", sig
				}
				claimed[id] = c
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids) // 稳定顺序，避免 map 遍历乱序导致池内顺序抖动

	p := accounts.LoadPool()
	accounts.PoolMu.Lock()
	changed := false
	inPool := make(map[string]bool, len(p.Models))
	for _, m := range p.Models {
		inPool[m.ID] = true
	}
	kept := make([]Model, 0, len(p.Models)+len(ids))
	for _, m := range p.Models {
		if m.Source == "provider" {
			if _, ok := claimed[m.ID]; !ok {
				changed = true // provider 已删除 / 停用 / 移除该模型
				continue
			}
		}
		kept = append(kept, m)
	}
	for _, id := range ids {
		c := claimed[id]
		if inPool[id] {
			// 已有条目：provider 条目刷新归属与计费（其他来源条目不动）
			for i := range kept {
				if kept[i].ID == id && kept[i].Source == "provider" {
					if kept[i].Provider != c.name {
						kept[i].Provider = c.name
						changed = true
					}
					// §9.3：分级器批准过的模型保留逐模型层级，不被整源层级覆盖。
					// Untiered entries that hit an id-only free signal are upgraded
					// in place with provenance, repairing entries seeded before the
					// classifier existed; otherwise the provider-wide tier applies.
					if kept[i].TierSignal == "" {
						if c.signal != "" && kept[i].Cost != "free" {
							kept[i].Cost = "free"
							kept[i].TierSignal = c.signal
							kept[i].TierDecidedAt = time.Now().Unix()
							changed = true
						} else if c.signal == "" && kept[i].Cost != c.cost {
							kept[i].Cost = c.cost
							changed = true
						}
					}
					break
				}
			}
			continue
		}
		m := Model{ID: id, Provider: c.name, Cost: c.cost, Status: "active", Source: "provider", TierSignal: c.signal}
		if c.signal != "" {
			m.TierDecidedAt = time.Now().Unix()
		}
		kept = append(kept, m)
		changed = true
	}
	if !changed {
		accounts.PoolMu.Unlock()
		return
	}
	if p.DefaultModel != "" {
		stillListed := false
		for _, m := range kept {
			if m.ID == p.DefaultModel {
				stillListed = true
				break
			}
		}
		if !stillListed {
			p.DefaultModel = ""
		}
	}
	p.Models = kept
	accounts.PoolMu.Unlock()
	accounts.SavePool()
	log.Printf("provider models synced: %d model(s) from %d enabled provider(s)", len(claimed), len(provs))
}
