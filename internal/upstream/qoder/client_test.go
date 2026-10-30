package qoder

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// testCatalog deliberately includes a disabled entry (still usable for chat)
// and an entry without a key (must be skipped).
const testCatalog = `{"chat":[
	{"key":"auto","display_name":"Auto","max_input_tokens":131072,"max_output_tokens":4096,"is_reasoning":false,"is_vl":false,"description":"","enable":true,"source":"system"},
	{"key":"hidden","display_name":"Hidden","max_input_tokens":65536,"max_output_tokens":1024,"is_reasoning":true,"is_vl":false,"description":"","enable":false},
	{"display_name":"NoKey","enable":true}
]}`

func testCredsFor(serverURL string) Creds {
	return Creds{
		UserID:    "user-" + serverURL,
		AuthToken: "dt-test-token",
		Email:     "dev@example.com",
		Name:      "Dev",
		MachineID: "11111111-2222-4333-8444-555555555555",
		BaseURL:   serverURL,
	}
}

// newFakeUpstream serves the model catalog and delegates chat requests to
// chatHandler. It returns the server plus a counter of catalog fetches.
func newFakeUpstream(t *testing.T, chatHandler func(w http.ResponseWriter, r *http.Request, body []byte)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var listCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/algo/api/v2/model/list", func(w http.ResponseWriter, r *http.Request) {
		listCalls.Add(1)
		if r.Method != http.MethodGet {
			t.Errorf("model list method = %s, want GET", r.Method)
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer COSY.") {
			t.Errorf("model list Authorization = %q, want Bearer COSY. prefix", got)
		}
		if got := r.Header.Get("Cosy-Bodylength"); got != "0" {
			t.Errorf("model list Cosy-Bodylength = %q, want 0", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, testCatalog)
	})
	mux.HandleFunc("/algo/api/v2/service/pro/sse/agent_chat_generation", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read chat body: %v", err)
		}
		if chatHandler != nil {
			chatHandler(w, r, body)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &listCalls
}

