package models

import (
	"errors"
	"reflect"
	"testing"
)

func TestReconcilePresenceThresholdAndRecovery(t *testing.T) {
	current := map[string]PresenceState{
		"a":    {},
		"gone": {},
		"back": {Delisted: true},
	}
	misses := map[string]int{}
	policy := ReconcilePolicy{ProviderID: "provider-a", MissingThreshold: 2, Authoritative: true}

	first, err := ReconcilePresence([]string{"a", "gone", "back"}, map[string]bool{"a": true, "back": true}, current, misses, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.ToDelist) != 0 || !reflect.DeepEqual(first.ToRestore, []string{"back"}) || first.MissingCounting != 1 {
		t.Fatalf("first result = %+v", first)
	}
	if misses["gone"] != 1 {
		t.Fatalf("misses after first = %v", misses)
	}

	second, err := ReconcilePresence([]string{"a", "gone", "back"}, map[string]bool{"a": true, "back": true}, current, misses, policy)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second.ToDelist, []string{"gone"}) || second.MissingCounting != 0 {
		t.Fatalf("second result = %+v", second)
	}

	third, err := ReconcilePresence([]string{"a", "gone", "back"}, map[string]bool{"a": true, "gone": true, "back": true}, map[string]PresenceState{"gone": {Delisted: true}}, misses, policy)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(third.ToRestore, []string{"gone"}) || len(misses) != 0 {
		t.Fatalf("recovery result = %+v misses=%v", third, misses)
	}
}

func TestReconcilePresenceRejectsUnsafeSnapshotsWithoutMutatingState(t *testing.T) {
	baseMisses := map[string]int{"gone": 1, "undeclared": 7}
	tests := []struct {
		name       string
		discovered map[string]bool
		policy     ReconcilePolicy
		wantErr    error
	}{
		{name: "empty snapshot", discovered: map[string]bool{}, policy: ReconcilePolicy{Authoritative: true}, wantErr: ErrEmptySource},
		{name: "non authoritative", discovered: map[string]bool{"a": true}, policy: ReconcilePolicy{}, wantErr: ErrNonAuthoritative},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			misses := map[string]int{"gone": 1, "undeclared": 7}
			_, err := ReconcilePresence([]string{"a", "gone"}, tt.discovered, nil, misses, tt.policy)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(misses, baseMisses) {
				t.Fatalf("miss counters changed on rejected snapshot: %v", misses)
			}
		})
	}
}

func TestReconcilePresenceProtectsManualRecordsAndFindsNewModels(t *testing.T) {
	current := map[string]PresenceState{
		"manual": {Protected: true},
		"live":   {},
	}
	misses := map[string]int{"stale": 4}
	result, err := ReconcilePresence(
		[]string{"manual", "live"},
		map[string]bool{"live": true, "new-b": true, "new-a": true},
		current,
		misses,
		ReconcilePolicy{ProviderID: "p", MissingThreshold: 1, Authoritative: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ToDelist) != 0 {
		t.Fatalf("protected record was delisted: %+v", result)
	}
	if !reflect.DeepEqual(result.New, []string{"new-a", "new-b"}) {
		t.Fatalf("new models = %v", result.New)
	}
	if len(misses) != 0 {
		t.Fatalf("stale/protected counters not pruned: %v", misses)
	}
}
