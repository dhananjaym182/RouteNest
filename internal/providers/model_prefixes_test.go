package providers

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"routenest/internal/config"
	"routenest/internal/domain"
	"routenest/internal/models"
)

func installModelPrefixTest(t *testing.T, records []domain.Model, providers ...*CustomProvider) {
	t.Helper()
	t.Setenv("ROUTENEST_DATA_DIR", t.TempDir())
	before := config.Snapshot()
	// Serial package fixtures initialize and restore private test state directly:
	// Publish intentionally cannot release a durable namespace reservation.
	t.Cleanup(func() { replaceConfigForPrefixTest(before) })
	replaceConfigForPrefixTest(config.DefaultProxy())
	config.Publish(config.DefaultProxy())
	installOfferingStateTest(t, records, providers...)
}

func replaceConfigForPrefixTest(cfg *config.Proxy) {
	config.Mu.Lock()
	*config.Held() = *cfg.Clone()
	config.Mu.Unlock()
}

func prefixForTest(t *testing.T, providerID string) string {
	t.Helper()
	for prefix, id := range config.Live().ProviderModelPrefixes {
		if id == providerID {
			return prefix
		}
	}
	t.Fatalf("provider %q has no reserved model namespace", providerID)
	return ""
}

func TestEnsureModelPrefixesIsolatesSharedModelsAndDisabledInstances(t *testing.T) {
	installModelPrefixTest(t, []domain.Model{{ID: "deepseek-v4-flash", Source: models.SourceZen}},
		&CustomProvider{ID: "antseed-first", Name: "AntSeed", BaseURL: "https://antseed.test/v1", Enabled: true, ModelIDs: []string{"deepseek-v4-flash"}},
		&CustomProvider{ID: "antseed-second", Name: "AntSeed", BaseURL: "https://antseed.test/v1", Enabled: false, ModelIDs: []string{"deepseek-v4-flash"}, CreatedAt: time.Now()},
	)
	before := config.Live()
	if err := EnsureModelPrefixes(); err != nil {
		t.Fatal(err)
	}
	if prefixForTest(t, "cline") != "cl" || prefixForTest(t, "opencode") != "op" || prefixForTest(t, "antseed-first") != "as" {
		t.Fatalf("unexpected preferred prefixes: %v", config.Live().ProviderModelPrefixes)
	}
	second := prefixForTest(t, "antseed-second")
	if second == "as" || !strings.HasPrefix(second, "as_") || len(config.Live().ProviderModelPrefixes) != 4 {
		t.Fatalf("disabled duplicate provider was not isolated: %v", config.Live().ProviderModelPrefixes)
	}
	if len(before.ProviderModelPrefixes) != 0 {
		t.Fatal("registration changed a published snapshot")
	}
}

func TestEnsureModelPrefixesSurvivesRenameDeleteAndRestart(t *testing.T) {
	first := &CustomProvider{ID: "antseed-first", Name: "AntSeed", BaseURL: "https://antseed.test/v1", Enabled: true, ModelIDs: []string{"deepseek-v4-flash@reasoning"}}
	second := &CustomProvider{ID: "antseed-second", Name: "AntSeed", BaseURL: first.BaseURL, Enabled: true, CreatedAt: time.Now()}
	installModelPrefixTest(t, nil, first, second)
	if err := EnsureModelPrefixes(); err != nil {
		t.Fatal(err)
	}
	original := config.Snapshot().ProviderModelPrefixes
	first.Name = "Renamed upstream"
	Upsert(first)
	if err := EnsureModelPrefixes(); err != nil || !reflect.DeepEqual(config.Live().ProviderModelPrefixes, original) {
		t.Fatalf("rename changed namespaces: err=%v prefixes=%v", err, config.Live().ProviderModelPrefixes)
	}
	if err := DeleteCustomProvider(first.ID); err != nil {
		t.Fatal(err)
	}
	if prefixForTest(t, first.ID) != "as" || !config.ProviderModelIDReserved(first.ID) {
		t.Fatal("deleted provider lost its reserved identity")
	}
	Upsert(&CustomProvider{ID: "replacement-antseed", Name: "AntSeed", BaseURL: first.BaseURL, Enabled: true})
	if err := EnsureModelPrefixes(); err != nil {
		t.Fatal(err)
	}
	if prefixForTest(t, "replacement-antseed") == "as" || prefixForTest(t, second.ID) == "as" {
		t.Fatal("deleted namespace was reused")
	}
	persisted := config.Snapshot().ProviderModelPrefixes
	reloaded := config.LoadLiveFromDisk()
	config.Publish(reloaded)
	if err := EnsureModelPrefixes(); err != nil || !reflect.DeepEqual(config.Live().ProviderModelPrefixes, persisted) {
		t.Fatalf("restart changed namespace bindings: err=%v prefixes=%v", err, config.Live().ProviderModelPrefixes)
	}
}

