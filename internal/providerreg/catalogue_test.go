package providerreg

import (
	"reflect"
	"strings"
	"testing"

	"routenest/internal/deviceflow"
)

// Plan Phase 2 counts: 109 presets = 19 manual + 90 imported.
func TestCatalogueCountsByProvenance(t *testing.T) {
	reg := Builtin()
	counts := map[string]int{}
	for _, p := range reg.All() {
		counts[p.Provenance]++
	}
	if reg.Len() != 109 || counts[ProvenanceManual] != 19 || counts[ProvenanceImported] != 90 {
		t.Fatalf("total=%d manual=%d imported=%d, want 109/19/90", reg.Len(), counts[ProvenanceManual], counts[ProvenanceImported])
	}
}

// Every login preset names a device-flow spec, and every spec is used by exactly
// one preset. The two renamed links are pinned explicitly.
func TestAuthFlowCrossReferences(t *testing.T) {
	reg := Builtin()
	specs := deviceflow.Specs()
	usedBy := map[string][]string{}
	for _, id := range reg.IDs() {
		p, _ := reg.Get(id)
		if p.LoginFlow != (p.AuthFlow != "") {
			t.Errorf("%s: loginFlow %v disagrees with authFlow %q", id, p.LoginFlow, p.AuthFlow)
		}
		if p.AuthFlow == "" {
			continue
		}
		if _, ok := specs[p.AuthFlow]; !ok {
			t.Errorf("%s: authFlow %q has no device-flow spec", id, p.AuthFlow)
		}
		usedBy[p.AuthFlow] = append(usedBy[p.AuthFlow], id)
	}
	for flow := range specs {
		if got := usedBy[flow]; len(got) != 1 {
			t.Errorf("device-flow spec %q used by presets %v, want exactly one", flow, got)
		}
	}
	for preset, flow := range map[string]string{"github-copilot": "github", "qwen-chat": "qwen"} {
		p, _ := reg.Get(preset)
		if p.AuthFlow != flow {
			t.Errorf("%s: authFlow = %q, want %q", preset, p.AuthFlow, flow)
		}
	}
	if got := reg.ByAuthFlow("github"); !reflect.DeepEqual(got, []string{"github-copilot"}) {
		t.Errorf("ByAuthFlow(github) = %v", got)
	}
}

func TestProtocolIndexResolvesQoder(t *testing.T) {
	reg := Builtin()
	for _, id := range []string{"qoder", "qoder-cn"} {
		p, ok := reg.Get(id)
		if !ok {
			t.Fatalf("missing %s", id)
		}
		if got := reg.ProtocolForBaseURL(p.BaseURL); got != "qoder" {
			t.Errorf("ProtocolForBaseURL(%s) = %q, want qoder", id, got)
		}
		if got := reg.ProtocolForBaseURL(p.BaseURL + "/chat/completions/"); got != "qoder" {
			t.Errorf("normalized lookup for %s = %q, want qoder", id, got)
		}
	}
	if got := reg.ProtocolForBaseURL("https://openrouter.ai/api/v1"); got != "" {
		t.Errorf("plain OpenAI preset reported protocol %q", got)
	}
	if got := reg.ProtocolForBaseURL("https://unknown.example/v1"); got != "" {
		t.Errorf("unknown URL reported protocol %q", got)
	}
	if !reflect.DeepEqual(reg.ByProtocol("qoder"), []string{"qoder", "qoder-cn"}) {
		t.Errorf("ByProtocol(qoder) = %v", reg.ByProtocol("qoder"))
	}
	if n := len(reg.ByProtocol(DefaultProtocol)); n == 0 {
		t.Error("default protocol index is empty")
	}
}

func TestBaseURLIndexReturnsSharedPresets(t *testing.T) {
	// Two presets share a base URL and no protocol; the index lists both.
	got := Builtin().ByBaseURL("https://api.naga.ac/v1")
	if !reflect.DeepEqual(got, []string{"naga-ac", "naga-ai"}) {
		t.Fatalf("ByBaseURL shared = %v", got)
	}
}

func TestIndexAccessorsReturnCopies(t *testing.T) {
	reg := Builtin()
	ids := reg.ByAuthFlow("github")
	ids[0] = "mutated"
	if reg.ByAuthFlow("github")[0] != "github-copilot" {
		t.Error("ByAuthFlow must return a copy")
	}
	bases := reg.ByBaseURL("https://api.naga.ac/v1")
	bases[0] = "mutated"
	if reg.ByBaseURL("https://api.naga.ac/v1")[0] != "naga-ac" {
		t.Error("ByBaseURL must return a copy")
	}
}

func TestNewRejectsConflictingProtocolOnSharedBaseURL(t *testing.T) {
	_, err := New(map[string]Preset{
		"a": {ID: "a", Name: "A", BaseURL: "https://example.test/v1"},
		"b": {ID: "b", Name: "B", BaseURL: "https://example.test/v1/", Protocol: "qoder"},
	})
	if err == nil || !strings.Contains(err.Error(), "different protocols") {
		t.Fatalf("err = %v, want protocol conflict", err)
	}
}

func TestValidateCrossReferences(t *testing.T) {
	base := Preset{ID: "x", Name: "X", BaseURL: "https://example.test/v1"}

	unknown := base
	unknown.AuthFlow = "no-such-flow"
	unknown.LoginFlow = true
	if err := Validate(map[string]Preset{"x": unknown}); err == nil || !strings.Contains(err.Error(), "no device-flow spec") {
		t.Errorf("unknown authFlow: err = %v", err)
	}

	mismatch := base
	mismatch.AuthFlow = "github"
	mismatch.LoginFlow = false
	if err := Validate(map[string]Preset{"x": mismatch}); err == nil || !strings.Contains(err.Error(), "loginFlow must be") {
		t.Errorf("loginFlow/authFlow mismatch: err = %v", err)
	}

	ok := base
	if err := Validate(map[string]Preset{"x": ok}); err != nil {
		t.Errorf("plain preset rejected: %v", err)
	}
}

func TestMergeRejectsDuplicateAcrossSources(t *testing.T) {
	manual := map[string]Preset{"same": {ID: "same", Provenance: ProvenanceManual}}
	imported := map[string]Preset{"same": {ID: "same", Provenance: ProvenanceImported}}
	if _, err := mergeCatalogues(manual, imported); err == nil {
		t.Fatal("expected duplicate across manual and imported to fail")
	}
}
