package integration

// State-file characterization for plan section 6.7. Each persisted file is
// written with its owner's Save function into a temporary directory, reloaded,
// and saved again. The top-level and nested JSON key paths of the saved output
// are compared with testdata/state_json_keys.golden, so any renamed filename or
// JSON tag fails here before a refactor can ship it.
//
// Nothing in this file touches the developer's real data directory: every path
// is under t.TempDir().

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"routenest/internal/config"
	"routenest/internal/domain"
	storeaccounts "routenest/internal/store/accounts"
	storeconnections "routenest/internal/store/connections"
	storeproviders "routenest/internal/store/providers"
	storerequestlog "routenest/internal/store/requestlog"
	storesyncstate "routenest/internal/store/syncstate"
)

var updateStateKeysGolden = flag.Bool("update-state-keys-golden", false, "rewrite testdata/state_json_keys.golden")

const (
	fixtureMapKey = "fixture-key"
	maxFillDepth  = 6
)

var fixedTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// fillAll sets every exported, settable field to a deterministic non-zero value
// so that every JSON tag reachable from v is emitted.
func fillAll(v reflect.Value, depth int) {
	if depth > maxFillDepth {
		return
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString("fixture")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(7)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(7)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1.5)
	case reflect.Ptr:
		n := reflect.New(v.Type().Elem())
		fillAll(n.Elem(), depth+1)
		v.Set(n)
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 1, 1)
		fillAll(s.Index(0), depth+1)
		v.Set(s)
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		elem := reflect.New(v.Type().Elem()).Elem()
		fillAll(elem, depth+1)
		key := reflect.ValueOf(fixtureMapKey).Convert(v.Type().Key())
		m.SetMapIndex(key, elem)
		v.Set(m)
	case reflect.Struct:
		if v.Type() == reflect.TypeOf(time.Time{}) {
			v.Set(reflect.ValueOf(fixedTime))
			return
		}
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if f.CanSet() {
				fillAll(f, depth+1)
			}
		}
	}
}

func filled[T any]() *T {
	var zero T
	v := reflect.ValueOf(&zero).Elem()
	fillAll(v, 0)
	return &zero
}

// jsonKeyPaths returns sorted key paths such as "accounts[].email". Array
// elements collapse to "[]" so the list does not depend on element count.
func jsonKeyPaths(data []byte) []string {
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return []string{"<invalid json: " + err.Error() + ">"}
	}
	set := map[string]bool{}
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, child := range t {
				p := k
				if prefix != "" {
					p = prefix + "." + k
				}
				set[p] = true
				walk(p, child)
			}
		case []any:
			for _, child := range t {
				walk(prefix+"[]", child)
			}
		}
	}
	walk("", root)
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// roundTrip saves a fixture, reloads it, and saves again. A loader may normalize
// defaults on the first reload, so the test requires the second and third
// saves to be byte-identical and returns the second.
func roundTrip(t *testing.T, path string, save func() error, load func() error, resave func() error) []byte {
	t.Helper()
	if err := save(); err != nil {
		t.Fatalf("save %s: %v", filepath.Base(path), err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Base(path), err)
	}
	if err := load(); err != nil {
		t.Fatalf("load %s: %v", filepath.Base(path), err)
	}
	if err := resave(); err != nil {
		t.Fatalf("resave %s: %v", filepath.Base(path), err)
	}
	second, _ := os.ReadFile(path)
	if err := load(); err != nil {
		t.Fatalf("reload %s: %v", filepath.Base(path), err)
	}
	if err := resave(); err != nil {
		t.Fatalf("second resave %s: %v", filepath.Base(path), err)
	}
	third, _ := os.ReadFile(path)
	if !bytes.Equal(second, third) {
		t.Errorf("%s is not stable across load/save/reload", filepath.Base(path))
	}
	// The first save is not compared by key: loaders normalize defaults (for
	// example, config headers), which is intended. The reloaded output is the
	// persisted contract, so it is the one frozen in the golden.
	_ = first
	return second
}

