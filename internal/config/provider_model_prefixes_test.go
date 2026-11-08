package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestProviderModelPrefixCloneOwnership(t *testing.T) {
	before := DefaultProxy()
	before.ProviderModelPrefixes = map[string]string{"as": "antseed-instance"}
	next := before.Clone()
	next.ProviderModelPrefixes["as"] = "other-instance"
	next.ProviderModelPrefixes["op"] = "opencode"
	if before.ProviderModelPrefixes["as"] != "antseed-instance" || len(before.ProviderModelPrefixes) != 1 {
		t.Fatal("prefix clone shares reservations with an earlier snapshot")
	}
	if DefaultProxy().Clone().ProviderModelPrefixes != nil {
		t.Fatal("cloning a missing prefix map must preserve nil")
	}
}

func TestProviderModelIDReservedIncludesRetiredInstances(t *testing.T) {
	resetLive(t)
	cfg := DefaultProxy()
	cfg.ProviderModelPrefixes = map[string]string{"as": "retired-antseed-instance"}
	live.Store(cfg)
	for _, id := range []string{"cline", "opencode", "retired-antseed-instance", " retired-antseed-instance "} {
		if !ProviderModelIDReserved(id) {
			t.Errorf("provider identity %q is not reserved", id)
		}
	}
	for _, id := range []string{"", "as", "AntSeed", "RETIRED-ANTSEED-INSTANCE"} {
		if ProviderModelIDReserved(id) {
			t.Errorf("unregistered provider identity %q is reserved", id)
		}
	}
}

func TestEnsureProviderModelPrefixesPersistsReservationsBeforePublish(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ROUTENEST_DATA_DIR", dir)
	resetLive(t)
	before := DefaultProxy()
	before.ProviderModelPrefixes = map[string]string{"as": "retired-antseed"}
	live.Store(before)
	calls := 0
	choose := func(id string, current *Proxy) (string, error) {
		calls++
		if current.ProviderModelPrefixes["as"] != "retired-antseed" {
			t.Fatal("allocator did not receive existing reservations")
		}
		return map[string]string{"cline": "cl", "opencode": "op", "new-antseed": "as_new"}[id], nil
	}
	ids := []string{"cline", "opencode", "retired-antseed", "new-antseed"}
	if err := EnsureProviderModelPrefixes(ids, choose); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"cl": "cline", "op": "opencode", "as": "retired-antseed", "as_new": "new-antseed"}
	if calls != 3 || !reflect.DeepEqual(Live().ProviderModelPrefixes, want) || len(before.ProviderModelPrefixes) != 1 {
		t.Fatalf("registration changed previous snapshot or reservations: calls=%d prefixes=%v previous=%v", calls, Live().ProviderModelPrefixes, before.ProviderModelPrefixes)
	}
	reloaded, err := LoadProxy(filepath.Join(dir, ProxyPath))
	if err != nil || !reflect.DeepEqual(reloaded.ProviderModelPrefixes, want) {
		t.Fatalf("restart lost reservations: prefixes=%v err=%v", reloaded.ProviderModelPrefixes, err)
	}
	current := Live()
	if err := os.Remove(filepath.Join(dir, ProxyPath)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, ProxyPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := EnsureProviderModelPrefixes(ids, choose); err != nil || Live() != current || calls != 3 {
		t.Fatalf("unchanged reservations wrote or republished: err=%v calls=%d", err, calls)
	}
}

func TestEnsureProviderModelPrefixesFailedSaveDoesNotPublish(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ROUTENEST_DATA_DIR", dir)
	resetLive(t)
	before := DefaultProxy()
	live.Store(before)
	if err := os.Mkdir(filepath.Join(dir, ProxyPath), 0700); err != nil {
		t.Fatal(err)
	}
	err := EnsureProviderModelPrefixes([]string{"antseed"}, func(string, *Proxy) (string, error) { return "as", nil })
	if err == nil || Live() != before || before.ProviderModelPrefixes != nil {
		t.Fatalf("failed registration published a namespace: err=%v prefixes=%v", err, Live().ProviderModelPrefixes)
	}
}

