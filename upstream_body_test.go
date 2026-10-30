package main

import "testing"

func TestBuildUpstreamBodyNoneDropsReasoningEffort(t *testing.T) {
	body := buildUpstreamBody(map[string]any{
		"model":            "m1",
		"reasoning_effort": "none",
	}, false)
	if _, ok := body["reasoning_effort"]; ok {
		t.Errorf("reasoning_effort=none must be dropped, got %v", body["reasoning_effort"])
	}

	// 默认仍然下发 high
	body2 := buildUpstreamBody(map[string]any{"model": "m1"}, false)
	if body2["reasoning_effort"] != defaultReasoningEffort {
		t.Errorf("default reasoning_effort = %v, want %v", body2["reasoning_effort"], defaultReasoningEffort)
	}
}

// TestBuildUpstreamBodyClampsMaxTokens 上游（OpenRouter/Meta）要求输出 token >= 16：
// 0 视为未设置、1~15 兜到默认值，>=16 原样透传。背景：ZCode 等客户端的后台
// 小任务会发很小的 max_tokens，触发 muse-spark 400 且错误被回退链吞掉。
func TestBuildUpstreamBodyClampsMaxTokens(t *testing.T) {
	cases := []struct {
		name   string
		params map[string]any
		want   int
	}{
		{"absent", map[string]any{"model": "m1"}, defaultMaxTokens},
		{"zero", map[string]any{"model": "m1", "max_tokens": float64(0)}, defaultMaxTokens},
		{"tiny", map[string]any{"model": "m1", "max_tokens": float64(8)}, defaultMaxTokens},
		{"completion-tiny", map[string]any{"model": "m1", "max_completion_tokens": float64(8)}, defaultMaxTokens},
		{"boundary-16", map[string]any{"model": "m1", "max_tokens": float64(16)}, 16},
		{"normal", map[string]any{"model": "m1", "max_tokens": float64(1024)}, 1024},
		{"completion-int", map[string]any{"model": "m1", "max_completion_tokens": 2048}, 2048},
		{"max-tokens-precedes-completion", map[string]any{"model": "m1", "max_tokens": 1024, "max_completion_tokens": 2048}, 1024},
	}
	for _, tc := range cases {
		body := buildUpstreamBody(tc.params, false)
		if got, _ := body["max_tokens"].(int); got != tc.want {
			t.Errorf("%s: max_tokens = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestBuildUpstreamBodyPreservesAnthropicIntegerMaxTokens(t *testing.T) {
	params := anthropicToOpenAI(anthropicReq{
		Model:     "m1",
		MaxTokens: 1024,
		Messages:  []anthropicMsg{{Role: "user", Content: "hi"}},
	})

	body := buildUpstreamBody(params, false)
	if got, _ := body["max_tokens"].(int); got != 1024 {
		t.Fatalf("max_tokens = %d, want 1024", got)
	}
}
