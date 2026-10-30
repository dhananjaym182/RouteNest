package main

import (
	"encoding/json"
	"testing"
)

// ============ Responses API 转换测试 ============

func TestResponsesToChatStringInput(t *testing.T) {
	out := responsesToChat(map[string]any{
		"model":             "m1",
		"input":             "hello",
		"instructions":      "be brief",
		"max_output_tokens": 500.0,
	})
	msgs := out["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("want system+user, got %d", len(msgs))
	}
	if msgs[0].(map[string]any)["role"] != "system" {
		t.Error("instructions should become first system message")
	}
	if out["max_tokens"] != 500 {
		t.Errorf("max_output_tokens mapping: %v", out["max_tokens"])
	}
}

func TestResponsesToChatToolRoundTrip(t *testing.T) {
	body := map[string]any{
		"model": "m1",
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": "run it"},
			map[string]any{"type": "function_call", "call_id": "fc1", "name": "shell", "arguments": map[string]any{"cmd": "ls"}},
			map[string]any{"type": "function_call_output", "call_id": "fc1", "output": "file.txt"},
		},
		"tools": []any{map[string]any{
			"type": "function", "name": "shell", "description": "run command",
			"parameters": map[string]any{"type": "object"},
		}},
	}
	out := responsesToChat(body)
	msgs := out["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("want user+assistant(tool_call)+tool, got %d msgs", len(msgs))
	}
	tc := msgs[1].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	if tc["id"] != "fc1" {
		t.Errorf("call_id propagation: %v", tc["id"])
	}
	var argsObj map[string]any
	json.Unmarshal([]byte(tc["function"].(map[string]any)["arguments"].(string)), &argsObj)
	if argsObj["cmd"] != "ls" {
		t.Errorf("object arguments marshaled: %v", argsObj)
	}
	if msgs[2].(map[string]any)["role"] != "tool" {
		t.Error("function_call_output -> role tool")
	}
	tools := out["tools"].([]any)[0].(map[string]any)
	fn := tools["function"].(map[string]any)
	if tools["type"] != "function" || fn["name"] != "shell" {
		t.Errorf("flat tool conversion broken: %v", tools)
	}
}

func TestChatToResponsesUsageMapping(t *testing.T) {
	chat := map[string]any{
		"model": "mm",
		"choices": []any{map[string]any{
			"message": map[string]any{"content": "answer text", "tool_calls": []any{
				map[string]any{"id": "c9", "type": "function",
					"function": map[string]any{"name": "f1", "arguments": "{}"}},
			}},
		}},
		"usage": map[string]any{
			"prompt_tokens": float64(11), "completion_tokens": float64(7), "total_tokens": float64(18),
			"prompt_tokens_details": map[string]any{"cached_tokens": float64(3)},
		},
	}
	resp := chatToResponses(chat)
	if resp["object"] != "response" || resp["status"] != "completed" {
		t.Errorf("response envelope: %v/%v", resp["object"], resp["status"])
	}
	outputs := resp["output"].([]any)
	if outputs[0].(map[string]any)["type"] != "message" {
		t.Errorf("first item type: %v", outputs[0])
	}
	if outputs[1].(map[string]any)["call_id"] != "c9" {
		t.Errorf("function_call call_id: %v", outputs[1])
	}
	u := resp["usage"].(map[string]any)
	if u["input_tokens"] != float64(11) || u["output_tokens"] != float64(7) {
		t.Errorf("usage mapping: %v", u)
	}
	if u["input_tokens_details"].(map[string]any)["cached_tokens"] != float64(3) {
		t.Errorf("cached tokens mapping: %v", u)
	}
	if resp["output_text"] != "answer text" {
		t.Errorf("output_text: %v", resp["output_text"])
	}
}

func TestUnwrapDataEnvelope(t *testing.T) {
	in := map[string]any{"data": map[string]any{"choices": []any{}, "id": "abc"}}
	if unwrapDataEnvelope(in)["id"] != "abc" {
		t.Error("envelope should be unwrapped when data has choices/id")
	}
	if unwrapDataEnvelope(map[string]any{"other": 1}) == nil {
		t.Error("non-envelope passthrough")
	}
}

func TestUsageToResponses(t *testing.T) {
	u := usageToResponses(tokenUsage{Prompt: 10, Completion: 5, Cached: 2})
	if u["input_tokens"] != int64(10) || u["output_tokens"] != int64(5) || u["total_tokens"] != int64(15) {
		t.Errorf("aggregate usage: %v", u)
	}
	if u["input_tokens_details"].(map[string]any)["cached_tokens"] != int64(2) {
		t.Errorf("cached detail: %v", u)
	}
}
