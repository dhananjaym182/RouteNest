package config

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPublishPreservesProviderModelPrefixesFromStaleSnapshot(t *testing.T) {
	t.Setenv("ROUTENEST_DATA_DIR", t.TempDir())
	resetLive(t)
	initial := DefaultProxy()
	initial.ProviderModelPrefixes = map[string]string{"op": "opencode"}
	Publish(initial)
	stale := Snapshot()
	if err := EnsureProviderModelPrefixes([]string{"antseed"}, func(string, *Proxy) (string, error) { return "as", nil }); err != nil {
		t.Fatal(err)
	}
	current := Live()
	stale.OnlyFree = true
	Publish(stale)
	want := map[string]string{"op": "opencode", "as": "antseed"}
	if !Live().OnlyFree || !reflect.DeepEqual(Live().ProviderModelPrefixes, want) {
		t.Fatalf("stale settings update dropped reservations: %+v", Live())
	}
	if _, changed := stale.ProviderModelPrefixes["as"]; changed || !reflect.DeepEqual(current.ProviderModelPrefixes, want) {
		t.Fatal("merging reservations mutated an existing argument or snapshot")
	}
	withoutPrefixes := DefaultProxy()
	withoutPrefixes.Strategy = "quality"
	Publish(withoutPrefixes)
	if Live().Strategy != "quality" || !reflect.DeepEqual(Live().ProviderModelPrefixes, want) {
		t.Fatal("an ordinary settings update dropped reserved provider identities")
	}
	reloaded := LoadLiveFromDisk()
	if !reflect.DeepEqual(reloaded.ProviderModelPrefixes, want) {
		t.Fatal("stale settings update dropped persisted reservations")
	}
}

func TestPublishRejectsProviderModelPrefixRemappingAndDuplicateIdentity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		bindings map[string]string
		aliases  []ModelAlias
	}{
		{name: "retarget existing prefix", bindings: map[string]string{"as": "replacement"}},
		{name: "add duplicate identity", bindings: map[string]string{"an": "antseed"}},
		{name: "retarget builtin prefix", bindings: map[string]string{"op": "replacement"}},
		{name: "stale alias overlaps new reservation", aliases: []ModelAlias{{Alias: "as/model", Target: "other", Force: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ROUTENEST_DATA_DIR", t.TempDir())
			resetLive(t)
			base := DefaultProxy()
			base.ProviderModelPrefixes = map[string]string{"as": "antseed", "op": "opencode"}
			Publish(base)
			before := Live()
			next := DefaultProxy()
			next.OnlyFree = true
			next.ProviderModelPrefixes = tc.bindings
			next.ModelAliases = tc.aliases
			Publish(next)
			if Live() != before || !reflect.DeepEqual(LoadLiveFromDisk().ProviderModelPrefixes, base.ProviderModelPrefixes) {
				t.Fatalf("unsafe settings update was published: %+v", Live())
			}
		})
	}
}

func TestPublishNamespaceSaveFailurePreservesPreviousSnapshot(t *testing.T) {
	for _, reserved := range []bool{false, true} {
		t.Run(map[bool]string{false: "first reservation", true: "existing reservation"}[reserved], func(t *testing.T) {
			t.Setenv("ROUTENEST_DATA_DIR", t.TempDir())
			resetLive(t)
			base := DefaultProxy()
			if reserved {
				base.ProviderModelPrefixes = map[string]string{"as": "antseed"}
			}
			Publish(base)
			before := Live()
			originalSave := saveLiveProxy
			saveLiveProxy = func(string, *Proxy) error { return errors.New("injected save failure") }
			t.Cleanup(func() { saveLiveProxy = originalSave })
			next := DefaultProxy()
			next.OnlyFree = true
			if !reserved {
				next.ProviderModelPrefixes = map[string]string{"as": "antseed"}
			}
			Publish(next)
			if Live() != before || Live().OnlyFree {
				t.Fatal("failed namespace-bearing save published settings")
			}
		})
	}
}

func TestPublishHydratesReservationsBeforeFirstPublication(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ROUTENEST_DATA_DIR", dir)
	resetLive(t)
	persisted := DefaultProxy()
	persisted.ProviderModelPrefixes = map[string]string{"as": "retired-antseed"}
	if err := SaveProxy(filepath.Join(dir, ProxyPath), persisted); err != nil {
		t.Fatal(err)
	}
	next := DefaultProxy()
	next.OnlyFree = true
	Publish(next)
	if Live().ProviderModelPrefixes["as"] != "retired-antseed" || !Live().OnlyFree {
		t.Fatal("cold settings publication erased persisted identities")
	}
}