func TestProviderModelPrefixValidationRejectsUnsafeBindings(t *testing.T) {
	for _, tc := range []struct {
		name     string
		bindings map[string]string
		aliases  []ModelAlias
	}{
		{name: "builtin prefix hijack", bindings: map[string]string{"op": "custom"}},
		{name: "builtin identity hijack", bindings: map[string]string{"oc": "opencode"}},
		{name: "duplicate identity", bindings: map[string]string{"as": "one", "as2": "one"}},
		{name: "uppercase prefix", bindings: map[string]string{"AS": "one"}},
		{name: "slash prefix", bindings: map[string]string{"as/nested": "one"}},
		{name: "empty identity", bindings: map[string]string{"as": ""}},
		{name: "whitespace identity", bindings: map[string]string{"as": " one "}},
		{name: "administrator alias", bindings: map[string]string{"as": "one"}, aliases: []ModelAlias{{Alias: "as/model", Target: "other"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultProxy()
			cfg.ProviderModelPrefixes = tc.bindings
			cfg.ModelAliases = tc.aliases
			if err := cfg.ValidateProviderModelPrefixes(); err == nil {
				t.Fatal("unsafe namespace was accepted")
			}
		})
	}
}

func TestEnsureProviderModelPrefixesRejectsReassignment(t *testing.T) {
	t.Setenv("ROUTENEST_DATA_DIR", t.TempDir())
	resetLive(t)
	before := DefaultProxy()
	before.ProviderModelPrefixes = map[string]string{"as": "retired-antseed"}
	live.Store(before)
	err := EnsureProviderModelPrefixes([]string{"new-antseed"}, func(string, *Proxy) (string, error) { return "as", nil })
	if err == nil || Live() != before || before.ProviderModelPrefixes["as"] != "retired-antseed" {
		t.Fatalf("retired namespace was reassigned: err=%v prefixes=%v", err, Live().ProviderModelPrefixes)
	}
}

func TestProviderModelNamespaceHelpersPreserveUpstreamIdentity(t *testing.T) {
	cfg := DefaultProxy()
	cfg.ProviderModelPrefixes = map[string]string{"as": "antseed-instance", "zz": "other-instance"}
	for providerID, want := range map[string]string{"cline": "cl", "opencode": "op", "antseed-instance": "as", "other-instance": "zz", "AntSeed": ""} {
		if got := cfg.ProviderModelPrefix(providerID); got != want {
			t.Errorf("provider %q prefix = %q, want %q", providerID, got, want)
		}
	}
	for _, tc := range []struct {
		raw, model, provider string
		qualified            bool
	}{
		{raw: "as/deepseek-v4-flash", model: "deepseek-v4-flash", provider: "antseed-instance", qualified: true},
		{raw: " as/vendor/model@reasoning ", model: "vendor/model@reasoning", provider: "antseed-instance", qualified: true},
		{raw: "as/", provider: "antseed-instance", qualified: true},
		{raw: "op/deepseek-v4-flash", model: "deepseek-v4-flash", provider: "opencode", qualified: true},
		{raw: "cl/deepseek-v4-flash", model: "deepseek-v4-flash", provider: "cline", qualified: true},
		{raw: "openrouter/vendor/model", model: "openrouter/vendor/model"},
		{raw: "opencode/model", model: "opencode/model"},
		{raw: "AS/model", model: "AS/model"},
		{raw: "deepseek-v4-flash@reasoning", model: "deepseek-v4-flash@reasoning"},
	} {
		model, provider, qualified := cfg.SplitProviderModel(tc.raw)
		if model != tc.model || provider != tc.provider || qualified != tc.qualified {
			t.Errorf("split %q = %q, %q, %v; want %q, %q, %v", tc.raw, model, provider, qualified, tc.model, tc.provider, tc.qualified)
		}
	}
}
