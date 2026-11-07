package models

import (
	"reflect"
	"testing"

	"routenest/internal/domain"
)

// Table: projection of provider claims. Each row gives the starting pool and the
// claims, then the expected records by id. Records are compared on the fields that
// reconciliation may touch.
func TestProjectProviderReconciliation(t *testing.T) {
	type want struct {
		provider, cost, signal string
		disabled, delisted     bool
		context                int
	}
	cases := []struct {
		name         string
		pool         []domain.Model
		defaultModel string
		claims       []ProviderClaim
		wantIDs      []string // resulting ids in order
		want         map[string]want
		wantDefault  string
		wantChanged  bool
	}{
		{
			name:    "new claim is added as a provider record",
			claims:  []ProviderClaim{{ID: "p/a", Provider: "Alpha", Cost: "pass"}},
			want:    map[string]want{"p/a": {provider: "Alpha", cost: "pass"}},
			wantIDs: []string{"p/a"}, wantChanged: true,
		},
		{
			name:    "claim already owned by another source is not duplicated",
			pool:    []domain.Model{{ID: "r/a", Source: SourceRemote, Cost: "pass"}},
			claims:  []ProviderClaim{{ID: "r/a", Provider: "Alpha", Cost: "pass"}},
			wantIDs: []string{"r/a"},
			want:    map[string]want{"r/a": {provider: "", cost: "pass"}},
		},
		{
			name:         "unclaimed provider record is removed and the default cleared",
			pool:         []domain.Model{{ID: "p/old", Source: SourceProvider, Provider: "Alpha", Cost: "pass"}},
			defaultModel: "p/old",
			claims:       nil,
			wantIDs:      nil,
			wantDefault:  "",
			wantChanged:  true,
		},
		{
			name:        "user state on an existing provider record survives re-projection",
			pool:        []domain.Model{{ID: "p/u", Source: SourceProvider, Provider: "Old", Cost: "pass", Disabled: true, Context: 2048, MetaLocked: true}},
			claims:      []ProviderClaim{{ID: "p/u", Provider: "Alpha", Cost: "pass"}},
			wantIDs:     []string{"p/u"},
			want:        map[string]want{"p/u": {provider: "Alpha", cost: "pass", disabled: true, context: 2048}},
			wantChanged: true,
		},
		{
			name:        "undecided record follows the provider-wide cost",
			pool:        []domain.Model{{ID: "p/c", Source: SourceProvider, Provider: "Alpha", Cost: "free"}},
			claims:      []ProviderClaim{{ID: "p/c", Provider: "Alpha", Cost: "pass"}},
			wantIDs:     []string{"p/c"},
			want:        map[string]want{"p/c": {provider: "Alpha", cost: "pass"}},
			wantChanged: true,
		},
		{
			name:    "a per-model decision is not overwritten by the provider-wide cost",
			pool:    []domain.Model{{ID: "p/m", Source: SourceProvider, Provider: "Alpha", Cost: "free", TierSignal: "manual"}},
			claims:  []ProviderClaim{{ID: "p/m", Provider: "Alpha", Cost: "pass"}},
			wantIDs: []string{"p/m"},
			want:    map[string]want{"p/m": {provider: "Alpha", cost: "free", signal: "manual"}},
		},
		{
			name:        "id-only free signal upgrades an untiered record",
			pool:        []domain.Model{{ID: "p/f:free", Source: SourceProvider, Provider: "Alpha", Cost: "pass"}},
			claims:      []ProviderClaim{{ID: "p/f:free", Provider: "Alpha", Cost: "pass", TierSignal: "suffix-free"}},
			wantIDs:     []string{"p/f:free"},
			want:        map[string]want{"p/f:free": {provider: "Alpha", cost: "free", signal: "suffix-free"}},
			wantChanged: true,
		},
		{
			name:    "custom records are never touched by projection",
			pool:    []domain.Model{{ID: "c/one", Custom: true, Cost: "pass"}},
			claims:  nil,
			wantIDs: []string{"c/one"},
			want:    map[string]want{"c/one": {cost: "pass"}},
		},
		{
			name:    "delisted provider record that is still claimed keeps its flag",
			pool:    []domain.Model{{ID: "p/d", Source: SourceProvider, Provider: "Alpha", Cost: "pass", Delisted: true}},
			claims:  []ProviderClaim{{ID: "p/d", Provider: "Alpha", Cost: "pass"}},
			wantIDs: []string{"p/d"},
			want:    map[string]want{"p/d": {provider: "Alpha", cost: "pass", delisted: true}},
		},
		{
			name:         "default model that is still listed is kept",
			pool:         []domain.Model{{ID: "p/keep", Source: SourceProvider, Provider: "Alpha", Cost: "pass"}},
			defaultModel: "p/keep",
			claims:       []ProviderClaim{{ID: "p/keep", Provider: "Alpha", Cost: "pass"}, {ID: "p/new", Provider: "Alpha", Cost: "pass"}},
			wantIDs:      []string{"p/keep", "p/new"},
			wantDefault:  "p/keep",
			wantChanged:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := &domain.AccountPool{Models: tc.pool, DefaultModel: tc.defaultModel}
			withPool(t, pool)
			changed := ProjectProvider(tc.claims)
			if changed != tc.wantChanged {
				t.Errorf("changed = %v, want %v", changed, tc.wantChanged)
			}
			got := livePool()
			var ids []string
			for _, m := range got.Models {
				ids = append(ids, m.ID)
			}
			if !reflect.DeepEqual(ids, tc.wantIDs) {
				t.Fatalf("ids = %v, want %v", ids, tc.wantIDs)
			}
			for id, w := range tc.want {
				m := findOne(t, got.Models, id)
				if w.provider != "" && m.Provider != w.provider {
					t.Errorf("%s provider = %q, want %q", id, m.Provider, w.provider)
				}
				if m.Cost != w.cost {
					t.Errorf("%s cost = %q, want %q", id, m.Cost, w.cost)
				}
				if w.signal != "" && m.TierSignal != w.signal {
					t.Errorf("%s signal = %q, want %q", id, m.TierSignal, w.signal)
				}
				if m.Disabled != w.disabled || m.Delisted != w.delisted {
					t.Errorf("%s disabled=%v delisted=%v, want %v/%v", id, m.Disabled, m.Delisted, w.disabled, w.delisted)
				}
				if w.context != 0 && m.Context != w.context {
					t.Errorf("%s context = %d, want %d", id, m.Context, w.context)
				}
			}
			if got.DefaultModel != tc.wantDefault {
				t.Errorf("default = %q, want %q", got.DefaultModel, tc.wantDefault)
			}
		})
	}
}

