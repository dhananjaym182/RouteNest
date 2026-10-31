package routing_test

import (
	"cline-go-proxy/internal/admin"
	"cline-go-proxy/internal/domain"
	"cline-go-proxy/internal/routing"
	"fmt"
	"strings"
	"testing"

	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/config"
)

func policyTestPool(models ...domain.Model) func() {
	oldPool := accounts.Pool
	accounts.Pool = &domain.AccountPool{Models: models}
	return func() { accounts.Pool = oldPool }
}

func TestBuildModelsListAppliesPolicy(t *testing.T) {
	oldProvider := routing.ConfigProvider
	routing.ConfigProvider = admin.GetProxyConfig
	t.Cleanup(func() { routing.ConfigProvider = oldProvider })
	defer policyTestPool(
		domain.Model{ID: "gpt-5", ThinkingLevels: []string{"low"}, InputModalities: []string{"text", "image"}},
		domain.Model{ID: "gpt-4-mini"},
	)()

	cfg := admin.DefaultProxyConfig()
	cfg.ExcludedModels = []string{"gpt-4-*"}
	cfg.ModelAliases = []config.ModelAlias{{Alias: "fast", Target: "gpt-5"}}
	admin.SetProxyConfig(cfg)
	defer admin.SetProxyConfig(admin.DefaultProxyConfig())

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
