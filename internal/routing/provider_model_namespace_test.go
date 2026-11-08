package routing

import (
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

const (
	namespacePolicyProviderID = "prov_namespace_policy_antseed"
	namespacePolicyModel      = "org/deepseek-v4.1@revision"
)

func installNamespacePolicyFixture(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(platform.DataDirEnv, dir)
	previousLive := config.Snapshot()
	previousTestConfig, previousConfigProvider := testProxyConfig, ConfigProvider
	// These serial fixtures initialize isolated test state; production Publish
	// deliberately does not release durable namespace reservations.
	config.Mu.Lock()
	*config.Held() = *config.DefaultProxy()
	config.Mu.Unlock()
	t.Cleanup(func() {
		testProxyConfig, ConfigProvider = previousTestConfig, previousConfigProvider
		config.Mu.Lock()
		*config.Held() = *previousLive.Clone()
		config.Mu.Unlock()
	})
	cfg := defaultProxyConfig()
	cfg.ProviderModelPrefixes = map[string]string{"as": namespacePolicyProviderID, "op": "opencode", "cl": "cline"}
	config.Publish(cfg)
	setProxyConfig(cfg)

	previousPool, previousPoolPath := accounts.Pool, accounts.PoolPath
	accounts.PoolPath = filepath.Join(dir, "accounts.json")
	accounts.Pool = &domain.AccountPool{Models: []domain.Model{{
		ID: namespacePolicyModel, Provider: "OpenCode", Source: "zen", Cost: "free", TierSignal: "manual", Status: "active",
		Context: 32768, ThinkingLevels: []string{"low"},
	}}}
	t.Cleanup(func() { accounts.Pool, accounts.PoolPath = previousPool, previousPoolPath })
	providers.Mu.Lock()
	previousProviders, previousLoaded := providers.CustomProviders, providers.Loaded
	previousProviderPath, previousCooldownPath := providers.FilePath, providers.CooldownsPath
	previousCooldowns := providers.Cooldowns
	providers.FilePath = filepath.Join(dir, "providers.json")
	providers.CooldownsPath = filepath.Join(dir, "cooldowns.json")
	providers.CustomProviders = nil
	providers.Loaded = true
	providers.Cooldowns = map[string]time.Time{}
	providers.Mu.Unlock()
	t.Cleanup(func() {
		providers.Mu.Lock()
		providers.CustomProviders, providers.Loaded = previousProviders, previousLoaded
		providers.FilePath, providers.CooldownsPath = previousProviderPath, previousCooldownPath
		providers.Cooldowns = previousCooldowns
		providers.Mu.Unlock()
	})
	providers.Upsert(&providers.CustomProvider{
		ID: namespacePolicyProviderID, Name: "AntSeed", BaseURL: "http://antseed.namespace-policy.test/v1", Enabled: true,
		ModelIDs: []string{namespacePolicyModel},
	})
	if err := providers.SetModelCost(namespacePolicyProviderID, namespacePolicyModel, "pass"); err != nil {
		t.Fatal(err)
	}
	zen.ConfigMu.Lock()
	previousZen := zen.Config
	zen.Config = &zen.ConfigData{Enabled: true, BaseURL: "http://opencode.namespace-policy.test/inference/v1"}
	zen.ConfigMu.Unlock()
	t.Cleanup(func() {
		zen.ConfigMu.Lock()
		zen.Config = previousZen
		zen.ConfigMu.Unlock()
	})
}

func namespaceListingByID(t *testing.T, onlyFree bool) map[string]map[string]any {
	t.Helper()
	rows := map[string]map[string]any{}
	for _, row := range BuildModelsList(onlyFree) {
		id, _ := row["id"].(string)
		if _, exists := rows[id]; exists {
			t.Fatalf("client catalog repeats model identity %q", id)
		}
		rows[id] = row
	}
	return rows
}

func TestProviderModelNamespaceCatalogAndAccess(t *testing.T) {
	installNamespacePolicyFixture(t)
	rows := namespaceListingByID(t, false)
	for _, target := range []struct{ prefix, providerID, ownedBy, source string }{
		{"as", namespacePolicyProviderID, "AntSeed", "provider"},
		{"op", "opencode", "opencode", "zen"},
	} {
		id := target.prefix + "/" + namespacePolicyModel
		row := rows[id]
		if row == nil || row["alias_of"] != namespacePolicyModel || row["providerId"] != target.providerID || row["owned_by"] != target.ownedBy || row["source"] != target.source {
			t.Errorf("catalog identity %q = %+v, want provider %s and unchanged upstream ID", id, row, target.providerID)
		}
	}
	if row := rows["as/"+namespacePolicyModel]; row != nil {
		if row["providerName"] != "AntSeed" {
			t.Errorf("AntSeed alias borrowed a different provider name: %+v", row)
		}
		if row["context_length"] == 32768 || reflect.DeepEqual(row["thinking_levels"], []string{"low"}) {
			t.Errorf("AntSeed alias borrowed OpenCode capability metadata: %+v", row)
		}
	}
	if row := rows["op/"+namespacePolicyModel]; row == nil || row["context_length"] != 32768 || !reflect.DeepEqual(row["thinking_levels"], []string{"low"}) {
		t.Errorf("OpenCode alias lost its offering metadata: %+v", row)
	}
	if access := ModelRouteAccess("as/" + namespacePolicyModel); access.Class != domain.AccessPaidAPI || ModelFreeOnlyEligible("as/"+namespacePolicyModel) {
		t.Errorf("AntSeed alias inherited shared free model access: %+v", access)
	}
	if access := ModelRouteAccess("op/" + namespacePolicyModel); access.Class != domain.AccessFree || !ModelFreeOnlyEligible("op/"+namespacePolicyModel) {
		t.Errorf("OpenCode alias lost its own free access: %+v", access)
	}
	freeRows := namespaceListingByID(t, true)
	if freeRows["as/"+namespacePolicyModel] != nil || freeRows["op/"+namespacePolicyModel] == nil {
		t.Fatal("free-only catalog did not isolate paid AntSeed from free OpenCode")
	}
}

func TestProviderModelNamespacePoliciesPreserveProviderIdentity(t *testing.T) {
	installNamespacePolicyFixture(t)
	asModel, opModel := "as/"+namespacePolicyModel, "op/"+namespacePolicyModel
	cfg := getProxyConfig().Clone()
	cfg.ModelChain = []string{asModel, opModel, asModel}
	cfg.ExcludedModels = []string{"as/*"}
	config.Publish(cfg)
	setProxyConfig(cfg)
	if _, reject := ResolveClientModel(asModel); reject == "" {
		t.Error("provider-specific exclusion failed to reject the AntSeed alias")
	}
	if _, reject := ResolveClientModel(opModel); reject != "" {
		t.Errorf("AntSeed exclusion leaked to OpenCode: %s", reject)
	}
	rows := namespaceListingByID(t, false)
	if rows[asModel] != nil || rows[opModel] == nil {
		t.Error("provider-specific exclusion leaked across catalog aliases")
	}
	cfg = cfg.Clone()
	cfg.ExcludedModels = nil
	config.Publish(cfg)
	setProxyConfig(cfg)
	if chain := ModelFallbackChain("primary"); !reflect.DeepEqual(chain, []string{"primary", asModel, opModel}) {
		t.Errorf("fallback chain lost alias identities or order: %v", chain)
	}
	asParams := map[string]any{"model": asModel, "reasoning_effort": "high"}
	opParams := map[string]any{"model": opModel, "reasoning_effort": "high"}
	ApplyThinkingPolicy(asParams, asModel)
	ApplyThinkingPolicy(opParams, opModel)
	if _, exists := asParams["reasoning_effort"]; !exists {
		t.Error("AntSeed request was filtered through OpenCode thinking capabilities")
	}
	if _, exists := opParams["reasoning_effort"]; exists {
		t.Error("OpenCode alias did not enforce its own supported thinking levels")
	}
}

func TestProviderModelNamespaceCatalogRemovesOnlyUnavailableProvider(t *testing.T) {
	installNamespacePolicyFixture(t)
	provider := providers.Find(namespacePolicyProviderID)
	provider.Enabled = false
	providers.Upsert(provider)
	rows := namespaceListingByID(t, false)
	if rows["as/"+namespacePolicyModel] != nil || rows["op/"+namespacePolicyModel] == nil {
		t.Error("disabling AntSeed hid or advertised the wrong provider alias")
	}
	providers.Delete(namespacePolicyProviderID)
	rows = namespaceListingByID(t, false)
	if rows["as/"+namespacePolicyModel] != nil || rows["op/"+namespacePolicyModel] == nil {
		t.Error("deleting AntSeed hid or advertised the wrong provider alias")
	}
	if config.Live().ProviderModelPrefixes["as"] != namespacePolicyProviderID {
		t.Error("provider deletion released its reserved model namespace")
	}
}