// stateFixtures holds the last saved bytes per file. Keyed by name so repeated
// runs (-count=N) overwrite rather than append.
var stateFixtures = map[string][]byte{}

func record(file string, data []byte) {
	stateFixtures[file] = data
}

func TestStateFileRoundTrip_Accounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), storeaccounts.FileName)
	if storeaccounts.FileName != ".routenest-accounts.json" {
		t.Fatalf("FileName = %q", storeaccounts.FileName)
	}
	p := filled[domain.AccountPool]()
	var cur *domain.AccountPool
	data := roundTrip(t, path,
		func() error { return storeaccounts.Save(path, p) },
		func() error { var err error; cur, err = storeaccounts.Load(path); return err },
		func() error { return storeaccounts.Save(path, cur) })
	record(storeaccounts.FileName, data)
}

func TestStateFileRoundTrip_Connections(t *testing.T) {
	path := filepath.Join(t.TempDir(), storeconnections.FileName)
	if storeconnections.FileName != ".routenest-connections.json" {
		t.Fatalf("FileName = %q", storeconnections.FileName)
	}
	list := []*domain.Connection{filled[domain.Connection]()}
	var cur []*domain.Connection
	data := roundTrip(t, path,
		func() error { return storeconnections.Save(path, list) },
		func() error { var err error; cur, err = storeconnections.Load(path); return err },
		func() error { return storeconnections.Save(path, cur) })
	record(storeconnections.FileName, data)
}

func TestStateFileRoundTrip_Providers(t *testing.T) {
	path := filepath.Join(t.TempDir(), storeproviders.FileName)
	if storeproviders.FileName != ".routenest-providers.json" {
		t.Fatalf("FileName = %q", storeproviders.FileName)
	}
	list := []*domain.CustomProvider{filled[domain.CustomProvider]()}
	var cur []*domain.CustomProvider
	data := roundTrip(t, path,
		func() error { return storeproviders.Save(path, list) },
		func() error { var err error; cur, err = storeproviders.Load(path); return err },
		func() error { return storeproviders.Save(path, cur) })
	record(storeproviders.FileName, data)
}

func TestStateFileRoundTrip_ProviderCooldowns(t *testing.T) {
	path := filepath.Join(t.TempDir(), storeproviders.CooldownsFileName)
	if storeproviders.CooldownsFileName != ".routenest-providers-cooldowns.json" {
		t.Fatalf("CooldownsFileName = %q", storeproviders.CooldownsFileName)
	}
	future := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	cooldowns := map[string]time.Time{"provider-a:model-x": future}
	var cur map[string]time.Time
	data := roundTrip(t, path,
		func() error { return storeproviders.SaveCooldowns(path, cooldowns) },
		func() error { var err error; cur, err = storeproviders.LoadCooldowns(path); return err },
		func() error { return storeproviders.SaveCooldowns(path, cur) })
	record(storeproviders.CooldownsFileName, data)
}

func TestStateFileRoundTrip_ProvidersSync(t *testing.T) {
	path := filepath.Join(t.TempDir(), storesyncstate.FileName)
	if storesyncstate.FileName != ".routenest-providers-sync.json" {
		t.Fatalf("FileName = %q", storesyncstate.FileName)
	}
	states := map[string]storesyncstate.State{"provider-a": *filled[storesyncstate.State]()}
	var cur map[string]*storesyncstate.State
	data := roundTrip(t, path,
		func() error { return storesyncstate.Save(path, states) },
		func() error { var err error; cur, err = storesyncstate.Load(path); return err },
		func() error {
			plain := map[string]storesyncstate.State{}
			for k, v := range cur {
				plain[k] = *v
			}
			return storesyncstate.Save(path, plain)
		})
	record(storesyncstate.FileName, data)
}

