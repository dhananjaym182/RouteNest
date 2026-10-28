package modelcatalog

import "time"

// ImportItem is one model in a preview import batch.
type ImportItem struct {
	ModelID string `json:"modelId"`
	Tier    string `json:"tier"`
	Signal  string `json:"signal"`
	Action  string `json:"action"` // add | tier_change | nochange | protected
	OldTier string `json:"oldTier,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// BatchStats is the server-authoritative tally of an import batch. Admin views
// read these numbers instead of recomputing them over ad-hoc item subsets.
//
// Invariants: Total == Free+Paid, Add+NoChange+Protected+TierChange == Total,
// Selectable == Add+TierChange and likewise for the free/paid breakdowns.
type BatchStats struct {
	Total          int `json:"total"`
	Free           int `json:"free"`
	Paid           int `json:"paid"`
	Add            int `json:"add"`
	AddFree        int `json:"addFree"`
	AddPaid        int `json:"addPaid"`
	TierChange     int `json:"tierChange"`
	TierChangeFree int `json:"tierChangeFree"`
	TierChangePaid int `json:"tierChangePaid"`
	NoChange       int `json:"noChange"`
	Protected      int `json:"protected"`
	Selectable     int `json:"selectable"` // add + tier_change
	SelectableFree int `json:"selectableFree"`
	SelectablePaid int `json:"selectablePaid"`
}

// ImportBatch is an in-memory preview awaiting administrator approval.
type ImportBatch struct {
	ID           string       `json:"id"`
	ProviderID   string       `json:"providerId"`
	ProviderName string       `json:"providerName"`
	CreatedAt    time.Time    `json:"createdAt"`
	Auto         bool         `json:"auto,omitempty"`
	Items        []ImportItem `json:"items"`
	Stats        BatchStats   `json:"stats"`
}

// Existing describes the pool entry an imported model would collide with.
type Existing struct {
	Source     string
	TierSignal string
	Cost       string
}

// BuildImportItems classifies entries and diffs them against existing pool
// entries. The caller owns batch persistence and pool locking. The returned
// stats are tallied from the finished items slice in one pass.
func BuildImportItems(entries []Entry, providerFree bool, existing map[string]Existing, allowlist map[string]bool) ([]ImportItem, BatchStats) {
	items := make([]ImportItem, 0, len(entries))
	for _, e := range entries {
		tier, signal := Classify(e, providerFree, allowlist)
		item := ImportItem{ModelID: e.ID, Tier: tier, Signal: signal, Action: "add"}
		if m, ok := existing[e.ID]; ok {
			switch {
			case m.Source != "" && m.Source != "provider":
				item.Action = "protected"
				item.Reason = "模型归属 " + m.Source + " 同步链，不跨源改层级"
			case m.TierSignal == "manual":
				item.Action = "protected"
				item.Reason = "用户手动设置过层级"
			case m.Cost == tier:
				item.Action = "nochange"
			default:
				item.Action = "tier_change"
				item.OldTier = m.Cost
			}
		}
		items = append(items, item)
	}
	return items, computeBatchStats(items)
}

// computeBatchStats tallies a finished item slice in a single pass so every
// view of a batch reports the same numbers.
func computeBatchStats(items []ImportItem) BatchStats {
	var s BatchStats
	for _, it := range items {
		free := it.Tier == "free"
		s.Total++
		if free {
			s.Free++
		} else {
			s.Paid++
		}
		switch it.Action {
		case "add":
			s.Add++
			if free {
				s.AddFree++
			} else {
				s.AddPaid++
			}
		case "tier_change":
			s.TierChange++
			if free {
				s.TierChangeFree++
			} else {
				s.TierChangePaid++
			}
		case "nochange":
			s.NoChange++
		case "protected":
			s.Protected++
		}
	}
	s.Selectable = s.Add + s.TierChange
	s.SelectableFree = s.AddFree + s.TierChangeFree
	s.SelectablePaid = s.AddPaid + s.TierChangePaid
	return s
}
