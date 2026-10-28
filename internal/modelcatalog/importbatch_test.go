package modelcatalog

import (
	"encoding/json"
	"testing"
)

func TestBuildImportItemsActions(t *testing.T) {
	entries := []Entry{
		{ID: "new-free", FreeFlag: true},
		{ID: "remote-model"},
		{ID: "manual-model"},
		{ID: "same-tier"},
		{ID: "changed-tier"},
	}
	existing := map[string]Existing{
		"remote-model": {Source: "remote", Cost: "free"},
		"manual-model": {TierSignal: "manual", Cost: "pass"},
		"same-tier":    {Source: "provider", Cost: "pass"},
		"changed-tier": {Source: "provider", Cost: "free"},
	}
	items, _ := BuildImportItems(entries, false, existing, nil)
	if len(items) != 5 {
		t.Fatalf("items = %+v", items)
	}
	byID := map[string]ImportItem{}
	for _, it := range items {
		byID[it.ModelID] = it
	}
	if byID["new-free"].Action != "add" || byID["new-free"].Tier != "free" {
		t.Errorf("new-free = %+v", byID["new-free"])
	}
	if byID["remote-model"].Action != "protected" || byID["remote-model"].Reason == "" {
		t.Errorf("remote-model = %+v", byID["remote-model"])
	}
	if byID["manual-model"].Action != "protected" {
		t.Errorf("manual-model = %+v", byID["manual-model"])
	}
	if byID["same-tier"].Action != "nochange" {
		t.Errorf("same-tier = %+v", byID["same-tier"])
	}
	if byID["changed-tier"].Action != "tier_change" || byID["changed-tier"].OldTier != "free" || byID["changed-tier"].Tier != "pass" {
		t.Errorf("changed-tier = %+v", byID["changed-tier"])
	}
}

func TestBuildImportItemsEmpty(t *testing.T) {
	got, stats := BuildImportItems(nil, false, nil, nil)
	if len(got) != 0 {
		t.Errorf("items = %+v", got)
	}
	if stats != (BatchStats{}) {
		t.Errorf("empty batch stats must be all zeros, got %+v", stats)
	}
}

// mixed import batch: add(free), add(paid), tier_change(free→paid),
// tier_change(paid→free), nochange, protected(remote), protected(manual).
func mixedBatchFixture() ([]ImportItem, BatchStats) {
	entries := []Entry{
		{ID: "add/free", FreeFlag: true},
		{ID: "add/paid", PricingPrompt: f(1), PricingComplet: f(2)},
		{ID: "tc/paid", PricingPrompt: f(1), PricingComplet: f(2)},
		{ID: "tc/free", FreeFlag: true},
		{ID: "same/paid", PricingPrompt: f(1), PricingComplet: f(2)},
		{ID: "remote/one", PricingPrompt: f(1), PricingComplet: f(2)},
		{ID: "manual/one", FreeFlag: true},
	}
	existing := map[string]Existing{
		"tc/paid":    {Source: "provider", Cost: "free"},
		"tc/free":    {Source: "provider", Cost: "pass"},
		"same/paid":  {Source: "provider", Cost: "pass"},
		"remote/one": {Source: "remote", Cost: "pass"},
		"manual/one": {Source: "provider", TierSignal: "manual", Cost: "free"},
	}
	return BuildImportItems(entries, false, existing, nil)
}

func TestBuildImportItemsStatsMixedBatch(t *testing.T) {
	items, stats := mixedBatchFixture()
	if len(items) != 7 {
		t.Fatalf("items = %+v", items)
	}
	want := BatchStats{
		Total:          7,
		Free:           3, // add/free, tc/free, manual/one
		Paid:           4,
		Add:            2,
		AddFree:        1,
		AddPaid:        1,
		TierChange:     2,
		TierChangeFree: 1,
		TierChangePaid: 1,
		NoChange:       1,
		Protected:      2,
		Selectable:     4,
		SelectableFree: 2,
		SelectablePaid: 2,
	}
	if stats != want {
		t.Fatalf("stats = %+v, want %+v", stats, want)
	}
	// Sanity-check the tier split actually matches the items (guards the
	// fixture above from drifting).
	free, paid := 0, 0
	for _, it := range items {
		if it.Tier == "free" {
			free++
		} else {
			paid++
		}
	}
	if free != want.Free || paid != want.Paid {
		t.Fatalf("tier split free=%d paid=%d, want %d/%d", free, paid, want.Free, want.Paid)
	}
}

func TestBatchStatsInvariants(t *testing.T) {
	cases := []struct {
		name string
		fn   func() ([]ImportItem, BatchStats)
	}{
		{"empty", func() ([]ImportItem, BatchStats) { return BuildImportItems(nil, false, nil, nil) }},
		{"mixed", mixedBatchFixture},
	}
	for _, tc := range cases {
		items, s := tc.fn()
		freeRemainder := 0
		for _, it := range items {
			if it.Tier == "free" && (it.Action == "nochange" || it.Action == "protected") {
				freeRemainder++
			}
		}
		if s.Total != s.Free+s.Paid {
			t.Errorf("%s: Total %d != Free+Paid %d", tc.name, s.Total, s.Free+s.Paid)
		}
		if s.Total != s.Add+s.NoChange+s.Protected+s.TierChange {
			t.Errorf("%s: Total %d != Add+NoChange+Protected+TierChange %d", tc.name, s.Total,
				s.Add+s.NoChange+s.Protected+s.TierChange)
		}
		if s.Selectable != s.Add+s.TierChange {
			t.Errorf("%s: Selectable %d != Add+TierChange %d", tc.name, s.Selectable, s.Add+s.TierChange)
		}
		if s.SelectableFree != s.AddFree+s.TierChangeFree {
			t.Errorf("%s: SelectableFree %d != AddFree+TierChangeFree %d", tc.name, s.SelectableFree, s.AddFree+s.TierChangeFree)
		}
		if s.SelectablePaid != s.AddPaid+s.TierChangePaid {
			t.Errorf("%s: SelectablePaid %d != AddPaid+TierChangePaid %d", tc.name, s.SelectablePaid, s.AddPaid+s.TierChangePaid)
		}
		if s.Free != s.AddFree+s.TierChangeFree+freeRemainder {
			t.Errorf("%s: free breakdown inconsistent: %+v", tc.name, s)
		}
		if s.AddFree+s.AddPaid != s.Add {
			t.Errorf("%s: AddFree+AddPaid %d != Add %d", tc.name, s.AddFree+s.AddPaid, s.Add)
		}
		if s.TierChangeFree+s.TierChangePaid != s.TierChange {
			t.Errorf("%s: TierChangeFree+TierChangePaid != TierChange (%+v)", tc.name, s)
		}
	}
}

func TestImportBatchStatsJSON(t *testing.T) {
	items, stats := mixedBatchFixture()
	batch := ImportBatch{ID: "b1", ProviderID: "p1", ProviderName: "P", Items: items, Stats: stats}
	raw, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Stats map[string]int `json:"stats"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"total", "free", "paid", "add", "addFree", "addPaid",
		"tierChange", "tierChangeFree", "tierChangePaid",
		"noChange", "protected", "selectable", "selectableFree", "selectablePaid",
	} {
		if _, ok := decoded.Stats[key]; !ok {
			t.Errorf("stats missing json key %q in %s", key, raw)
		}
	}
	if decoded.Stats["total"] != 7 || decoded.Stats["selectable"] != 4 {
		t.Errorf("stats json values wrong: %v", decoded.Stats)
	}
}