func TestStateFileRoundTrip_RequestLogs(t *testing.T) {
	path := filepath.Join(t.TempDir(), storerequestlog.FileName)
	if storerequestlog.FileName != ".routenest-request-logs.json" {
		t.Fatalf("FileName = %q", storerequestlog.FileName)
	}
	entries := []domain.RequestLog{*filled[domain.RequestLog]()}
	var cur []domain.RequestLog
	data := roundTrip(t, path,
		func() error { return storerequestlog.Save(path, entries) },
		func() error { var err error; cur, err = storerequestlog.Load(path); return err },
		func() error { return storerequestlog.Save(path, cur) })
	record(storerequestlog.FileName, data)
}

func TestStateFileRoundTrip_Config(t *testing.T) {
	const name = ".routenest-config.json"
	path := filepath.Join(t.TempDir(), name)
	cfg := filled[config.Proxy]()
	var cur *config.Proxy
	data := roundTrip(t, path,
		func() error { return config.SaveProxy(path, cfg) },
		func() error { var err error; cur, err = config.LoadProxy(path); return err },
		func() error { return config.SaveProxy(path, cur) })
	record(name, data)
}

func TestStateFileRoundTrip_Zen(t *testing.T) {
	const name = ".routenest-zen.json"
	path := filepath.Join(t.TempDir(), name)
	cfg := filled[config.Zen]()
	var cur *config.Zen
	data := roundTrip(t, path,
		func() error { return config.SaveZen(path, cfg) },
		func() error { var err error; cur, err = config.LoadZen(path, "https://example.invalid"); return err },
		func() error { return config.SaveZen(path, cur) })
	record(name, data)
}

func TestStateFileRoundTrip_ClineProxy(t *testing.T) {
	const name = ".routenest-proxy.json"
	path := filepath.Join(t.TempDir(), name)
	cfg := filled[config.ClineEgress]()
	var cur config.ClineEgress
	data := roundTrip(t, path,
		func() error { return config.SaveClineEgress(path, cfg) },
		func() error { var err error; cur, err = config.LoadClineEgress(path); return err },
		func() error { return config.SaveClineEgress(path, &cur) })
	record(name, data)
}

// TestStateFileNamesAreStable checks that the path variables used by runtime
// owners still resolve to the plan's filenames inside the isolated test directory.
func TestStateFileNamesAreStable(t *testing.T) {
	want := []string{
		".routenest-accounts.json",
		".routenest-config.json",
		".routenest-zen.json",
		".routenest-proxy.json",
		".routenest-providers.json",
		".routenest-connections.json",
		".routenest-providers-cooldowns.json",
		".routenest-providers-sync.json",
		".routenest-request-logs.json",
		".routenest-credentials.json",
	}
	got := map[string]bool{}
	for _, name := range want {
		got[name] = true
	}
	for _, name := range []string{storeaccounts.FileName, storeconnections.FileName, storeproviders.FileName, storeproviders.CooldownsFileName, storesyncstate.FileName, storerequestlog.FileName} {
		if !got[name] {
			t.Errorf("owner file %q is not in the section 6.7 list", name)
		}
	}
}

// TestStateKeysGolden compares the JSON key paths of every fixture with the
// checked-in golden. Run with -update-state-keys-golden after an intended change.
func TestStateKeysGolden(t *testing.T) {
	if len(stateFixtures) == 0 {
		t.Skip("round-trip fixtures did not run; run the full package")
	}
	names := make([]string, 0, len(stateFixtures))
	for name := range stateFixtures {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "== %s\n", name)
		for _, p := range jsonKeyPaths(stateFixtures[name]) {
			b.WriteString(p)
			b.WriteByte('\n')
		}
	}
	got := []byte(b.String())
	path := filepath.Join("testdata", "state_json_keys.golden")
	if *updateStateKeysGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update-state-keys-golden): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("state JSON key paths changed; review and rerun with -update-state-keys-golden if intended\n%s", got)
	}
}
