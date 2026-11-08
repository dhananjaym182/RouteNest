package admin

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/config"
	"routenest/internal/domain"
	"routenest/internal/platform"
	"routenest/internal/providers"
	"routenest/internal/zen"
)

const offeringNameViewRawModel = "org/shared-model@revision"

type offeringNameViewRow struct {
	domain.ModelOffering
	QualifiedID  string `json:"qualifiedId"`
	ProviderName string `json:"providerName"`
}

func installOfferingNameViewFixture(t *testing.T, records []domain.Model, custom []*domain.CustomProvider, prefixes map[string]string) string {
	t.Helper()
	dataDir := t.TempDir()
	t.Setenv(platform.DataDirEnv, dataDir)

	previousConfig := config.Snapshot()
	cfg := config.DefaultProxy()
	cfg.ProviderModelPrefixes = prefixes
	// These serial fixtures replace private test state directly. Public config
	// publication deliberately retains durable namespace reservations.
	config.Mu.Lock()
	*config.Held() = *cfg
	config.Mu.Unlock()
	t.Cleanup(func() {
		config.Mu.Lock()
		*config.Held() = *previousConfig.Clone()
		config.Mu.Unlock()
	})

	accounts.PoolMu.Lock()
	previousPool, previousPoolPath := accounts.Pool, accounts.PoolPath
	accounts.Pool = &domain.AccountPool{Models: records, ModelCatalogInitialized: true}
	accounts.PoolPath = filepath.Join(dataDir, ".routenest-accounts.json")
	accounts.PoolMu.Unlock()
	t.Cleanup(func() {
		accounts.PoolMu.Lock()
		accounts.Pool, accounts.PoolPath = previousPool, previousPoolPath
		accounts.PoolMu.Unlock()
	})

	providers.Mu.Lock()
	previousProviders, previousLoaded := providers.CustomProviders, providers.Loaded
	previousProviderPath, previousCooldownPath := providers.FilePath, providers.CooldownsPath
	previousCooldowns := providers.Cooldowns
	providers.CustomProviders, providers.Loaded = custom, true
	providers.FilePath = filepath.Join(dataDir, ".routenest-providers.json")
	providers.CooldownsPath = filepath.Join(dataDir, ".routenest-providers-cooldowns.json")
	providers.Cooldowns = make(map[string]time.Time)
	providers.Mu.Unlock()
	t.Cleanup(func() {
		providers.Mu.Lock()
		providers.CustomProviders, providers.Loaded = previousProviders, previousLoaded
		providers.FilePath, providers.CooldownsPath = previousProviderPath, previousCooldownPath
		providers.Cooldowns = previousCooldowns
		providers.Mu.Unlock()
	})

	zen.ConfigMu.Lock()
	previousZenConfig := zen.Config
	zen.Config = zen.DefaultConfig()
	zen.Config.Enabled = true
	zen.ConfigMu.Unlock()
	t.Cleanup(func() {
		zen.ConfigMu.Lock()
		zen.Config = previousZenConfig
		zen.ConfigMu.Unlock()
	})
	return dataDir
}

func readOfferingNameViewRows(t *testing.T) []offeringNameViewRow {
	t.Helper()
	rec := callProviderSystemHandler(t, handleModelOfferings, http.MethodGet)
	if rec.Code != http.StatusOK {
		t.Fatalf("offerings status = %d: %s", rec.Code, rec.Body.String())
	}
	var rows []offeringNameViewRow
	if err := json.Unmarshal(decodeProviderSystemData(t, rec)["offerings"], &rows); err != nil {
		t.Fatalf("decode offering display fields: %v", err)
	}
	return rows
}

