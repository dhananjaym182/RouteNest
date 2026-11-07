package models

import (
	"time"

	"routenest/internal/domain"
)

// ProviderClaim is one model id that an enabled custom provider declares.
type ProviderClaim struct {
	ID         string
	Provider   string // display name of the claiming provider
	Cost       string // "pass" or "free" from the provider-level classification
	TierSignal string // id-only free provenance, or "" when none
	Shared     bool   // another custom provider also declares this model ID
}

// ProjectProvider reconciles provider-sourced records with the claims:
//   - a provider record no longer claimed is removed;
//   - a claimed id already present under another source is left alone;
//   - an existing provider record gets the claiming provider's name, and its cost
//     follows the provider classification unless a per-model tier was decided;
//   - a new claim is added as a provider record.
//
// Claims must be sorted by id, so new records keep a stable order. When the
// default model is no longer listed after a change, the default is cleared.
// It reports whether the list changed.
func ProjectProvider(claims []ProviderClaim) bool {
	now := time.Now().Unix()
	return Edit(func(p *domain.AccountPool) bool {
		claimed := make(map[string]ProviderClaim, len(claims))
		for _, c := range claims {
			claimed[c.ID] = c
		}
		inPool := make(map[string]bool, len(p.Models))
		for _, m := range p.Models {
			inPool[m.ID] = true
		}
		changed := false
		kept := make([]domain.Model, 0, len(p.Models)+len(claims))
		for _, m := range p.Models {
			if m.Source == SourceProvider {
				if _, ok := claimed[m.ID]; !ok {
					changed = true // provider deleted, disabled, or dropped the model
					continue
				}
			}
			kept = append(kept, m)
		}
		for _, c := range claims {
			if inPool[c.ID] {
				for i := range kept {
					if kept[i].ID != c.ID || kept[i].Source != SourceProvider {
						continue
					}
					// A shared logical row is only a compatibility projection. Its
					// existing owner and pricing must not depend on provider order.
					if c.Shared {
						break
					}
					if kept[i].Provider != c.Provider {
						kept[i].Provider = c.Provider
						changed = true
					}
					// A per-model tier decided by the import classifier is kept;
					// the provider-wide tier applies only to undecided records.
					if kept[i].TierSignal == "" {
						if c.TierSignal != "" && kept[i].Cost != CostFree {
							kept[i].Cost = CostFree
							kept[i].TierSignal = c.TierSignal
							kept[i].TierDecidedAt = now
							changed = true
						} else if c.TierSignal == "" && kept[i].Cost != c.Cost {
							kept[i].Cost = c.Cost
							changed = true
						}
					}
					break
				}
				continue
			}
			m := domain.Model{ID: c.ID, Provider: c.Provider, Cost: c.Cost, Status: "active", Source: SourceProvider, TierSignal: c.TierSignal}
			if c.TierSignal != "" {
				m.TierDecidedAt = now
			}
			kept = append(kept, m)
			changed = true
		}
		if !changed {
			return false
		}
		if p.DefaultModel != "" {
			listed := false
			for _, m := range kept {
				if m.ID == p.DefaultModel {
					listed = true
					break
				}
			}
			if !listed {
				p.DefaultModel = ""
			}
		}
		p.Models = kept
		return true
	})
}

// Decision is one approved import action: set the cost and signal of the model
// id, adding the record when it is not present.
type Decision struct {
	ID     string
	Cost   string
	Signal string
}

// ApplyDecisions applies approved import decisions. An existing id (from any
// source) gets the new cost, signal and decision time. An unknown id is added as
// a provider record. Updates use indexes, so an append never invalidates them.
// It returns the ids added and the number of existing records changed.
func ApplyDecisions(decisions []Decision, providerName string, now int64) (added []string, changed int) {
	Edit(func(p *domain.AccountPool) bool {
		index := make(map[string]int, len(p.Models))
		for i := range p.Models {
			index[p.Models[i].ID] = i // the last record with an id wins, as before
		}
		for _, d := range decisions {
			if i, ok := index[d.ID]; ok {
				m := &p.Models[i]
				m.Cost, m.TierSignal, m.TierDecidedAt = d.Cost, d.Signal, now
				changed++
				continue
			}
			p.Models = append(p.Models, domain.Model{
				ID: d.ID, Provider: providerName, Cost: d.Cost, Status: "active",
				Source: SourceProvider, TierSignal: d.Signal, TierDecidedAt: now,
			})
			added = append(added, d.ID)
		}
		return changed+len(added) > 0
	})
	return added, changed
}

