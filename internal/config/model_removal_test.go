package config

import (
	"errors"
	"reflect"
	"testing"

	"routenest/internal/domain"
)

func TestPruneModelReferencesPreservesPublishedSnapshotAndOrder(t *testing.T) {
	t.Setenv("ROUTENEST_DATA_DIR", t.TempDir())
	resetLive(t)
	base := DefaultProxy()
	base.ModelChain = []string{"a", "gone", "b", "gone@Alpha"}
	base.ModelAliases = []ModelAlias{{Alias: "old", Target: "gone"}, {Alias: "new", Target: "b"}}
	base.Combos = []domain.ModelCombo{{ID: "empty", Enabled: true, Entries: []domain.ComboEntry{{Model: "gone", Source: "cline"}}}}
	Publish(base)
	before := Live()
	if err := PruneModelReferences(domain.ModelRemovalRef{ID: "gone", Source: "remote"}, true, true, false); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.ModelChain, base.ModelChain) || len(before.ModelAliases) != 2 || len(before.Combos[0].Entries) != 1 {
		t.Fatal("cleanup mutated an already published snapshot")
	}
	if !reflect.DeepEqual(Live().ModelChain, []string{"a", "b", "gone@Alpha"}) || len(Live().ModelAliases) != 1 || Live().Combos[0].Enabled {
		t.Fatalf("pruned config = %+v", Live())
	}
}

func TestPruneModelReferencesFailedSaveDoesNotPublish(t *testing.T) {
	t.Setenv("ROUTENEST_DATA_DIR", t.TempDir())
	resetLive(t)
	base := DefaultProxy()
	base.ModelChain = []string{"gone"}
	Publish(base)
	before := Live()
	originalSave := saveModelReferences
	saveModelReferences = func(string, *Proxy) error { return errors.New("injected save failure") }
	t.Cleanup(func() { saveModelReferences = originalSave })
	if err := PruneModelReferences(domain.ModelRemovalRef{ID: "gone", Source: "remote"}, true, true, false); err == nil || Live() != before {
		t.Fatalf("failed save published cleanup: %v %+v", err, Live())
	}
}
