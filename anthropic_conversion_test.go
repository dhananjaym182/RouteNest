package main

import (
	"encoding/json"
	"testing"
)

// ============ Anthropic thinking 映射测试 ============

func TestAnthropicThinkingMapping(t *testing.T) {
	cases := []struct {
		name     string
		thinking string
		want     any // expected reasoning_effort in translated request
	}{
		{"disabled→none", `{"type":"disabled"}`, "none"},
		{"enabled→high", `{"type":"enabled","budget_tokens":8000}`, "high"},
		{"adaptive→high", `{"type":"adaptive"}`, "high"},
		{"absent→unset", "", nil},
	}
	for _, c := range cases {
		req := anthropicReq{
			Model:     "m1",
			MaxTokens: 100,
			Messages:  []anthropicMsg{{Role: "user", Content: "hi"}},
		}
		if c.thinking != "" {
			req.Thinking = json.RawMessage(c.thinking)
		}
		out := anthropicToOpenAI(req)
		got, ok := out["reasoning_effort"]
		if c.want == nil {
			if ok {
				t.Errorf("[%s] reasoning_effort should be absent, got %v", c.name, got)
			}
			continue
		}
		if got != c.want {
			t.Errorf("[%s] reasoning_effort = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestOpenAIToAnthropicThinkingBlock(t *testing.T) {
	out := openAIToAnthropic(map[string]any{
		"model": "m1",
		"choices": []any{map[string]any{
			"finish_reason": "stop",
			"message": map[string]any{
				"role":              "assistant",
				"content":           "answer",
				"reasoning_content": "thinking hard",
			},
		}},
	})
	blocks, ok := out["content"].([]any)
	if !ok || len(blocks) != 2 {
		t.Fatalf("want thinking+text blocks, got %v", out["content"])
	}
	first, _ := blocks[0].(map[string]any)
	if first["type"] != "thinking" || first["thinking"] != "thinking hard" {
		t.Errorf("first block should be thinking, got %v", first)
	}
	second, _ := blocks[1].(map[string]any)
	if second["type"] != "text" || second["text"] != "answer" {
		t.Errorf("second block should be text, got %v", second)
	}

	// 无 reasoning_content 时保持单 text 块
	out2 := openAIToAnthropic(map[string]any{
		"model": "m1",
		"choices": []any{map[string]any{
			"finish_reason": "stop",
			"message":       map[string]any{"role": "assistant", "content": "plain"},
		}},
	})
	blocks2, _ := out2["content"].([]any)
	if len(blocks2) != 1 {
		t.Fatalf("want single text block, got %v", blocks2)
	}
	if b, _ := blocks2[0].(map[string]any); b["type"] != "text" {
		t.Errorf("block type = %v, want text", b["type"])
	}
}
