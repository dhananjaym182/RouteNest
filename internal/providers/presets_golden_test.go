package providers

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

var updatePresetsGolden = flag.Bool("update-presets-golden", false, "rewrite testdata/presets_golden.json from the current output")

// TestPresetsEndpointGolden freezes the public /admin/api/providers/presets
// response. Any change to a preset's visible fields must be deliberate: rerun
// with -update-presets-golden and review the diff.
func TestPresetsEndpointGolden(t *testing.T) {
	rec := httptest.NewRecorder()
	HandlePresets(rec, httptest.NewRequest(http.MethodGet, "/admin/api/providers/presets", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, rec.Body.Bytes(), "", "  "); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	got := append(pretty.Bytes(), '\n')

	path := filepath.Join("testdata", "presets_golden.json")
	if *updatePresetsGolden {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update-presets-golden to create): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("presets response differs from %s; review and rerun with -update-presets-golden if intended", path)
	}
}

// TestPresetsCatalogueCounts pins the catalogue sizes documented in README.md.
func TestPresetsCatalogueCounts(t *testing.T) {
	rec := httptest.NewRecorder()
	HandlePresets(rec, httptest.NewRequest(http.MethodGet, "/admin/api/providers/presets", nil))

	var env struct {
		Data struct {
			Presets []struct {
				Key       string `json:"key"`
				FreeTier  bool   `json:"freeTier"`
				LoginFlow bool   `json:"loginFlow"`
			} `json:"presets"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}

	total, freeTier, login := len(env.Data.Presets), 0, 0
	seen := map[string]bool{}
	for _, p := range env.Data.Presets {
		if seen[p.Key] {
			t.Errorf("duplicate preset key %q", p.Key)
		}
		seen[p.Key] = true
		if p.FreeTier {
			freeTier++
		}
		if p.LoginFlow {
			login++
		}
	}
	if total != 109 {
		t.Errorf("preset total = %d, want 109", total)
	}
	if freeTier != 34 {
		t.Errorf("freeTier presets = %d, want 34", freeTier)
	}
	if login != 9 {
		t.Errorf("loginFlow presets = %d, want 9", login)
	}
}