// writeEnvelope emits one Qoder SSE frame carrying an inner OpenAI payload.
func writeEnvelope(w io.Writer, status int, inner string) {
	b, err := json.Marshal(map[string]any{"statusCodeValue": status, "body": inner})
	if err != nil {
		panic(err)
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return string(b)
}

func TestModels(t *testing.T) {
	srv, listCalls := newFakeUpstream(t, nil)
	creds := testCredsFor(srv.URL)
	ctx := context.Background()

	models, err := Models(ctx, srv.Client(), creds)
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	// enable:false entries are included; entries without a key are skipped.
	want := []string{"auto", "hidden"}
	if len(models) != len(want) || models[0] != want[0] || models[1] != want[1] {
		t.Fatalf("Models = %v, want %v", models, want)
	}

	// Second call is served from the one-hour cache.
	if _, err := Models(ctx, srv.Client(), creds); err != nil {
		t.Fatalf("Models (cached): %v", err)
	}
	if got := listCalls.Load(); got != 1 {
		t.Errorf("catalog fetches = %d, want 1", got)
	}

	InvalidateCatalog(creds.BaseURL, creds.UserID)
	if _, err := Models(ctx, srv.Client(), creds); err != nil {
		t.Fatalf("Models (after invalidate): %v", err)
	}
	if got := listCalls.Load(); got != 2 {
		t.Errorf("catalog fetches after invalidate = %d, want 2", got)
	}
}

func TestChatStreamUnwrapsEnvelope(t *testing.T) {
	srv, _ := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("chat Content-Type = %q", got)
		}
		if got := r.Header.Get("X-Model-Key"); got != "auto" {
			t.Errorf("X-Model-Key = %q, want auto", got)
		}
		if got := r.Header.Get("Cosy-Bodylength"); got != strconv.Itoa(len(body)) {
			t.Errorf("Cosy-Bodylength = %q, want %d", got, len(body))
		}
		if got, want := r.Header.Get("Cosy-Sigpath"), "/api/v2/service/pro/sse/agent_chat_generation"; got != want {
			t.Errorf("Cosy-Sigpath = %q, want %q", got, want)
		}

		// The WAF-bypass encoding must decode back to valid JSON with the
		// Qoder request shape.
		decoded, err := decodeBody(string(body))
		if err != nil {
			t.Errorf("chat body does not decode: %v", err)
			return
		}
		var payload map[string]any
		if err := json.Unmarshal(decoded, &payload); err != nil {
			t.Errorf("decoded chat body is not JSON: %v (%s)", err, decoded)
			return
		}
		if got := payload["system"]; got != "be brief\n\nbe nice" {
			t.Errorf("system = %v, want hoisted system text", got)
		}
		messages, _ := payload["messages"].([]any)
		if len(messages) != 1 {
			t.Errorf("messages = %v, want only the user turn", payload["messages"])
		} else if msg, _ := messages[0].(map[string]any); msg["role"] != "user" || msg["content"] != "hello" {
			t.Errorf("messages[0] = %v", messages[0])
		}
		if params, _ := payload["parameters"].(map[string]any); params["max_tokens"] != float64(4096) {
			t.Errorf("parameters.max_tokens = %v, want 4096", payload["parameters"])
		}
		if mc, _ := payload["model_config"].(map[string]any); mc["key"] != "auto" {
			t.Errorf("model_config.key = %v, want auto", payload["model_config"])
		}
		if ctxBlock, _ := payload["chat_context"].(map[string]any); ctxBlock["text"] != "hello" {
			t.Errorf("chat_context.text = %v, want hello", payload["chat_context"])
		}
		if payload["stream"] != true {
			t.Errorf("stream = %v, want true (upstream always streams)", payload["stream"])
		}
		if rs, _ := payload["request_set_id"].(string); rs == "" || rs != payload["chat_record_id"] {
			t.Errorf("request_set_id/chat_record_id mismatch: %v / %v", payload["request_set_id"], payload["chat_record_id"])
		}

		w.Header().Set("Content-Type", "text/event-stream")
		writeEnvelope(w, 200, `{"choices":[{"delta":{"content":"hi"}}]}`)
		writeEnvelope(w, 200, "[DONE]")
	})

	creds := testCredsFor(srv.URL)
	params := map[string]any{
		"model": "qoder/auto",
		"messages": []any{
			map[string]any{"role": "system", "content": "be brief"},
			map[string]any{"role": "system", "content": "be nice"},
			map[string]any{"role": "user", "content": "hello"},
		},
	}
	resp, err := Chat(context.Background(), srv.Client(), creds, params, true)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	want := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"
	if string(data) != want {
		t.Errorf("stream = %q, want %q", data, want)
	}
}

func TestChatStreamEnvelopeError(t *testing.T) {
	srv, _ := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeEnvelope(w, 500, "boom")
	})
	creds := testCredsFor(srv.URL)
	params := map[string]any{
		"model":    "auto",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}
	resp, err := Chat(context.Background(), srv.Client(), creds, params, true)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	out := string(data)
	if !strings.Contains(out, "[qoder error 500: boom]") {
		t.Errorf("missing synthetic error chunk: %q", out)
	}
	if !strings.Contains(out, `"object":"chat.completion.chunk"`) {
		t.Errorf("error chunk is not an OpenAI chunk: %q", out)
	}
	if !strings.HasSuffix(out, "data: [DONE]\n\n") {
		t.Errorf("stream must end with [DONE]: %q", out)
	}
	if got := strings.Count(out, "data: [DONE]"); got != 1 {
		t.Errorf("DONE emitted %d times, want 1", got)
	}
	if strings.Contains(out, "boom\"}") {
		t.Errorf("envelope must be unwrapped, not passed through: %q", out)
	}
}

