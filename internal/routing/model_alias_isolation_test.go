package routing

import (
	"testing"

	"routenest/internal/config"
	"routenest/internal/providers"
)

func TestAliasProviderUsesRegisteredNamespace(t *testing.T) {
	installNamespacePolicyFixture(t)
	cfg := getProxyConfig().Clone()
	cfg.ModelAliases = []config.ModelAlias{{Alias: "antseed-alias", Target: namespacePolicyModel, Provider: "AntSeed"}}
	setProxyConfig(cfg)
	resolved, reject := ResolveClientModel("antseed-alias")
	if reject != "" || resolved != "as/"+namespacePolicyModel {
		t.Fatalf("provider alias = %q, reject=%q", resolved, reject)
	}
}

func TestAliasProviderCannotBecomeAnotherProvidersRawID(t *testing.T) {
	installNamespacePolicyFixture(t)
	providers.Upsert(&providers.CustomProvider{
		ID: "prov_alias_literal", Name: "Other", BaseURL: "http://alias-literal.invalid/v1", Enabled: true,
		ModelIDs: []string{"model@missing", "model@AntSeed"},
	})
	for _, provider := range []string{"missing", "Other"} {
		cfg := getProxyConfig().Clone()
		cfg.ModelAliases = []config.ModelAlias{{Alias: "mandatory-provider", Target: "model", Provider: provider}}
		setProxyConfig(cfg)
		if _, reject := ResolveClientModel("mandatory-provider"); reject == "" {
			t.Fatalf("unregistered mandatory provider %q acquired another offering's raw identity", provider)
		}
	}
	// A registered provider also retains its namespace when a sibling declares
	// the literal spelling of the old name-pin representation.
	cfg := getProxyConfig().Clone()
	cfg.ModelAliases = []config.ModelAlias{{Alias: "registered-provider", Target: "model", Provider: "AntSeed"}}
	setProxyConfig(cfg)
	resolved, reject := ResolveClientModel("registered-provider")
	if reject != "" || resolved != "as/model" {
		t.Fatalf("registered provider alias lost identity: %q, reject=%q", resolved, reject)
	}
}
