package config

import (
	"path/filepath"
	"reflect"
	"testing"

	"routenest/internal/domain"
	"routenest/internal/platform"
)

func TestPruneModelReferencesCanonicalBuiltinComboSourcesPreserveSiblings(t *testing.T) {
	const modelID = "org/shared-model@revision"
	for _, target := range []struct{ source, providerID, prefix string }{{"remote", "cline", "cl"}, {"zen", "opencode", "op"}} {
		t.Run(target.providerID, func(t *testing.T) {
			dataDir := t.TempDir()
			t.Setenv(platform.DataDirEnv, dataDir)
			previous := Snapshot()
			Mu.Lock()
			*Held() = *DefaultProxy()
			Mu.Unlock()
			t.Cleanup(func() {
				Mu.Lock()
				*Held() = *previous.Clone()
				Mu.Unlock()
			})
			cfg := DefaultProxy()
			cfg.ProviderModelPrefixes = map[string]string{"bs": "provider-sibling", "retired": "retired-provider"}
			cfg.ModelChain = []string{modelID, target.prefix + "/" + modelID, "bs/" + modelID, "other-model"}
			cfg.ModelAliases = []ModelAlias{
				{Alias: "removed-alias", Target: target.prefix + "/" + modelID},
				{Alias: "sibling-alias", Target: "bs/" + modelID},
				{Alias: "shared-alias", Target: modelID},
			}
			entries := []domain.ComboEntry{
				{Model: modelID, Source: "remote"},
				{Model: modelID, Source: "cline"},
				{Model: modelID, Source: "zen"},
				{Model: modelID, Source: "provider", Provider: "provider-sibling"},
				{Model: modelID},
				{Model: "org/shared-model", Source: target.source},
				{Model: target.prefix + "/" + modelID},
			}
			cfg.Combos = []domain.ModelCombo{
				{ID: "shared", Enabled: true, Entries: entries},
				{ID: "removed-only", Enabled: true, Entries: []domain.ComboEntry{{Model: modelID, Source: target.source}}},
			}
			Publish(cfg)
			before := Live()
			if err := PruneModelReferences(domain.ModelRemovalRef{ID: modelID, Source: target.source, ProviderID: target.providerID}, false, true, false); err != nil {
				t.Fatalf("canonical builtin cleanup failed: %v", err)
			}
			var want []domain.ComboEntry
			for _, entry := range entries {
				remove := entry.Model == target.prefix+"/"+modelID || entry.Model == modelID && (entry.Source == target.source || target.providerID == "cline" && entry.Source == "cline")
				if !remove {
					want = append(want, entry)
				}
			}
			after := Live()
			if !reflect.DeepEqual(after.Combos[0].Entries, want) || !after.Combos[0].Enabled || after.Combos[1].Enabled || len(after.Combos[1].Entries) != 0 {
				t.Fatalf("canonical source cleanup left a removed entry or pruned a sibling: got=%+v want=%+v", after.Combos, want)
			}
			if !reflect.DeepEqual(after.ModelChain, []string{modelID, "bs/" + modelID, "other-model"}) || !reflect.DeepEqual(after.ModelAliases, cfg.ModelAliases[1:]) || !reflect.DeepEqual(after.ProviderModelPrefixes, cfg.ProviderModelPrefixes) {
				t.Fatal("canonical source cleanup changed sibling/global references or reserved namespaces")
			}
			if !reflect.DeepEqual(before.Combos[0].Entries, entries) {
				t.Fatal("cleanup mutated the previously published snapshot")
			}
			stored, err := LoadProxy(filepath.Join(dataDir, ProxyPath))
			if err != nil || !reflect.DeepEqual(stored.Combos[0].Entries, want) || stored.Combos[1].Enabled {
				t.Fatalf("canonical source cleanup was not persisted: err=%v", err)
			}
		})
	}
}