func TestChatCollapse(t *testing.T) {
	srv, _ := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := func(delta map[string]any, finish any) string {
			choice := map[string]any{"index": 0, "delta": delta}
			if finish != nil {
				choice["finish_reason"] = finish
			}
			return mustJSON(t, map[string]any{"choices": []any{choice}})
		}
		toolDelta := func(index int, id, name, args string) map[string]any {
			fn := map[string]any{"arguments": args}
			if name != "" {
				fn["name"] = name
			}
			call := map[string]any{"index": index, "function": fn}
			if id != "" {
				call["id"] = id
			}
			return map[string]any{"tool_calls": []any{call}}
		}

		writeEnvelope(w, 200, mustJSON(t, map[string]any{
			"id":      "chatcmpl-1",
			"object":  "chat.completion.chunk",
			"created": 123,
			"model":   "auto",
			"choices": []any{map[string]any{
				"index": 0,
				"delta": map[string]any{"role": "assistant", "content": "he"},
			}},
		}))
		writeEnvelope(w, 200, chunk(map[string]any{"content": "llo", "reasoning_content": "why"}, nil))
		writeEnvelope(w, 200, chunk(toolDelta(0, "call_1", "read", `{"pa`), nil))
		last := chunk(toolDelta(0, "", "", `th":"x"}`), "tool_calls")
		var lastObj map[string]any
		_ = json.Unmarshal([]byte(last), &lastObj)
		lastObj["usage"] = map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
		writeEnvelope(w, 200, mustJSON(t, lastObj))
		writeEnvelope(w, 200, "[DONE]")
	})

	creds := testCredsFor(srv.URL)
	params := map[string]any{
		"model":    "auto",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}
	resp, err := Chat(context.Background(), srv.Client(), creds, params, false)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	var out struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		Model   string `json:"model"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Role             string `json:"role"`
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			Prompt     int64 `json:"prompt_tokens"`
			Completion int64 `json:"completion_tokens"`
			Total      int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode collapsed response: %v", err)
	}

	if out.Object != "chat.completion" {
		t.Errorf("object = %q, want chat.completion", out.Object)
	}
	if out.ID != "chatcmpl-1" || out.Model != "auto" || out.Created != 123 {
		t.Errorf("id/model/created = %q/%q/%d", out.ID, out.Model, out.Created)
	}
	if len(out.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(out.Choices))
	}
	choice := out.Choices[0]
	if choice.Message.Role != "assistant" || choice.Message.Content != "hello" {
		t.Errorf("message = %q/%q, want assistant/hello", choice.Message.Role, choice.Message.Content)
	}
	if choice.Message.ReasoningContent != "why" {
		t.Errorf("reasoning_content = %q, want why", choice.Message.ReasoningContent)
	}
	if choice.FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q, want tool_calls", choice.FinishReason)
	}
	if len(choice.Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls = %d, want 1", len(choice.Message.ToolCalls))
	}
	tc := choice.Message.ToolCalls[0]
	if tc.ID != "call_1" || tc.Type != "function" || tc.Function.Name != "read" {
		t.Errorf("tool call = %+v", tc)
	}
	if tc.Function.Arguments != `{"path":"x"}` {
		t.Errorf("tool arguments = %q, want merged JSON", tc.Function.Arguments)
	}
	if out.Usage.Prompt != 10 || out.Usage.Completion != 5 || out.Usage.Total != 15 {
		t.Errorf("usage = %+v", out.Usage)
	}
}

func TestChatMissingModelConfigIsHardError(t *testing.T) {
	srv, listCalls := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		t.Error("chat endpoint must not be called without a model_config")
	})
	creds := testCredsFor(srv.URL)
	params := map[string]any{
		"model":    "qoder/ghost",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}
	_, err := Chat(context.Background(), srv.Client(), creds, params, true)
	if err == nil {
		t.Fatal("expected a hard error for a model missing from the catalog")
	}
	msg := err.Error()
	if !strings.Contains(msg, "ghost") || !strings.Contains(msg, "model_config") {
		t.Errorf("error = %q, want model key + model_config mention", msg)
	}
	// One cached lookup, then exactly one forced refresh.
	if got := listCalls.Load(); got != 2 {
		t.Errorf("catalog fetches = %d, want 2 (initial + forced refresh)", got)
	}
}
