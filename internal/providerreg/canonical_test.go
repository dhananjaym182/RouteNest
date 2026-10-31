package providerreg

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateCanonical = flag.Bool("update-canonical-golden", false, "rewrite testdata/canonical_presets.json")

// canonicalPresets returns every built-in preset, sorted by ID, as indented JSON.
// It serializes all Preset fields, including Protocol, which the admin golden does not show.
func canonicalPresets(t *testing.T) []byte {
	t.Helper()
	reg := Builtin()
	list := make([]Preset, 0, reg.Len())
	for _, id := range reg.IDs() {
		p, _ := reg.Get(id)
		list = append(list, p)
	}
	out, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(out, '\n')
}

// TestCanonicalPresetsGolden freezes the full preset data as seen by callers.
// Regenerate with -update-canonical-golden only for a reviewed data change.
func TestCanonicalPresetsGolden(t *testing.T) {
	got := canonicalPresets(t)
	path := filepath.Join("testdata", "canonical_presets.json")
	if *updateCanonical {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update-canonical-golden): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("canonical presets differ from %s", path)
	}
}
