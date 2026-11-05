package providerreg

import (
	"reflect"
	"strings"
	"testing"

	"routenest/internal/deviceflow"
)

// Global provider plan counts: 111 definitions = 21 manual + 90 imported.
func TestCatalogueCountsByProvenance(t *testing.T) {
	reg := Builtin()
	counts := map[string]int{}
	for _, p := range reg.All() {
		counts[p.Provenance]++
	}
	if reg.Len() != 111 || counts[ProvenanceManual] != 21 || counts[ProvenanceImported] != 90 {
		t.Fatalf("total=%d manual=%d imported=%d, want 111/21/90", reg.Len(), counts[ProvenanceManual], counts[ProvenanceImported])
	}
}

func TestGlobalBuiltInDefinitions(t *testing.T) {
	reg := Builtin()
	tests := []struct {
		id        string
		adapter   string
		protocol  string
		authFlows []string
	}{
		{id: "cline", adapter: "cline", protocol: "cline", authFlows: []string{"cline-workos", "cline-refresh-token"}},
		{id: "opencode", adapter: "opencodezen", protocol: "opencode-zen", authFlows: []string{"opencode-public", "opencode-session"}},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			p, ok := reg.Get(tt.id)
			if !ok {
				t.Fatalf("missing built-in provider definition %q", tt.id)
			}
			if !p.RuntimeBuiltin || p.AdapterID != tt.adapter || p.Protocol != tt.protocol {
				t.Fatalf("definition = %+v, want runtime built-in adapter=%q protocol=%q", p, tt.adapter, tt.protocol)
			}
			if !reflect.DeepEqual(p.AuthFlows, tt.authFlows) {
				t.Errorf("auth flows = %v, want %v", p.AuthFlows, tt.authFlows)
			}
			if p.MissingThreshold != 1 {
				t.Errorf("missing threshold = %d, want compatibility value 1", p.MissingThreshold)
			}
			for _, capability := range []string{
				CapabilityModelDiscovery,
				CapabilityFreeModelDetection,
				CapabilityRemovedModelDetection,
				CapabilityReactiveQuota,
				CapabilityQuotaResetDetection,
				CapabilityCredentialRotation,
				CapabilityEgressPool,
			} {
				if p.Capabilities[capability] != CapabilitySupported {
					t.Errorf("capability %s = %q, want supported", capability, p.Capabilities[capability])
				}
			}
		})
	}

	if got := reg.ByAdapter("cline"); !reflect.DeepEqual(got, []string{"cline"}) {
		t.Errorf("ByAdapter(cline) = %v", got)
	}
	if got := reg.ByDeclaredAuthFlow("opencode-session"); !reflect.DeepEqual(got, []string{"opencode"}) {
		t.Errorf("ByDeclaredAuthFlow(opencode-session) = %v", got)
	}
}

func TestProviderDefinitionAccessorsReturnDeepCopies(t *testing.T) {
	reg := Builtin()
	p, ok := reg.Get("cline")
	if !ok {
		t.Fatal("missing cline definition")
	}
	p.AuthFlows[0] = "mutated"
	p.Capabilities[CapabilityReactiveQuota] = CapabilityUnsupported

	again, _ := reg.Get("cline")
	if again.AuthFlows[0] != "cline-workos" {
		t.Errorf("AuthFlows aliased: %v", again.AuthFlows)
	}
	if again.Capabilities[CapabilityReactiveQuota] != CapabilitySupported {
		t.Errorf("Capabilities aliased: %v", again.Capabilities)
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

// AuthFlowLabels gives the UI the canonical display name for each flow, so the
// console does not keep a parallel hardcoded table.
func TestAuthFlowLabels(t *testing.T) {
	labels := Builtin().AuthFlowLabels()
	want := map[string]string{"github": "GitHub Copilot (Free plan)", "qwen": "Qwen Chat (Free)"}
	for flow, name := range want {
		if labels[flow] != name {
			t.Errorf("label[%s] = %q, want %q", flow, labels[flow], name)
		}
	}
	for _, flow := range ConnectionFlowKeys() {
		if labels[flow] == "" {
			t.Errorf("flow %q has no catalogue label", flow)
		}
	}
}

// ConnectionFlowKeys returns every device-flow spec ID, mirroring the admin
// connections endpoint's flow list.
func ConnectionFlowKeys() []string {
	specs := deviceflow.Specs()
	out := make([]string, 0, len(specs))
	for flow := range specs {
		out = append(out, flow)
	}
	return out
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

	badAdapter := base
	badAdapter.AdapterID = "missing-adapter"
	if err := Validate(map[string]Preset{"x": badAdapter}); err == nil || !strings.Contains(err.Error(), "unsupported adapter") {
		t.Errorf("unknown adapter: err = %v", err)
	}

	badFlow := base
	badFlow.AuthFlows = []string{"missing-flow"}
	if err := Validate(map[string]Preset{"x": badFlow}); err == nil || !strings.Contains(err.Error(), "unknown auth flow") {
		t.Errorf("unknown declared auth flow: err = %v", err)
	}

	badCapability := base
	badCapability.Capabilities = map[string]CapabilityState{CapabilityReactiveQuota: "enabled"}
	if err := Validate(map[string]Preset{"x": badCapability}); err == nil || !strings.Contains(err.Error(), "invalid capability state") {
		t.Errorf("invalid capability state: err = %v", err)
	}

	badThreshold := base
	badThreshold.MissingThreshold = -1
	if err := Validate(map[string]Preset{"x": badThreshold}); err == nil || !strings.Contains(err.Error(), "missingThreshold") {
		t.Errorf("invalid missing threshold: err = %v", err)
	}
}

func TestMergeRejectsDuplicateAcrossSources(t *testing.T) {
	manual := map[string]Preset{"same": {ID: "same", Provenance: ProvenanceManual}}
	imported := map[string]Preset{"same": {ID: "same", Provenance: ProvenanceImported}}
	if _, err := mergeCatalogues(manual, imported); err == nil {
		t.Fatal("expected duplicate across manual and imported to fail")
	}
}
