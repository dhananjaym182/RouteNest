package adminapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	configpkg "routenest/internal/config"
)

func TestConfigProviderModelPrefixesRoundTrip(t *testing.T) {
	h, fixture := newConfigHandler()
	bindings := map[string]string{
		"cl": "cline", "op": "opencode", "as": "antseed_primary", "old-as": "retired-antseed",
	}
	fixture.cfg.ProviderModelPrefixes = bindings
	want := map[string]any{
		"cl": "cline", "op": "opencode", "as": "antseed_primary", "old-as": "retired-antseed",
	}
	assertBindings := func(recorder *httptest.ResponseRecorder) {
		t.Helper()
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
		}
		data := decodeEnvelope(t, recorder).Data.(map[string]any)
		if got := data["providerModelPrefixes"]; !reflect.DeepEqual(got, want) {
			t.Fatalf("providerModelPrefixes = %#v, want %#v", got, want)
		}
	}

	get := httptest.NewRecorder()
	h.Get(get, httptest.NewRequest(http.MethodGet, "/admin/api/config", nil))
	assertBindings(get)

	update := httptest.NewRecorder()
	h.Update(update, postJSON("/admin/api/config/update", `{
		"modelAliases":[{"alias":"team/fast","target":"as/deepseek-4-1"}],
		"providerModelPrefixes":{"as":"opencode"}
	}`))
	assertBindings(update)
	if !reflect.DeepEqual(fixture.cfg.ProviderModelPrefixes, bindings) {
		t.Fatalf("client reassigned provider namespaces: %v", fixture.cfg.ProviderModelPrefixes)
	}
	if len(fixture.cfg.ModelAliases) != 1 || fixture.cfg.ModelAliases[0].Target != "as/deepseek-4-1" {
		t.Fatalf("valid alias to a qualified provider model was lost: %+v", fixture.cfg.ModelAliases)
	}

	get = httptest.NewRecorder()
	h.Get(get, httptest.NewRequest(http.MethodGet, "/admin/api/config", nil))
	assertBindings(get)
}

func TestConfigUpdateRejectsProviderModelNamespaceAliases(t *testing.T) {
	reserved := map[string]string{"cl": "cline", "op": "opencode", "as": "antseed_primary", "old-as": "retired-antseed"}
	for _, tc := range []struct {
		name     string
		alias    string
		bindings map[string]string
	}{
		{name: "Cline", alias: "cl/deepseek-4-1", bindings: reserved},
		{name: "OpenCode", alias: "op/deepseek-4-1", bindings: reserved},
		{name: "AntSeed", alias: "as/deepseek-4-1", bindings: reserved},
		{name: "trimmed AntSeed", alias: " as/deepseek-4-1 ", bindings: reserved},
		{name: "retired provider", alias: "old-as/deepseek-4-1", bindings: reserved},
		{name: "legacy Cline", alias: "cl/deepseek-4-1"},
		{name: "legacy OpenCode", alias: "op/deepseek-4-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, fixture := newConfigHandler()
			fixture.cfg.ProviderModelPrefixes = tc.bindings
			fixture.cfg.ModelAliases = []configpkg.ModelAlias{{Alias: "existing", Target: "m1"}}
			before, snapshot := fixture.cfg, fixture.cfg.Clone()
			body, err := json.Marshal(map[string]any{
				"strategy": "fill",
				"modelAliases": []configpkg.ModelAlias{{
					Alias: tc.alias, Target: "m2",
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			recorder := httptest.NewRecorder()
			h.Update(recorder, postJSON("/admin/api/config/update", string(body)))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("namespace shadowing status = %d, want 400: %s", recorder.Code, recorder.Body.String())
			}
			if response := decodeEnvelope(t, recorder); !strings.Contains(response.Error, "namespace") {
				t.Fatalf("namespace error missing: %q", response.Error)
			}
			if fixture.cfg != before || !reflect.DeepEqual(fixture.cfg, snapshot) {
				t.Fatalf("rejected namespace alias mutated the configuration: %+v", fixture.cfg)
			}
		})
	}
}

func TestConfigUpdateValidatesMergedProviderModelNamespaces(t *testing.T) {
	h, fixture := newConfigHandler()
	fixture.cfg.ProviderModelPrefixes = map[string]string{"as": "antseed_primary"}
	fixture.cfg.ModelAliases = []configpkg.ModelAlias{{Alias: "as/deepseek-4-1", Target: "m1"}}
	before, snapshot := fixture.cfg, fixture.cfg.Clone()
	recorder := httptest.NewRecorder()
	h.Update(recorder, postJSON("/admin/api/config/update", `{"strategy":"fill","excludedModels":["internal-*"]}`))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("merged namespace validation status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
	if fixture.cfg != before || !reflect.DeepEqual(fixture.cfg, snapshot) {
		t.Fatalf("rejected merged policy patch mutated the configuration: %+v", fixture.cfg)
	}
}
