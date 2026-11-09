package models

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"routenest/internal/accounts"
	"routenest/internal/domain"
	poolstore "routenest/internal/store/accounts"
)

// withPool installs pool as the live pool, persisted in a temp file, and
// restores the previous state when the test ends.
func withPool(t *testing.T, pool *domain.AccountPool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".routenest-accounts.json")
	oldPath := accounts.PoolPath
	accounts.PoolMu.Lock()
	oldPool := accounts.Pool
	accounts.PoolPath = path
	accounts.Pool = pool
	accounts.PoolMu.Unlock()
	t.Cleanup(func() {
		accounts.PoolMu.Lock()
		accounts.Pool = oldPool
		accounts.PoolMu.Unlock()
		accounts.PoolPath = oldPath
	})
	return path
}

func livePool() *domain.AccountPool {
	accounts.PoolMu.Lock()
	defer accounts.PoolMu.Unlock()
	return accounts.Pool
}

func TestSnapshotIsDeepCopy(t *testing.T) {
	withPool(t, &domain.AccountPool{Models: []domain.Model{
		{ID: "a", ThinkingLevels: []string{"low"}, InputModalities: []string{"text"}},
	}})
	snap := Snapshot()
	snap[0].ID = "mutated"
	snap[0].ThinkingLevels[0] = "mutated"
	if got := livePool().Models[0]; got.ID != "a" || got.ThinkingLevels[0] != "low" {
		t.Fatalf("snapshot aliases the pool: %+v", got)
	}
}

// Duplicate ids: metadata edits touch only the first record, disabling touches
// every record, and legacy deletion fails closed as ambiguous.
func TestDuplicateIDSemantics(t *testing.T) {
	withPool(t, &domain.AccountPool{Models: []domain.Model{
		{ID: "dup", Custom: true},
		{ID: "dup", Source: SourceRemote},
	}})

	UpdateContext("dup", 100, 10)
	m := livePool().Models
	if m[0].Context != 100 || m[1].Context != 0 {
		t.Fatalf("context edit must be first-match: %+v", m)
	}

	if n := SetDisabled([]string{"dup"}, true); n != 2 {
		t.Fatalf("disable changed %d records, want 2", n)
	}
	for _, rec := range livePool().Models {
		if !rec.Disabled {
			t.Fatalf("disable must be ID-wide: %+v", livePool().Models)
		}
	}

	before := Snapshot()
	if out, err := Delete("dup"); out != DeleteAmbiguous || err != nil {
		t.Fatalf("delete = %v, %v, want ambiguous", out, err)
	}
	if !reflect.DeepEqual(Snapshot(), before) {
		t.Fatalf("ambiguous delete changed records: %+v", Snapshot())
	}
}

func TestDeleteProtectsSyncedRecordButAllowsDelisted(t *testing.T) {
	withPool(t, &domain.AccountPool{Models: []domain.Model{
		{ID: "live", Source: SourceRemote},
		{ID: "gone", Source: SourceRemote, Delisted: true},
	}})
	if out, err := Delete("live"); out != DeleteProtected || err != nil {
		t.Fatalf("live synced record: %v, want protected", out)
	}
	if out, err := Delete("gone"); out != DeleteDeleted || err != nil {
		t.Fatalf("delisted record: %v, want deleted", out)
	}
	if out, err := Delete("missing"); out != DeleteNotFound || err != nil {
		t.Fatalf("missing record: %v, want not found", out)
	}
}

func TestDeletePersistenceFailureRollsBack(t *testing.T) {
	path := withPool(t, &domain.AccountPool{DefaultModel: "local", Models: []domain.Model{{ID: "local", Custom: true}}})
	accounts.PoolPath = t.TempDir()
	before := Snapshot()
	if _, err := Delete("local"); err == nil {
		t.Fatal("expected persistence failure")
	}
	if !reflect.DeepEqual(Snapshot(), before) || livePool().DefaultModel != "local" {
		t.Fatalf("failed delete changed live state: %+v", livePool())
	}
	accounts.PoolPath = path
	if outcome, err := Delete("local"); outcome != DeleteDeleted || err != nil {
		t.Fatalf("retry delete = %v, %v", outcome, err)
	}
	reloaded, err := poolstore.Load(path)
	if err != nil {
		t.Fatalf("reload successful deletion: %v", err)
	}
	if len(reloaded.Models) != 0 || reloaded.DefaultModel != "" {
		t.Fatalf("deleted model returned after restart: %+v", reloaded)
	}
}

