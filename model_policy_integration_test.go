package main

import (
	"cline-go-proxy/internal/routing"
	"fmt"
	"strings"
	"testing"

	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/config"
)

func policyTestPool(models ...Model) func() {
	oldPool := accounts.Pool
	accounts.Pool = &AccountPool{Models: models}
	return func() { accounts.Pool = oldPool }
}

func TestBuildModelsListAppliesPolicy(t *testing.T) {
	defer policyTestPool(
		Model{ID: "gpt-5", ThinkingLevels: []string{"low"}, InputModalities: []string{"text", "image"}},
		Model{ID: "gpt-4-mini"},
	)()

	cfg := defaultProxyConfig()
	cfg.ExcludedModels = []string{"gpt-4-*"}
	cfg.ModelAliases = []config.ModelAlias{{Alias: "fast", Target: "gpt-5"}}
	setProxyConfig(cfg)
	defer setProxyConfig(defaultProxyConfig())

	list := routing.BuildModelsList(false)
	var ids []string
	var entry map[string]any
	for _, e := range list {
		id, _ := e["id"].(string)
		ids = append(ids, id)
		if id == "gpt-5" {
			entry = e
		}
	}
	joined := strings.Join(ids, ",")
	if strings.Contains(joined, "gpt-4-mini") {
		t.Fatalf("excluded model must not be listed: %v", ids)
	}
	if !strings.Contains(joined, "fast") {
		t.Fatalf("alias entry missing: %v", ids)
	}
	if entry == nil || fmt.Sprint(entry["thinking_levels"]) != "[low]" || fmt.Sprint(entry["input_modalities"]) != "[text image]" {
		t.Fatalf("capability metadata not exposed: %+v", entry)
	}
}
