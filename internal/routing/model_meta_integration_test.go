package routing_test

import (
	"cline-go-proxy/internal/domain"
	"cline-go-proxy/internal/routing"
	"testing"

	"cline-go-proxy/internal/accounts"
)

// routing.ModelMaxOutputLimit：已知表优先，其次池中模型 Output（zen 估值不算硬上限）。
func TestModelMaxOutputLimit(t *testing.T) {
	oldPool := accounts.Pool
	t.Cleanup(func() { accounts.Pool = oldPool })

	accounts.Pool = &domain.AccountPool{Models: []domain.Model{
		{ID: "my-model", Output: 8192},
		{ID: "deepseek-v4-flash-free", Source: "zen", Output: 128000},
	}}

	if got := routing.ModelMaxOutputLimit("google/gemini-3.8-flash"); got != 65536 {
		t.Errorf("table limit = %d, want 65536", got)
	}
	if got := routing.ModelMaxOutputLimit("my-model"); got != 8192 {
		t.Errorf("accounts.Pool model limit = %d, want 8192", got)
	}
	if got := routing.ModelMaxOutputLimit("deepseek-v4-flash-free"); got != 0 {
		t.Errorf("zen model output is a compaction estimate, must not cap (got %d)", got)
	}
	if got := routing.ModelMaxOutputLimit("unknown-model"); got != 0 {
		t.Errorf("unknown model limit = %d, want 0", got)
	}
}

// gemini-3.8-flash 输出预算封顶：默认 128000 / 超大 200000 都收到 65536，
// 低于上限的原样透传；其他模型不受影响。
// 背景：Vercel 网关 vertex 路由 routing.MaxOutputTokens 支持范围 1~65536，
// 128000 直接 400 invalid argument（429 配额限流回退路由后必现）。
// 默认预算已降为 32000（低于 65536 不会触发封顶），这里显式调高环境变量
// 才能走到封顶分支；ROUTENEST_MAX_TOKENS 是 routing.MaxOutputTokens() 的来源。
func TestBuildUpstreamBodyCapsGeminiMaxTokens(t *testing.T) {
	t.Setenv("ROUTENEST_MAX_TOKENS", "128000")
	oldPool := accounts.Pool
	t.Cleanup(func() { accounts.Pool = oldPool })
	accounts.Pool = &domain.AccountPool{}

	cases := []struct {
		name   string
		params map[string]any
		want   int
	}{
		{"gemini-default", map[string]any{"model": "google/gemini-3.8-flash"}, 65536},
		{"gemini-free-alias", map[string]any{"model": "cline-free/gemini-3.8-flash"}, 65536},
		{"gemini-huge-explicit", map[string]any{"model": "google/gemini-3.8-flash", "max_tokens": float64(200000)}, 65536},
		{"gemini-under-limit", map[string]any{"model": "google/gemini-3.8-flash", "max_tokens": float64(1024)}, 1024},
		{"gemini-at-limit", map[string]any{"model": "google/gemini-3.8-flash", "max_tokens": float64(65536)}, 65536},
		{"other-model-unaffected", map[string]any{"model": "z-ai/glm-5.3-flash"}, routing.MaxOutputTokens()},
	}
	for _, tc := range cases {
		body := routing.BuildUpstreamBody(tc.params, false)
		if got, _ := body["max_tokens"].(int); got != tc.want {
			t.Errorf("%s: max_tokens = %d, want %d", tc.name, got, tc.want)
		}
	}
}