// ApplyProviderProjection mirrors provider-scoped decisions into the legacy
// logical-model list without letting one provider overwrite another provider or
// a built-in source. Existing rows are updated only when the offering is
// exclusive and the row is already owned by providerName; unknown IDs are added.
// Routing must use provider-scoped ModelOfferings rather than this projection.
func ApplyProviderProjection(decisions []Decision, providerName string, exclusive map[string]bool, now int64) (added []string, changed int) {
	Edit(func(p *domain.AccountPool) bool {
		index := make(map[string]int, len(p.Models))
		for i := range p.Models {
			index[p.Models[i].ID] = i
		}
		for _, decision := range decisions {
			if i, ok := index[decision.ID]; ok {
				model := &p.Models[i]
				if !exclusive[decision.ID] || model.Source != SourceProvider || model.Provider != providerName {
					continue
				}
				if model.Cost == decision.Cost && model.TierSignal == decision.Signal && !model.Delisted {
					continue
				}
				model.Cost = decision.Cost
				model.TierSignal = decision.Signal
				model.TierDecidedAt = now
				model.Delisted = false
				changed++
				continue
			}
			p.Models = append(p.Models, domain.Model{
				ID: decision.ID, Provider: providerName, Cost: decision.Cost, Status: "active",
				Source: SourceProvider, TierSignal: decision.Signal, TierDecidedAt: now,
			})
			index[decision.ID] = len(p.Models) - 1
			added = append(added, decision.ID)
		}
		return changed+len(added) > 0
	})
	return added, changed
}

// ApplyProviderDelistedProjection updates the legacy provider row to the
// aggregate state of all enabled custom-provider offerings with that model ID.
// It never touches Cline, Zen, or administrator-owned rows.
func ApplyProviderDelistedProjection(delisted map[string]bool) int {
	changed := 0
	Edit(func(p *domain.AccountPool) bool {
		for i := range p.Models {
			model := &p.Models[i]
			want, ok := delisted[model.ID]
			if !ok || model.Source != SourceProvider || model.Delisted == want {
				continue
			}
			model.Delisted = want
			changed++
		}
		return changed > 0
	})
	return changed
}

// SetManualCost sets the cost of the first record with id and marks it as a
// manual tier. An empty cost clears the manual marker and keeps the current
// cost until the next import or sync decides it. It reports whether a record
// was found.
func SetManualCost(id, cost string, now int64) bool {
	found := false
	Edit(func(p *domain.AccountPool) bool {
		for i := range p.Models {
			if p.Models[i].ID != id {
				continue
			}
			if cost == "" {
				p.Models[i].TierSignal = ""
				p.Models[i].TierDecidedAt = 0
			} else {
				p.Models[i].Cost = cost
				p.Models[i].TierSignal = "manual"
				p.Models[i].TierDecidedAt = now
			}
			found = true
			return true
		}
		return false
	})
	return found
}

// ApplyDelisting marks provider records that are declared but missing upstream
// (ids in toMark) as delisted, and clears the mark on records that are back
// upstream. Only provider records whose ids the provider declares are touched.
// It returns the number of records newly marked and the number of declared
// provider records that are delisted after the change.
func ApplyDelisting(declared map[string]bool, toMark []string, upstream map[string]bool) (marked, alreadyMarked int) {
	mark := make(map[string]bool, len(toMark))
	for _, id := range toMark {
		mark[id] = true
	}
	Edit(func(p *domain.AccountPool) bool {
		changed := false
		for i := range p.Models {
			m := &p.Models[i]
			if m.Source != SourceProvider || !declared[m.ID] {
				continue
			}
			if mark[m.ID] {
				if !m.Delisted {
					m.Delisted = true
					marked++
					changed = true
				}
			} else if upstream[m.ID] && m.Delisted {
				m.Delisted = false
				changed = true
			}
		}
		for _, m := range p.Models {
			if m.Source == SourceProvider && m.Delisted && declared[m.ID] {
				alreadyMarked++
			}
		}
		return changed
	})
	return marked, alreadyMarked
}
