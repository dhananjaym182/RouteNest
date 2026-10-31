package qoder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// collapseTool is one accumulated tool call in a collapsed response.
type collapseTool struct {
	ID        string
	Name      string
	Arguments string
}

// usageAcc is the normalized token accounting accumulated from chunks.
type usageAcc struct {
	Prompt     int64
	Completion int64
	Total      int64
	Cached     int64
	Valid      bool
}

// collapseAcc accumulates OpenAI chunks into one chat.completion. The merging
// semantics mirror zen.go's collapseZenStreamResponse so downstream consumers
// see the same shape regardless of upstream.
type collapseAcc struct {
	ID        string
	Model     string
	Created   int64
	Content   string
	Reasoning string
	Finish    string
	Tools     []collapseTool
	Usage     usageAcc
}

// collapseStream consumes the upstream envelope stream and returns one JSON
// chat.completion response.
func collapseStream(resp *http.Response, model string, cancel context.CancelFunc) (*http.Response, error) {
	defer cancel()
	defer resp.Body.Close()

	acc := &collapseAcc{Model: model, Created: time.Now().Unix()}
	err := pumpEnvelopes(resp.Body, model, func(payload string) error {
		acc.consume(payload)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("qoder stream read: %w", err)
	}
	return acc.response(resp.Request)
}

func (a *collapseAcc) consume(payload string) {
	if payload == "" || payload == "[DONE]" {
		return
	}
	var chunk struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Created int64  `json:"created"`
		Choices []struct {
			FinishReason any `json:"finish_reason"`
			Delta        struct {
				Content          string `json:"content"`
				ReasoningContent any    `json:"reasoning_content"`
				ToolCalls        []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
		Usage map[string]any `json:"usage"`
	}
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return
	}
	if chunk.ID != "" {
		a.ID = chunk.ID
	}
	if chunk.Model != "" {
		a.Model = chunk.Model
	}
	if chunk.Created != 0 {
		a.Created = chunk.Created
	}
	if len(chunk.Choices) > 0 {
		choice := chunk.Choices[0]
		a.Content += choice.Delta.Content
		if rc, ok := choice.Delta.ReasoningContent.(string); ok {
			a.Reasoning += rc
		}
		if fr, ok := choice.FinishReason.(string); ok && fr != "" {
			a.Finish = fr
		}
		for _, tc := range choice.Delta.ToolCalls {
			for len(a.Tools) <= tc.Index {
				a.Tools = append(a.Tools, collapseTool{})
			}
			t := &a.Tools[tc.Index]
			if tc.ID != "" {
				t.ID = tc.ID
			}
			t.Name += tc.Function.Name
			t.Arguments += tc.Function.Arguments
		}
	}
	if chunk.Usage != nil {
		a.Usage = mergeUsage(a.Usage, parseUsage(chunk.Usage))
	}
}

// response renders the accumulator as a single chat.completion JSON response.
func (a *collapseAcc) response(req *http.Request) (*http.Response, error) {
	msg := map[string]any{"role": "assistant", "content": a.Content}
	if a.Reasoning != "" {
		msg["reasoning_content"] = a.Reasoning
	}
	if len(a.Tools) > 0 {
		calls := make([]any, 0, len(a.Tools))
		for i, t := range a.Tools {
			if t.ID == "" {
				t.ID = fmt.Sprintf("call_%d", i)
			}
			calls = append(calls, map[string]any{
				"id":       t.ID,
				"type":     "function",
				"function": map[string]any{"name": t.Name, "arguments": t.Arguments},
			})
		}
		msg["tool_calls"] = calls
	}
	finish := a.Finish
	if finish == "" {
		if len(a.Tools) > 0 {
			finish = "tool_calls"
		} else {
			finish = "stop"
		}
	}
	id := a.ID
	if id == "" {
		id = "chatcmpl-" + randHex(12)
	}
	out := map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": a.Created,
		"model":   a.Model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       msg,
			"finish_reason": finish,
		}},
		"usage": map[string]any{
			"prompt_tokens":         a.Usage.Prompt,
			"completion_tokens":     a.Usage.Completion,
			"total_tokens":          a.Usage.Total,
			"prompt_tokens_details": map[string]any{"cached_tokens": a.Usage.Cached},
		},
	}
	data, err := marshalNoEscape(out)
	if err != nil {
		return nil, fmt.Errorf("qoder collapse: %w", err)
	}
	return &http.Response{
		Status:        "200 OK",
		StatusCode:    http.StatusOK,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(bytes.NewReader(data)),
		ContentLength: int64(len(data)),
		Request:       req,
	}, nil
}

// parseUsage reads OpenAI/Anthropic-shaped usage counters.
func parseUsage(v any) usageAcc {
	m, ok := v.(map[string]any)
	if !ok {
		return usageAcc{}
	}
	read := func(keys ...string) int64 {
		for _, key := range keys {
			if n, ok := numberToInt(m[key]); ok && n >= 0 {
				return int64(n)
			}
		}
		return 0
	}
	readNested := func(parent string, keys ...string) int64 {
		details, ok := m[parent].(map[string]any)
		if !ok {
			return 0
		}
		for _, key := range keys {
			if n, ok := numberToInt(details[key]); ok && n >= 0 {
				return int64(n)
			}
		}
		return 0
	}
	u := usageAcc{
		Prompt:     read("prompt_tokens", "input_tokens"),
		Completion: read("completion_tokens", "output_tokens"),
		Total:      read("total_tokens"),
		Cached:     readNested("prompt_tokens_details", "cached_tokens"),
	}
	if u.Cached == 0 {
		u.Cached = readNested("input_tokens_details", "cached_tokens")
	}
	u.Valid = u.Prompt != 0 || u.Completion != 0 || u.Total != 0
	if u.Total == 0 {
		u.Total = u.Prompt + u.Completion
	}
	return u
}

// mergeUsage keeps the latest non-zero counters, matching zen's merge semantics.
func mergeUsage(current, next usageAcc) usageAcc {
	if !next.Valid {
		return current
	}
	if next.Prompt != 0 {
		current.Prompt = next.Prompt
	}
	if next.Completion != 0 {
		current.Completion = next.Completion
	}
	if next.Total != 0 {
		current.Total = next.Total
	}
	if next.Cached != 0 {
		current.Cached = next.Cached
	}
	current.Valid = current.Valid || next.Valid
	if current.Total == 0 && (current.Prompt != 0 || current.Completion != 0) {
		current.Total = current.Prompt + current.Completion
	}
	return current
}
