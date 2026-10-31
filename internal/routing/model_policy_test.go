package routing

import (
	"strings"
	"testing"

	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/config"
	"cline-go-proxy/internal/domain"
)

func policyTestPool(models ...Model) func() {
	oldPool := accounts.Pool
	accounts.Pool = &AccountPool{Models: models}
	return func() { accounts.Pool = oldPool }
}

func TestResolveClientModelAlias(t *testing.T) {
	defer policyTestPool(Model{ID: "gpt-5", Provider: "openai"})()

	cfg := defaultProxyConfig()
	cfg.ModelAliases = []config.ModelAlias{{Alias: "fast", Target: "gpt-5", Provider: "OpenRouter"}}
	setProxyConfig(cfg)
	defer setProxyConfig(defaultProxyConfig())

	got, reject := ResolveClientModel("fast")
	if reject != "" || got != "gpt-5@OpenRouter" {
		t.Fatalf("alias rewrite = %q reject=%q, want gpt-5@OpenRouter", got, reject)
	}
}

func TestResolveClientModelAliasShadowing(t *testing.T) {
	defer policyTestPool(Model{ID: "fast", Provider: "openai"}, Model{ID: "gpt-5"})()

	cfg := defaultProxyConfig()
	cfg.ModelAliases = []config.ModelAlias{
		{Alias: "fast", Target: "gpt-5"},
		{Alias: "strong", Target: "gpt-5", Force: true},
	}
	setProxyConfig(cfg)
	defer setProxyConfig(defaultProxyConfig())

	if got, _ := ResolveClientModel("fast"); got != "fast" {
		t.Fatalf("non-forced alias must not shadow a real model, got %q", got)
	}
	if got, _ := ResolveClientModel("strong"); got != "gpt-5" {
		t.Fatalf("forced alias should rewrite, got %q", got)
	}
}

func TestResolveClientModelExclusion(t *testing.T) {
	defer policyTestPool(Model{ID: "gpt-5"}, Model{ID: "gpt-4-mini"})()

	cfg := defaultProxyConfig()
	cfg.ExcludedModels = []string{"gpt-4*"}
	cfg.ModelAliases = []config.ModelAlias{{Alias: "cheap", Target: "gpt-4-mini"}}
	setProxyConfig(cfg)
	defer setProxyConfig(defaultProxyConfig())

	if _, reject := ResolveClientModel("gpt-4o"); reject == "" {
		t.Fatal("wildcard exclusion must reject gpt-4o")
	}
	if _, reject := ResolveClientModel("gpt-5"); reject != "" {
		t.Fatalf("allowed model rejected: %v", reject)
	}
	// Exclusion applies after alias resolution too.
	if _, reject := ResolveClientModel("cheap"); !strings.Contains(reject, "excluded") {
		t.Fatalf("alias to excluded target must reject, got %q", reject)
	}
}

func TestResolveClientModelCombosAndFreeUntouched(t *testing.T) {
	defer policyTestPool()()

	cfg := defaultProxyConfig()
	cfg.Combos = []domain.ModelCombo{{ID: "my-combo", Name: "x", Enabled: true, Entries: []domain.ComboEntry{{Model: "gpt-5"}}}}
	cfg.ModelAliases = []config.ModelAlias{{Alias: "my-combo", Target: "other", Force: true}}
	setProxyConfig(cfg)
	defer setProxyConfig(defaultProxyConfig())

	if got, _ := ResolveClientModel("my-combo"); got != "my-combo" {
		t.Fatalf("combo id must not be alias-rewritten, got %q", got)
	}
	if got, _ := ResolveClientModel("free"); got != "free" {
		t.Fatalf("free alias must survive, got %q", got)
	}
}

func TestApplyThinkingPolicy(t *testing.T) {
	defer policyTestPool(Model{ID: "gpt-5", ThinkingLevels: []string{"low", "medium"}})()

	params := map[string]any{"model": "gpt-5", "reasoning_effort": "high"}
	ApplyThinkingPolicy(params, "gpt-5")
	if _, ok := params["reasoning_effort"]; ok {
		t.Fatal("unsupported effort must be dropped")
	}

	params = map[string]any{"model": "gpt-5", "reasoning": map[string]any{"effort": "ultra"}}
	ApplyThinkingPolicy(params, "gpt-5")
	if _, ok := params["reasoning"]; ok {
		t.Fatal("nested unsupported effort must be dropped")
	}

	params = map[string]any{"model": "gpt-5", "reasoning_effort": "Low"}
	ApplyThinkingPolicy(params, "gpt-5")
	if params["reasoning_effort"] != "Low" {
		t.Fatalf("supported effort (case-insensitive) must survive, got %v", params["reasoning_effort"])
	}

	params = map[string]any{"model": "unknown", "reasoning_effort": "high"}
	ApplyThinkingPolicy(params, "unknown")
	if params["reasoning_effort"] != "high" {
		t.Fatal("models without declared levels must keep the client effort")
	}
}
