package combos

import (
	"cline-go-proxy/internal/domain"
	"cline-go-proxy/internal/zen"
	"testing"
)

// ============================ 请求日志归因 ============================

func TestApplyComboLogFields(t *testing.T) {
	rl := domain.RequestLog{Model: "c"}
	combo := domain.ModelCombo{ID: "c", Name: "Combo Display"}
	trace := []domain.AttemptLog{
		{Model: "combo-a", Order: 1, Upstream: zen.UpstreamCline, Status: 500, Outcome: "failed", Reason: "API 500: boom"},
		{Model: "combo-b", Order: 2, Upstream: zen.UpstreamOpenCode, Outcome: "success"},
	}
	ApplyLog(&rl, combo, trace, "combo-b")

	if rl.RequestedModel != "c" || rl.ComboID != "c" || rl.ComboName != "Combo Display" {
		t.Fatalf("combo attribution = %+v", rl)
	}
	if rl.Model != "combo-b" {
		t.Fatalf("actual model = %q, want combo-b", rl.Model)
	}
	if !rl.FallbackUsed || rl.FallbackCount != 1 {
		t.Fatalf("fallback used=%v count=%d, want true/1", rl.FallbackUsed, rl.FallbackCount)
	}
	if len(rl.Attempts) != 2 || rl.Attempts[0].Reason == "" {
		t.Fatalf("attempts not recorded: %+v", rl.Attempts)
	}
}
