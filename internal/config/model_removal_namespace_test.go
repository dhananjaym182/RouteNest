package config

import (
	"fmt"
	"reflect"
	"testing"

	"routenest/internal/domain"
)

func TestPruneProviderQualifiedModelReferences(t *testing.T) {
	for _, tc := range []struct {
		name         string
		ref          domain.ModelRemovalRef
		references   []string
		want         []string
		removeGlobal bool
	}{
		{
			name: "shared provider model",
			ref:  domain.ModelRemovalRef{ID: "vendor/shared", Source: "provider", ProviderID: "antseed", Provider: "AntSeed"},
			references: []string{
				"vendor/shared", "as/vendor/shared", "op/vendor/shared", "cl/vendor/shared",
				"vendor/shared@antseed", "vendor/shared@AntSeed", "vendor/shared@reasoning",
				"as/vendor/shared@reasoning", "op/vendor/shared@reasoning",
			},
			want: []string{"vendor/shared", "op/vendor/shared", "cl/vendor/shared", "vendor/shared@reasoning", "as/vendor/shared@reasoning", "op/vendor/shared@reasoning"},
		},
		{
			name: "literal upstream at variant",
			ref:  domain.ModelRemovalRef{ID: "vendor/shared@reasoning", Source: "provider", ProviderID: "antseed", Provider: "AntSeed"},
			references: []string{
				"vendor/shared", "as/vendor/shared", "vendor/shared@reasoning", "as/vendor/shared@reasoning",
				"op/vendor/shared@reasoning", "as/vendor/shared@other", "vendor/shared@reasoning@antseed",
			},
			want: []string{"vendor/shared", "as/vendor/shared", "vendor/shared@reasoning", "op/vendor/shared@reasoning", "as/vendor/shared@other"},
		},
		{
			name: "global logical ID still preserves sibling pins and variants",
			ref:  domain.ModelRemovalRef{ID: "vendor/shared", Source: "remote", Provider: "Cline"},
			references: []string{
				"vendor/shared", "cl/vendor/shared", "as/vendor/shared", "op/vendor/shared",
				"vendor/shared@cline", "vendor/shared@Cline", "vendor/shared@reasoning", "as/vendor/shared@reasoning",
			},
			want:         []string{"as/vendor/shared", "op/vendor/shared", "vendor/shared@reasoning", "as/vendor/shared@reasoning"},
			removeGlobal: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ROUTENEST_DATA_DIR", t.TempDir())
			resetLive(t)
			base := DefaultProxy()
			base.ProviderModelPrefixes = map[string]string{"as": "antseed", "op": "opencode", "cl": "cline"}
			base.ModelChain = tc.references
			combo := domain.ModelCombo{ID: "namespace-cleanup", Enabled: true}
			for index, reference := range tc.references {
				base.ModelAliases = append(base.ModelAliases, ModelAlias{Alias: fmt.Sprintf("route-%d", index), Target: reference})
				combo.Entries = append(combo.Entries, domain.ComboEntry{Model: reference})
			}
			base.Combos = []domain.ModelCombo{combo}
			Publish(base)
			before := Live()
			if err := PruneModelReferences(tc.ref, tc.removeGlobal, true, true); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before.ModelChain, tc.references) || !reflect.DeepEqual(before.ProviderModelPrefixes, base.ProviderModelPrefixes) {
				t.Fatal("cleanup changed a previous published snapshot")
			}
			current := Live()
			aliases := make([]string, 0, len(current.ModelAliases))
			for _, alias := range current.ModelAliases {
				aliases = append(aliases, alias.Target)
			}
			entries := make([]string, 0, len(current.Combos[0].Entries))
			for _, entry := range current.Combos[0].Entries {
				entries = append(entries, entry.Model)
			}
			if !reflect.DeepEqual(current.ModelChain, tc.want) || !reflect.DeepEqual(aliases, tc.want) || !reflect.DeepEqual(entries, tc.want) {
				t.Fatalf("provider cleanup crossed an offering or variant: chain=%v aliases=%v combo=%v want=%v", current.ModelChain, aliases, entries, tc.want)
			}
			if !reflect.DeepEqual(current.ProviderModelPrefixes, base.ProviderModelPrefixes) {
				t.Fatal("model cleanup dropped provider reservations")
			}
		})
	}
}

func TestPruneModelReferencesPreservesPinnedUpstreamVariantsAndNameGroups(t *testing.T) {
	t.Setenv("ROUTENEST_DATA_DIR", t.TempDir())
	resetLive(t)
	base := DefaultProxy()
	base.ProviderModelPrefixes = map[string]string{"as": "antseed", "op": "opencode"}
	base.ModelAliases = []ModelAlias{
		{Alias: "removed", Target: "model", Provider: "antseed"},
		{Alias: "variant", Target: "model@reasoning", Provider: "antseed"},
		{Alias: "sibling", Target: "model", Provider: "opencode"},
		{Alias: "name-group", Target: "model", Provider: "AntSeed"},
		{Alias: "qualified-sibling", Target: "op/model", Provider: "antseed"},
	}
	base.Combos = []domain.ModelCombo{{ID: "pins", Enabled: true, Entries: []domain.ComboEntry{
		{Model: "model", Source: "provider", Provider: "antseed"},
		{Model: "model@reasoning", Source: "provider", Provider: "antseed"},
		{Model: "model", Source: "zen"},
		{Model: "model", Source: "provider", Provider: "AntSeed"},
	}}}
	Publish(base)
	if err := PruneModelReferences(domain.ModelRemovalRef{ID: "model", Source: "provider", ProviderID: "antseed", Provider: "AntSeed"}, false, true, false); err != nil {
		t.Fatal(err)
	}
	current := Live()
	if len(current.ModelAliases) != 4 || current.ModelAliases[0].Alias != "variant" || len(current.Combos[0].Entries) != 3 || current.Combos[0].Entries[0].Model != "model@reasoning" {
		t.Fatalf("cleanup truncated a variant or removed sibling/name-group references: aliases=%v combos=%v", current.ModelAliases, current.Combos)
	}
}

func TestPruneModelReferencesUsesBuiltinNamespacesInLegacyConfig(t *testing.T) {
	t.Setenv("ROUTENEST_DATA_DIR", t.TempDir())
	resetLive(t)
	base := DefaultProxy()
	base.ModelChain = []string{"model", "op/model", "cl/model", "model@reasoning"}
	Publish(base)
	if err := PruneModelReferences(domain.ModelRemovalRef{ID: "model", Source: "zen"}, false, true, true); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(Live().ModelChain, []string{"model", "cl/model", "model@reasoning"}) {
		t.Fatalf("legacy namespace cleanup did not isolate OpenCode: %v", Live().ModelChain)
	}
}
