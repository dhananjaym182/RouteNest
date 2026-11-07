package syncstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"routenest/internal/domain"
)

func TestLoadMissingFile(t *testing.T) {
	states, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil || states != nil {
		t.Fatalf("states/err = %v/%v", states, err)
	}
}

func TestLoadCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte("{bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestLoadRejectsTopLevelNull(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte("null"), 0600); err != nil {
		t.Fatal(err)
	}
	if states, err := Load(path); err == nil || states != nil {
		t.Fatalf("top-level null must fail, got states=%v err=%v", states, err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	in := map[string]State{
		"p1": {LastCheckedMs: 1700000000000, LastError: "timeout", DelistedMarked: 2, MissCounting: 1, NewQueued: 3, BatchID: "b1"},
		"p2": {LastCheckedMs: 1700000001000},
	}
	if err := Save(path, in); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(out) != 2 || out["p1"] == nil {
		t.Fatalf("out = %+v", out)
	}
	if out["p1"].LastError != "timeout" || out["p1"].DelistedMarked != 2 || out["p1"].BatchID != "b1" {
		t.Errorf("fields lost: %+v", out["p1"])
	}
	if out["p2"].LastCheckedMs != 1700000001000 {
		t.Errorf("p2 = %+v", out["p2"])
	}
}

func TestSaveLoadPreservesAuthoritativeEmptyCollections(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	empty := []string{}
	in := map[string]State{
		"p1": {
			DelistedModels: empty,
			Offerings: map[string]domain.ModelOfferingState{
				"shared/model": {ThinkingLevels: &empty},
			},
		},
	}
	if err := Save(path, in); err != nil {
		t.Fatalf("save: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var encoded map[string]map[string]json.RawMessage
	if err := json.Unmarshal(raw, &encoded); err != nil {
		t.Fatalf("decode raw state: %v", err)
	}
	if got := string(encoded["p1"]["delistedModels"]); got != "[]" {
		t.Fatalf("delistedModels encoded as %s, want []", got)
	}

	out, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if out["p1"].DelistedModels == nil || len(out["p1"].DelistedModels) != 0 {
		t.Fatalf("delistedModels = %#v, want non-nil empty slice", out["p1"].DelistedModels)
	}
	levels := out["p1"].Offerings["shared/model"].ThinkingLevels
	if levels == nil || *levels == nil || len(*levels) != 0 {
		t.Fatalf("thinkingLevels = %#v, want pointer to non-nil empty slice", levels)
	}
}