func TestDefaultModelCleanup(t *testing.T) {
	withPool(t, &domain.AccountPool{DefaultModel: "x", Models: []domain.Model{{ID: "x", Custom: true}, {ID: "y", Custom: true}}})
	SetDisabled([]string{"y"}, true)
	if livePool().DefaultModel != "x" {
		t.Fatal("disabling another model must keep the default")
	}
	SetDisabled([]string{"x"}, true)
	if livePool().DefaultModel != "" {
		t.Fatal("disabling the default must clear it")
	}

	withPool(t, &domain.AccountPool{DefaultModel: "z", Models: []domain.Model{{ID: "z", Custom: true}}})
	if _, err := Delete("z"); err != nil {
		t.Fatal(err)
	}
	if livePool().DefaultModel != "" {
		t.Fatal("deleting the default must clear it")
	}
}

func TestMetaLockFollowsContextEdits(t *testing.T) {
	withPool(t, &domain.AccountPool{Models: []domain.Model{{ID: "m", Custom: true}}})
	UpdateContext("m", 4096, 8192)
	if m := livePool().Models[0]; !m.MetaLocked {
		t.Fatalf("non-zero limits must lock: %+v", m)
	}
	UpdateContext("m", 0, 0)
	if m := livePool().Models[0]; m.MetaLocked || m.Context != 0 {
		t.Fatalf("zero limits must unlock and reset: %+v", m)
	}
}

func TestUpdateCapabilitiesNilLeavesUnchanged(t *testing.T) {
	withPool(t, &domain.AccountPool{Models: []domain.Model{{ID: "c", ThinkingLevels: []string{"low"}, InputModalities: []string{"text"}}}})
	UpdateCapabilities("c", Capabilities{InputModalities: []string{}})
	m := livePool().Models[0]
	if !reflect.DeepEqual(m.ThinkingLevels, []string{"low"}) || len(m.InputModalities) != 0 {
		t.Fatalf("caps = %+v", m)
	}
}

// The on-disk schema is unchanged: a file written by the previous version loads,
// changes persist, and every field that was not edited survives the reload.
func TestPersistedFixtureRoundTrip(t *testing.T) {
	raw, err := os.ReadFile("testdata/accounts_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := poolstore.Load(writeTemp(t, raw))
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	path := withPool(t, loaded)

	SetDisabled([]string{"custom/one"}, true)
	reloaded, err := poolstore.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	byID := map[string]domain.Model{}
	for _, m := range reloaded.Models {
		byID[m.ID] = m
	}
	if !byID["custom/one"].Disabled {
		t.Fatal("edit not persisted")
	}
	if got := byID["remote/alpha"]; got.Context != 200000 || !got.MetaLocked || got.Source != SourceRemote {
		t.Fatalf("untouched remote record changed: %+v", got)
	}
	if got := byID["zen/free"]; got.TierSignal != "allowlist" || got.TierDecidedAt != 1700000000 {
		t.Fatalf("zen tier provenance lost: %+v", got)
	}
	if got := byID["seed/legacy"]; got.Source != SourceSeed {
		t.Fatalf("legacy seed source lost: %+v", got)
	}
	if reloaded.DefaultModel != "remote/alpha" || reloaded.ListenHost != "127.0.0.1" || len(reloaded.Keys) != 1 {
		t.Fatalf("pool-level fields changed: %+v", reloaded)
	}

	// The key set of the saved file matches the fixture's key set.
	want := topKeys(t, raw)
	saved, _ := os.ReadFile(path)
	if got := topKeys(t, saved); !reflect.DeepEqual(got, want) {
		t.Fatalf("saved top-level keys = %v, want %v", got, want)
	}
}

func writeTemp(t *testing.T, raw []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".routenest-accounts.json")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func topKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