func findOne(t *testing.T, list []domain.Model, id string) domain.Model {
	t.Helper()
	for _, m := range list {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("record %q missing", id)
	return domain.Model{}
}

// Table: delisting. Declared provider records missing upstream are marked after
// consecutive misses (toMark); marks clear when the id returns upstream.
func TestApplyDelistingReconciliation(t *testing.T) {
	declared := map[string]bool{"p/gone": true, "p/back": true, "p/stay": true}
	cases := []struct {
		name         string
		pool         []domain.Model
		toMark       []string
		upstream     map[string]bool
		wantMarked   int
		wantAlready  int
		wantDelisted map[string]bool
	}{
		{
			name:         "missing id is marked once",
			pool:         []domain.Model{{ID: "p/gone", Source: SourceProvider}, {ID: "p/stay", Source: SourceProvider}},
			toMark:       []string{"p/gone"},
			upstream:     map[string]bool{"p/stay": true},
			wantMarked:   1,
			wantAlready:  1,
			wantDelisted: map[string]bool{"p/gone": true, "p/stay": false},
		},
		{
			name:         "returning id clears the mark",
			pool:         []domain.Model{{ID: "p/back", Source: SourceProvider, Delisted: true}},
			upstream:     map[string]bool{"p/back": true},
			wantMarked:   0,
			wantAlready:  0,
			wantDelisted: map[string]bool{"p/back": false},
		},
		{
			name:         "undeclared and non-provider records are not touched",
			pool:         []domain.Model{{ID: "other", Source: SourceProvider}, {ID: "p/gone", Source: SourceRemote}},
			toMark:       []string{"other", "p/gone"},
			wantMarked:   0,
			wantAlready:  0,
			wantDelisted: map[string]bool{"other": false, "p/gone": false},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withPool(t, &domain.AccountPool{Models: tc.pool})
			marked, already := ApplyDelisting(declared, tc.toMark, tc.upstream)
			if marked != tc.wantMarked || already != tc.wantAlready {
				t.Errorf("marked=%d already=%d, want %d/%d", marked, already, tc.wantMarked, tc.wantAlready)
			}
			for _, m := range livePool().Models {
				if want, ok := tc.wantDelisted[m.ID]; ok && m.Delisted != want {
					t.Errorf("%s delisted = %v, want %v", m.ID, m.Delisted, want)
				}
			}
		})
	}
}

