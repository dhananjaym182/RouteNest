package models

import (
	"errors"
	"reflect"
	"testing"

	"routenest/internal/domain"
)

// Table: replacing a synchronized source (Cline remote list). The build function
// returns the desired records; the table checks what the pool holds afterwards.
func TestReplaceSourceReconciliation(t *testing.T) {
	cases := []struct {
		name        string
		pool        []domain.Model
		desired     []domain.Model
		wantErr     error
		wantAdded   []string
		wantRemoved []string
		wantDelist  map[string]bool // id -> delisted after the update
		wantDisable map[string]bool // id -> disabled after the update
		wantTotal   int
	}{
		{
			name:       "new upstream id is added",
			pool:       []domain.Model{{ID: "r/a", Source: SourceRemote}},
			desired:    []domain.Model{{ID: "r/a", Source: SourceRemote}, {ID: "r/b", Source: SourceRemote}},
			wantAdded:  []string{"r/b"},
			wantDelist: map[string]bool{"r/a": false, "r/b": false},
			wantTotal:  2,
		},
		{
			name:        "id missing upstream is kept and marked delisted, not removed",
			pool:        []domain.Model{{ID: "r/a", Source: SourceRemote}, {ID: "r/gone", Source: SourceRemote}},
			desired:     []domain.Model{{ID: "r/a", Source: SourceRemote}},
			wantRemoved: []string{"r/gone"},
			wantDelist:  map[string]bool{"r/gone": true, "r/a": false},
			wantTotal:   1,
		},
		{
			name:       "already delisted id is not reported as removed again",
			pool:       []domain.Model{{ID: "r/gone", Source: SourceRemote, Delisted: true}, {ID: "r/a", Source: SourceRemote}},
			desired:    []domain.Model{{ID: "r/a", Source: SourceRemote}},
			wantDelist: map[string]bool{"r/gone": true},
			wantTotal:  1,
		},
		{
			name:       "returning id is rebuilt unmarked",
			pool:       []domain.Model{{ID: "r/back", Source: SourceRemote, Delisted: true}},
			desired:    []domain.Model{{ID: "r/back", Source: SourceRemote}},
			wantDelist: map[string]bool{"r/back": false},
			wantTotal:  1,
		},
		{
			name:        "disabled flag on a delisted record survives",
			pool:        []domain.Model{{ID: "r/off", Source: SourceRemote, Disabled: true}},
			desired:     []domain.Model{{ID: "r/a", Source: SourceRemote}},
			wantAdded:   []string{"r/a"},
			wantRemoved: []string{"r/off"},
			wantDelist:  map[string]bool{"r/off": true},
			wantDisable: map[string]bool{"r/off": true},
			wantTotal:   1,
		},
		{
			name: "records of other sources are never touched",
			pool: []domain.Model{
				{ID: "z/one", Source: SourceZen},
				{ID: "p/one", Source: SourceProvider},
				{ID: "c/one", Custom: true},
			},
			desired:   []domain.Model{{ID: "r/a", Source: SourceRemote}},
			wantAdded: []string{"r/a"},
			wantTotal: 1,
		},
		{
			name:    "empty upstream list writes nothing",
			pool:    []domain.Model{{ID: "r/a", Source: SourceRemote}},
			desired: nil,
			wantErr: ErrEmptySource,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withPool(t, &domain.AccountPool{Models: tc.pool})
			before := livePool().Models
			update, err := ReplaceSource(SourceRemote, func(current []domain.Model) []domain.Model {
				return tc.desired
			})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				if !reflect.DeepEqual(livePool().Models, before) {
					t.Fatal("a failed replace must not change the pool")
				}
				return
			}
			if !reflect.DeepEqual(update.Added, tc.wantAdded) || !reflect.DeepEqual(update.Removed, tc.wantRemoved) {
				t.Errorf("added=%v removed=%v, want %v / %v", update.Added, update.Removed, tc.wantAdded, tc.wantRemoved)
			}
			if update.Total != tc.wantTotal {
				t.Errorf("total = %d, want %d", update.Total, tc.wantTotal)
			}
			byID := map[string]domain.Model{}
			for _, m := range livePool().Models {
				byID[m.ID] = m
			}
			for id, want := range tc.wantDelist {
				if byID[id].Delisted != want {
					t.Errorf("%s delisted = %v, want %v", id, byID[id].Delisted, want)
				}
			}
			for id, want := range tc.wantDisable {
				if byID[id].Disabled != want {
					t.Errorf("%s disabled = %v, want %v", id, byID[id].Disabled, want)
				}
			}
			for _, pre := range before {
				if pre.Source != SourceRemote {
					if got, ok := byID[pre.ID]; !ok || !reflect.DeepEqual(got, pre) {
						t.Errorf("record of another source changed: %+v", pre)
					}
				}
			}
		})
	}
}

// Table: removing a delisted record (upstream confirmed the model is gone).
func TestRemoveDelistedRules(t *testing.T) {
	cases := []struct {
		name        string
		pool        []domain.Model
		default_    string
		id          string
		wantRemoved bool
		wantDefault string
	}{
		{name: "delisted synced record is removed", pool: []domain.Model{{ID: "g", Source: SourceRemote, Delisted: true}}, id: "g", wantRemoved: true},
		{name: "live synced record is protected", pool: []domain.Model{{ID: "l", Source: SourceRemote}}, id: "l"},
		{name: "delisted custom record is protected", pool: []domain.Model{{ID: "c", Custom: true, Delisted: true}}, id: "c"},
		{name: "removing the default clears it", pool: []domain.Model{{ID: "g", Source: SourceRemote, Delisted: true}}, default_: "g", id: "g", wantRemoved: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withPool(t, &domain.AccountPool{Models: tc.pool, DefaultModel: tc.default_})
			if got := RemoveDelisted(tc.id); got != tc.wantRemoved {
				t.Fatalf("removed = %v, want %v", got, tc.wantRemoved)
			}
			if tc.wantRemoved && livePool().DefaultModel != tc.wantDefault {
				t.Fatalf("default = %q, want %q", livePool().DefaultModel, tc.wantDefault)
			}
		})
	}
}
