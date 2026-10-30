package egress

import (
	"errors"
	"testing"

	"cline-go-proxy/internal/config"
)

func installRuleConfig(t *testing.T, rules ...config.RequestErrorRule) {
	t.Helper()
	oldProvider := ConfigProvider
	cfg := config.DefaultProxy()
	cfg.RequestErrorRules = rules
	cfg.Normalize()
	ConfigProvider = func() *config.Proxy { return cfg }
	t.Cleanup(func() { ConfigProvider = oldProvider })
}

func TestRuleDecision(t *testing.T) {
	installRuleConfig(t,
		config.RequestErrorRule{StatusCodes: []int{429}, Match: "quota window", Action: config.ErrorActionStopAndCooldown},
		config.RequestErrorRule{Match: "server_reported_error_type", Action: config.ErrorActionContinue},
	)

	if stop, cool, ok := RuleDecision(NewAPIError(429, "quota window reset in 6h")); !stop || !cool || !ok {
		t.Fatalf("429 quota = stop:%v cool:%v ok:%v", stop, cool, ok)
	}
	if stop, cool, ok := RuleDecision(NewAPIError(503, "x server_reported_error_type y")); stop || cool || !ok {
		t.Fatalf("continue rule = stop:%v cool:%v ok:%v", stop, cool, ok)
	}
	if _, _, ok := RuleDecision(NewAPIError(400, "other")); ok {
		t.Fatal("unmatched API error must not match")
	}
	if _, _, ok := RuleDecision(errors.New("plain")); ok {
		t.Fatal("non-API error must not match")
	}
	if _, _, ok := RuleDecision(WrapStop(NewAPIError(429, "quota window"))); ok {
		t.Fatal("already-ruled error must not re-match")
	}
}