// Table: import decisions. Existing ids are updated, unknown ids are appended.
func TestApplyDecisionsRules(t *testing.T) {
	withPool(t, &domain.AccountPool{Models: []domain.Model{
		{ID: "x/one", Source: SourceRemote, Cost: "pass"},
	}})
	added, changed := ApplyDecisions([]Decision{
		{ID: "x/one", Cost: "free", Signal: "pricing-free"},
		{ID: "x/new", Cost: "pass", Signal: "pricing-paid"},
	}, "Alpha", 1700000000)
	if !reflect.DeepEqual(added, []string{"x/new"}) || changed != 1 {
		t.Fatalf("added=%v changed=%d", added, changed)
	}
	p := livePool()
	one := findOne(t, p.Models, "x/one")
	if one.Cost != "free" || one.TierSignal != "pricing-free" || one.TierDecidedAt != 1700000000 || one.Source != SourceRemote {
		t.Errorf("existing record: %+v", one)
	}
	nw := findOne(t, p.Models, "x/new")
	if nw.Source != SourceProvider || nw.Provider != "Alpha" || nw.Status != "active" || nw.TierDecidedAt != 1700000000 {
		t.Errorf("new record: %+v", nw)
	}
}

// Table: manual tiers. An empty cost clears the marker and keeps the cost.
func TestSetManualCostRules(t *testing.T) {
	withPool(t, &domain.AccountPool{Models: []domain.Model{
		{ID: "m/one", Cost: "pass", TierSignal: "pricing-paid", TierDecidedAt: 5},
	}})
	if !SetManualCost("m/one", "free", 100) {
		t.Fatal("not found")
	}
	if m := livePool().Models[0]; m.Cost != "free" || m.TierSignal != "manual" || m.TierDecidedAt != 100 {
		t.Fatalf("manual set: %+v", m)
	}
	if !SetManualCost("m/one", "", 200) {
		t.Fatal("not found")
	}
	if m := livePool().Models[0]; m.Cost != "free" || m.TierSignal != "" || m.TierDecidedAt != 0 {
		t.Fatalf("manual clear must keep cost: %+v", m)
	}
	if SetManualCost("missing", "free", 1) {
		t.Fatal("missing id must report not found")
	}
}

func TestProjectProviderSharedClaimDoesNotRewriteExistingCompatibilityRow(t *testing.T) {
	withPool(t, &domain.AccountPool{Models: []domain.Model{{
		ID: "shared/model", Provider: "Alpha", Source: SourceProvider,
		Cost: CostPass, Status: "active",
	}}})
	if ProjectProvider([]ProviderClaim{{
		ID: "shared/model", Provider: "Beta", Cost: CostFree,
		TierSignal: "pricing-zero", Shared: true,
	}}) {
		t.Fatal("shared provider claim unexpectedly rewrote the compatibility row")
	}
	got := Snapshot()[0]
	if got.Provider != "Alpha" || got.Cost != CostPass || got.TierSignal != "" || got.TierDecidedAt != 0 {
		t.Fatalf("shared compatibility row changed: %+v", got)
	}
}