func TestModelOfferingsDisplayNamesIsolateSharedLiteralIDs(t *testing.T) {
	providerID := "prov_name_view_shared"
	installOfferingNameViewFixture(t, []domain.Model{
		{ID: offeringNameViewRawModel, Source: "remote", Provider: "Stale pooled label", Cost: "free", Status: "active"},
		{ID: offeringNameViewRawModel, Source: "zen", Provider: "Stale pooled label", Cost: "free", Status: "active"},
	}, []*domain.CustomProvider{{
		ID: providerID, Name: "AntSeed", Enabled: true, ModelIDs: []string{offeringNameViewRawModel},
	}}, map[string]string{"as": providerID})

	want := map[domain.ProviderInstanceID]struct{ qualifiedID, name string }{
		"cline":                               {"cl/" + offeringNameViewRawModel, "Cline"},
		"opencode":                            {"op/" + offeringNameViewRawModel, "OpenCode Zen"},
		domain.ProviderInstanceID(providerID): {"as/" + offeringNameViewRawModel, "AntSeed"},
	}
	rows := readOfferingNameViewRows(t)
	if len(rows) != len(want) {
		t.Fatalf("offering count = %d, want %d", len(rows), len(want))
	}
	for _, row := range rows {
		expected, ok := want[row.ProviderID]
		if !ok {
			t.Fatalf("unexpected provider ID %q", row.ProviderID)
		}
		if row.QualifiedID != expected.qualifiedID || row.ProviderName != expected.name {
			t.Errorf("provider %q display = (%q, %q), want (%q, %q)", row.ProviderID, row.QualifiedID, row.ProviderName, expected.qualifiedID, expected.name)
		}
		if row.ModelID != offeringNameViewRawModel || row.UpstreamModelID != offeringNameViewRawModel {
			t.Errorf("display projection changed literal model identity: %+v", row.ModelOffering)
		}
		delete(want, row.ProviderID)
	}
}

func TestModelOfferingsDisplayNamesFollowRenameWithoutReassigningPrefix(t *testing.T) {
	providerID := "prov_name_view_rename"
	provider := &domain.CustomProvider{ID: providerID, Name: "AntSeed", Enabled: true, ModelIDs: []string{offeringNameViewRawModel}}
	installOfferingNameViewFixture(t, nil, []*domain.CustomProvider{provider}, map[string]string{"as": providerID})
	before := readOfferingNameViewRows(t)
	if len(before) != 1 || before[0].ProviderName != "AntSeed" || before[0].QualifiedID != "as/"+offeringNameViewRawModel {
		t.Fatalf("initial offering display = %+v", before)
	}

	providers.Mu.Lock()
	provider.Name = "OpenCode Zen"
	providers.Mu.Unlock()
	after := readOfferingNameViewRows(t)
	if len(after) != 1 || after[0].ProviderName != "OpenCode Zen" || after[0].QualifiedID != before[0].QualifiedID {
		t.Fatalf("renamed offering display = %+v", after)
	}
	if !reflect.DeepEqual(before[0].ModelOffering, after[0].ModelOffering) {
		t.Fatalf("provider rename changed offering identity or state: before=%+v after=%+v", before[0].ModelOffering, after[0].ModelOffering)
	}
}