func TestEnsureModelPrefixesAllocationIgnoresRegistryAndPriorityOrder(t *testing.T) {
	var expected map[string]string
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "forward", true: "reverse"}[reverse], func(t *testing.T) {
			first := &CustomProvider{ID: "antseed-a", Name: "AntSeed", BaseURL: "https://antseed.test/v1", Priority: 100}
			second := &CustomProvider{ID: "antseed-b", Name: "AntSeed", BaseURL: first.BaseURL, Priority: 1, CreatedAt: time.Now()}
			providers := []*CustomProvider{first, second}
			if reverse {
				first.Priority, second.Priority = 1, 100
				providers = []*CustomProvider{second, first}
			}
			installModelPrefixTest(t, nil, providers...)
			if err := EnsureModelPrefixes(); err != nil {
				t.Fatal(err)
			}
			got := config.Snapshot().ProviderModelPrefixes
			if expected == nil {
				expected = got
			} else if !reflect.DeepEqual(got, expected) {
				t.Fatalf("registry order changed prefixes: %v versus %v", got, expected)
			}
		})
	}
}

func TestEnsureModelPrefixesAvoidsRawAndAdministratorAliasNamespaces(t *testing.T) {
	for _, conflict := range []string{"catalog", "disabled declaration", "administrator alias"} {
		t.Run(conflict, func(t *testing.T) {
			antseed := &CustomProvider{ID: "antseed", Name: "AntSeed", BaseURL: "https://antseed.test/v1", ModelIDs: []string{"deepseek-v4-flash"}}
			installModelPrefixTest(t, nil, antseed)
			switch conflict {
			case "catalog":
				models.Edit(func(pool *domain.AccountPool) bool {
					pool.Models = []domain.Model{{ID: "as/existing@variant"}}
					return true
				})
			case "disabled declaration":
				Upsert(&CustomProvider{ID: "other", Name: "Other", BaseURL: "https://other.test/v1", Enabled: false, ModelIDs: []string{"as/existing"}})
			case "administrator alias":
				cfg := config.Snapshot()
				cfg.ModelAliases = []config.ModelAlias{{Alias: "as/existing", Target: "deepseek-v4-flash"}}
				config.Publish(cfg)
			}
			if err := EnsureModelPrefixes(); err != nil {
				t.Fatal(err)
			}
			if prefixForTest(t, antseed.ID) == "as" {
				t.Fatal("new provider namespace hijacked an existing model or alias")
			}
		})
	}
}

func TestEnsureModelPrefixesRejectsBuiltinNamespaceConflicts(t *testing.T) {
	for _, conflict := range []string{"op/model", "cl/model", "custom builtin ID", "persisted builtin hijack"} {
		t.Run(conflict, func(t *testing.T) {
			installModelPrefixTest(t, nil)
			switch conflict {
			case "custom builtin ID":
				Upsert(&CustomProvider{ID: "opencode", Name: "Other", BaseURL: "https://other.test/v1"})
			case "persisted builtin hijack":
				cfg := config.Snapshot()
				cfg.ProviderModelPrefixes = map[string]string{"op": "other"}
				if err := config.SaveProxy(filepath.Join(os.Getenv("ROUTENEST_DATA_DIR"), config.ProxyPath), cfg); err != nil {
					t.Fatal(err)
				}
				replaceConfigForPrefixTest(config.LoadLiveFromDisk())
			default:
				models.Edit(func(pool *domain.AccountPool) bool { pool.Models = []domain.Model{{ID: conflict}}; return true })
			}
			before := config.Live()
			if err := EnsureModelPrefixes(); err == nil || config.Live() != before {
				t.Fatalf("builtin namespace conflict was not rejected atomically: err=%v", err)
			}
		})
	}
}

func TestSaveCustomProviderReservesNamespaceBeforePublication(t *testing.T) {
	installModelPrefixTest(t, nil)
	saved, err := SaveCustomProvider(CustomProvider{Name: "AntSeed", BaseURL: "https://antseed.test/v1", Enabled: true, ModelIDs: []string{"deepseek-v4-flash"}})
	if err != nil || saved.ID == "" {
		t.Fatalf("provider save failed: provider=%+v err=%v", saved, err)
	}
	if prefixForTest(t, saved.ID) != "as" {
		t.Fatal("saved provider has no preferred namespace")
	}
	saved.Name = "Renamed upstream"
	if _, err := SaveCustomProvider(saved); err != nil || prefixForTest(t, saved.ID) != "as" {
		t.Fatalf("provider update changed namespace: err=%v", err)
	}
	reloaded := config.LoadLiveFromDisk()
	if reloaded.ProviderModelPrefixes["as"] != saved.ID {
		t.Fatal("provider save did not durably reserve its namespace")
	}
}

func TestSaveCustomProviderNamespaceFailureDoesNotPublishProvider(t *testing.T) {
	installModelPrefixTest(t, nil)
	path := filepath.Join(os.Getenv("ROUTENEST_DATA_DIR"), config.ProxyPath)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	before := config.Live()
	_, err := SaveCustomProvider(CustomProvider{ID: "antseed", Name: "AntSeed", BaseURL: "https://antseed.test/v1", Enabled: true, ModelIDs: []string{"deepseek-v4-flash"}})
	if err == nil || Find("antseed") != nil || config.Live() != before || len(models.Snapshot()) != 0 {
		t.Fatalf("failed namespace registration published provider state: err=%v providers=%v", err, List())
	}
}