func TestModelOfferingsDisplayNamesPreserveExistingFieldsAndOrder(t *testing.T) {
	providerID := "prov_name_view_fields"
	installOfferingNameViewFixture(t, []domain.Model{
		{ID: offeringNameViewRawModel, Source: "remote", Cost: "free", Status: "limited", Disabled: true, Delisted: true,
			Custom: true, Context: 131072, Output: 8192, MetaLocked: true, TierSignal: "manual", TierDecidedAt: 1700000001,
			ThinkingLevels: []string{"low", "high"}, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}},
		{ID: offeringNameViewRawModel, Source: "zen", Cost: "pass", Status: "active", Context: 64000, Output: 4096,
			TierSignal: "discovery", TierDecidedAt: 1700000002},
		{ID: "another-model", Source: "provider", Provider: "AntSeed", Cost: "free", Status: "active", TierSignal: "manual", Custom: true},
	}, []*domain.CustomProvider{{
		ID: providerID, Name: "AntSeed", Enabled: false, ModelIDs: []string{offeringNameViewRawModel, "another-model"},
	}}, map[string]string{"as": providerID})

	expected, err := providers.SnapshotModelOfferings()
	if err != nil {
		t.Fatal(err)
	}
	if len(expected) != 4 {
		t.Fatalf("fixture offering count = %d, want 4", len(expected))
	}
	expectedJSON, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	var expectedFields []map[string]any
	if err := json.Unmarshal(expectedJSON, &expectedFields); err != nil {
		t.Fatal(err)
	}

	rec := callProviderSystemHandler(t, handleModelOfferings, http.MethodGet)
	if rec.Code != http.StatusOK {
		t.Fatalf("offerings status = %d: %s", rec.Code, rec.Body.String())
	}
	var actualFields []map[string]any
	if err := json.Unmarshal(decodeProviderSystemData(t, rec)["offerings"], &actualFields); err != nil {
		t.Fatal(err)
	}
	for _, row := range actualFields {
		delete(row, "qualifiedId")
		delete(row, "providerName")
	}
	if !reflect.DeepEqual(actualFields, expectedFields) {
		t.Fatalf("display fields changed legacy offering fields, count, or order: got=%+v want=%+v", actualFields, expectedFields)
	}
}

func TestModelOfferingsDisplayNamesDoNotAllocateOrPersistPrefixes(t *testing.T) {
	providerID := "prov_name_view_unallocated"
	dataDir := installOfferingNameViewFixture(t, []domain.Model{
		{ID: offeringNameViewRawModel, Source: "remote", Cost: "free", Status: "active"},
	}, []*domain.CustomProvider{{
		ID: providerID, Name: "AntSeed", Enabled: true, ModelIDs: []string{offeringNameViewRawModel},
	}}, nil)
	configPath := filepath.Join(dataDir, config.ProxyPath)
	marker := []byte("read-only GET must preserve this file\n")
	if err := os.WriteFile(configPath, marker, 0600); err != nil {
		t.Fatal(err)
	}
	before := config.Snapshot()
	rows := readOfferingNameViewRows(t)
	if len(rows) != 2 {
		t.Fatalf("offering count = %d, want 2", len(rows))
	}
	for _, row := range rows {
		switch row.ProviderID {
		case "cline":
			if row.QualifiedID != "cl/"+offeringNameViewRawModel || row.ProviderName != "Cline" {
				t.Errorf("builtin display did not use its existing namespace: %+v", row)
			}
		case domain.ProviderInstanceID(providerID):
			if row.QualifiedID != "" || row.ProviderName != "AntSeed" || row.ModelID != offeringNameViewRawModel {
				t.Errorf("unallocated provider lost its raw identity or acquired a namespace: %+v", row)
			}
		default:
			t.Errorf("unexpected provider ID %q", row.ProviderID)
		}
	}
	if !reflect.DeepEqual(config.Snapshot(), before) {
		t.Fatal("GET changed the live proxy configuration")
	}
	stored, err := os.ReadFile(configPath)
	if err != nil || !reflect.DeepEqual(stored, marker) {
		t.Fatalf("GET changed persisted proxy configuration: read error=%v", err)
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil || len(entries) != 1 || entries[0].Name() != config.ProxyPath {
		t.Fatalf("GET created persistence files: entries=%v error=%v", entries, err)
	}
}

func TestModelOfferingsDisplayNamesDoNotExpandEmptyCatalog(t *testing.T) {
	for _, initialized := range []bool{false, true} {
		name := "uninitialized"
		if initialized {
			name = "initialized"
		}
		t.Run(name, func(t *testing.T) {
			installOfferingNameViewFixture(t, nil, nil, nil)
			accounts.PoolMu.Lock()
			accounts.Pool.ModelCatalogInitialized = initialized
			accounts.PoolMu.Unlock()
			if rows := readOfferingNameViewRows(t); len(rows) != 0 {
				t.Fatalf("empty catalog acquired offline seed offerings: %+v", rows)
			}
		})
	}
}
