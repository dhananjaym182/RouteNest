package qoder

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Creds is the per-connection identity needed to sign requests.
type Creds struct {
	UserID    string // Qoder user id (psd["userId"] on the connection)
	AuthToken string // device access token, "dt-..."
	Email     string
	Name      string
	MachineID string // stable per connection (psd["machineId"]); generated if empty
	BaseURL   string // e.g. "https://api3.qoder.sh" (no trailing slash)
}

// Chat sends an OpenAI-shaped request to Qoder and returns an OpenAI-compatible
// response. The returned *http.Response has an OpenAI SSE body when stream is
// true, and a single collapsed OpenAI JSON chat.completion body when stream is
// false. The caller owns closing the body.
//
// Upstream HTTP failures are passed through unchanged (original status and
// body) so callers can surface the real status; only 2xx envelope streams are
// rewritten.
func Chat(ctx context.Context, hc *http.Client, creds Creds, params map[string]any, stream bool) (*http.Response, error) {
	modelRaw, _ := params["model"].(string)
	modelKey := strings.TrimPrefix(modelRaw, "qoder/")
	if modelKey == "" {
		return nil, fmt.Errorf("qoder: request is missing a model")
	}
	if hc == nil {
		hc = http.DefaultClient
	}

	// Qoder silently downgrades the model when the wrong model_config block is
	// sent, so a catalog miss is fatal rather than a best-effort fallback.
	entry, err := modelConfigFor(ctx, hc, creds, modelKey)
	if err != nil {
		return nil, err
	}

	payload, err := buildChatPayload(creds, params, modelKey, entry)
	if err != nil {
		return nil, err
	}
	plain, err := marshalNoEscape(payload)
	if err != nil {
		return nil, fmt.Errorf("qoder: encode request body: %w", err)
	}
	encoded := encodeBody(plain)

	endpoint := ChatURL(creds.BaseURL)
	headers, err := buildCosyHeaders([]byte(encoded), endpoint, creds)
	if err != nil {
		return nil, err
	}

	// The derived context lets the returned body's Close() abort an upstream
	// read that is otherwise blocked on a quiet connection.
	reqCtx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, strings.NewReader(encoded))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("qoder: build request: %w", err)
	}
	for k, vs := range headers {
		req.Header[k] = vs
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("X-Model-Key", modelKey)
	req.Header.Set("X-Model-Source", modelSource(entry.raw))
	// gzip would break the body signature; force identity.
	req.Header.Set("Accept-Encoding", "identity")

	resp, err := hc.Do(req)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("qoder chat: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Pass the upstream error through untouched, but keep the request
		// context tied to the body lifetime.
		resp.Body = &cancelReadCloser{ReadCloser: resp.Body, cancel: cancel}
		return resp, nil
	}

	displayModel := "qoder/" + modelKey
	if modelRaw != "" {
		displayModel = modelRaw
	}

	if stream {
		return wrapStream(resp, displayModel, cancel), nil
	}
	return collapseStream(resp, displayModel, cancel)
}

// modelSource reads model_config.source, defaulting to "system".
func modelSource(raw json.RawMessage) string {
	var meta struct {
		Source string `json:"source"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil || meta.Source == "" {
		return "system"
	}
	return meta.Source
}

// ============ Request payload ============

type chatParameters struct {
	MaxTokens int `json:"max_tokens"`
}

type modelConfigRef struct {
	Key         string `json:"key"`
	IsReasoning bool   `json:"is_reasoning"`
}

type chatContextExtra struct {
	Context         []any          `json:"context"`
	ModelConfig     modelConfigRef `json:"modelConfig"`
	OriginalContent string         `json:"originalContent"`
}

type chatContext struct {
	ChatPrompt string           `json:"chatPrompt"`
	ImageURLs  any              `json:"imageUrls"`
	Extra      chatContextExtra `json:"extra"`
	Features   []any            `json:"features"`
	Text       string           `json:"text"`
}

type businessBlock struct {
	Product string `json:"product"`
	Version string `json:"version"`
	Type    string `json:"type"`
	Stage   string `json:"stage"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	BeginAt int64  `json:"begin_at"`
}

// chatPayload mirrors buildQoderRequestBody in the reference executor. The
// upstream only streams, so `stream` is always true even for non-stream
// callers (they get the stream collapsed locally).
type chatPayload struct {
	RequestID      string          `json:"request_id"`
	RequestSetID   string          `json:"request_set_id"`
	ChatRecordID   string          `json:"chat_record_id"`
	SessionID      string          `json:"session_id"`
	Stream         bool            `json:"stream"`
	ChatTask       string          `json:"chat_task"`
	IsReply        bool            `json:"is_reply"`
	IsRetry        bool            `json:"is_retry"`
	Source         int             `json:"source"`
	Version        string          `json:"version"`
	SessionType    string          `json:"session_type"`
	AgentID        string          `json:"agent_id"`
	TaskID         string          `json:"task_id"`
	CodeLanguage   string          `json:"code_language"`
	ChatPrompt     string          `json:"chat_prompt"`
	ImageURLs      any             `json:"image_urls"`
	AliyunUserType string          `json:"aliyun_user_type"`
	System         string          `json:"system"`
	Messages       []any           `json:"messages"`
	Tools          []any           `json:"tools"`
	Parameters     chatParameters  `json:"parameters"`
	ChatContext    chatContext     `json:"chat_context"`
	ModelConfig    json.RawMessage `json:"model_config"`
	Business       businessBlock   `json:"business"`
}

func buildChatPayload(creds Creds, params map[string]any, modelKey string, entry catalogEntry) (*chatPayload, error) {
	messages, systemText := normalizeMessages(params["messages"])
	tools := anySlice(params["tools"])
	if tools == nil {
		tools = []any{}
	}

	maxTokens := 32768
	if entry.maxOutputTokens > 0 {
		maxTokens = entry.maxOutputTokens
	}
	if n, ok := positiveInt(params["max_tokens"]); ok && n < maxTokens {
		maxTokens = n
	}
	if n, ok := positiveInt(params["max_completion_tokens"]); ok && n < maxTokens {
		maxTokens = n
	}

	lastUser := lastUserText(messages)
	recordID := stableChatRecordID(modelKey, messages, tools, maxTokens)
	requestID, err := newUUID()
	if err != nil {
		return nil, err
	}
	businessID, err := newUUID()
	if err != nil {
		return nil, err
	}

	return &chatPayload{
		RequestID:      requestID,
		RequestSetID:   recordID,
		ChatRecordID:   recordID,
		SessionID:      stableHash("qoder-session", creds.UserID, modelKey),
		Stream:         true,
		ChatTask:       "FREE_INPUT",
		IsReply:        true,
		IsRetry:        false,
		Source:         1,
		Version:        "3",
		SessionType:    "qodercli",
		AgentID:        "agent_common",
		TaskID:         "common",
		CodeLanguage:   "",
		ChatPrompt:     "",
		ImageURLs:      nil,
		AliyunUserType: "",
		System:         systemText,
		Messages:       messages,
		Tools:          tools,
		Parameters:     chatParameters{MaxTokens: maxTokens},
		ChatContext: chatContext{
			ChatPrompt: "",
			ImageURLs:  nil,
			Extra: chatContextExtra{
				Context:         []any{},
				ModelConfig:     modelConfigRef{Key: modelKey, IsReasoning: entry.isReasoning},
				OriginalContent: lastUser,
			},
			Features: []any{},
			Text:     lastUser,
		},
		ModelConfig: entry.raw,
		Business: businessBlock{
			Product: "cli",
			Version: "1.0.0",
			Type:    "agent",
			Stage:   "start",
			ID:      businessID,
			Name:    truncate(lastUser, 30),
			BeginAt: time.Now().UnixMilli(),
		},
	}, nil
}

// normalizeMessages hoists every system message into one string (Qoder rejects
// role:"system" inside messages) and normalizes each remaining content block.
func normalizeMessages(raw any) ([]any, string) {
	list := anySlice(raw)
	if len(list) == 0 {
		return []any{}, ""
	}
	var systemParts []string
	out := make([]any, 0, len(list))
	for _, item := range list {
		msg, ok := item.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		if role == "system" {
			if text := extractText(msg["content"]); text != "" {
				systemParts = append(systemParts, text)
			}
			continue
		}
		cloned := make(map[string]any, len(msg))
		for k, v := range msg {
			cloned[k] = v
		}
		cloned["content"] = normalizeContent(msg["content"])
		out = append(out, cloned)
	}
	return out, strings.Join(systemParts, "\n\n")
}

// normalizeContent flattens text-only content to a plain string. When images
// are present the content stays an array of OpenAI `image_url` parts; Claude
// `{type:"image", source:{...}}` blocks are converted to the same shape.
func normalizeContent(content any) any {
	switch c := content.(type) {
	case nil:
		return ""
	case string:
		return c
	case []any:
		return normalizeContentBlocks(c)
	default:
		return fmt.Sprint(content)
	}
}

func normalizeContentBlocks(content []any) any {
	blocks := make([]any, 0, len(content))
	var textParts []string
	hasImage := false
	for _, item := range content {
		part, ok := item.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := part["type"].(string)
		if typ == "image_url" {
			if iu, ok := part["image_url"].(map[string]any); ok {
				if u, _ := iu["url"].(string); u != "" {
					blocks = append(blocks, map[string]any{
						"type":      "image_url",
						"image_url": map[string]any{"url": u},
					})
					hasImage = true
					continue
				}
			}
		}
		if typ == "image" {
			if src, ok := part["source"].(map[string]any); ok {
				if url := imageSourceURL(src); url != "" {
					blocks = append(blocks, map[string]any{
						"type":      "image_url",
						"image_url": map[string]any{"url": url},
					})
					hasImage = true
					continue
				}
			}
		}
		if txt, ok := part["text"].(string); ok && txt != "" {
			if hasImage || len(blocks) > 0 {
				// Keep ordering faithful once images are in play.
				blocks = append(blocks, map[string]any{"type": "text", "text": txt})
			} else {
				textParts = append(textParts, txt)
			}
		}
	}
	if !hasImage {
		return strings.Join(textParts, "\n")
	}
	if len(textParts) > 0 {
		// Text collected before the first image is prepended as one block.
		blocks = append([]any{map[string]any{"type": "text", "text": strings.Join(textParts, "\n")}}, blocks...)
	}
	return blocks
}

// imageSourceURL converts a Claude image source into an OpenAI-compatible URL.
func imageSourceURL(src map[string]any) string {
	if typ, _ := src["type"].(string); typ == "base64" {
		data, _ := src["data"].(string)
		if data == "" {
			return ""
		}
		mediaType, _ := src["media_type"].(string)
		if mediaType == "" {
			mediaType = "image/png"
		}
		return "data:" + mediaType + ";base64," + data
	}
	u, _ := src["url"].(string)
	return u
}

func extractText(content any) string {
	switch c := content.(type) {
	case nil:
		return ""
	case string:
		return c
	case []any:
		parts := make([]string, 0, len(c))
		for _, item := range c {
			part, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if txt, ok := part["text"].(string); ok {
				parts = append(parts, txt)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return fmt.Sprint(content)
	}
}

func lastUserText(messages []any) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg, ok := messages[i].(map[string]any)
		if !ok {
			continue
		}
		if role, _ := msg["role"].(string); role != "user" {
			continue
		}
		switch c := msg["content"].(type) {
		case string:
			return c
		case []any:
			return extractText(c)
		}
	}
	return ""
}

// anySlice coerces the shapes a decoded JSON array can take into []any.
func anySlice(v any) []any {
	switch s := v.(type) {
	case []any:
		return s
	case []map[string]any:
		out := make([]any, len(s))
		for i, m := range s {
			out[i] = m
		}
		return out
	}
	return nil
}

func positiveInt(v any) (int, bool) {
	n, ok := numberToInt(v)
	return n, ok && n > 0
}

// stableHash mirrors the reference stableHash: sha256(prefix + "\0" + part...)
// truncated to 16 hex chars.
func stableHash(prefix string, parts ...string) string {
	h := sha256.New()
	h.Write([]byte(prefix))
	for _, p := range parts {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// stableChatRecordID groups related requests under one chat record. It hashes
// the same inputs as the reference; map key ordering differs from JS object
// insertion order, but the value only needs to be stable within this client.
func stableChatRecordID(model string, messages, tools []any, maxTokens int) string {
	h := sha256.New()
	h.Write([]byte("qoder-record\x00"))
	h.Write([]byte(model))
	for _, item := range messages {
		msg, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if role, _ := msg["role"].(string); role != "" {
			h.Write([]byte{0})
			h.Write([]byte(role))
		}
		switch c := msg["content"].(type) {
		case string:
			if c != "" {
				h.Write([]byte{0})
				h.Write([]byte(c))
			}
		case []any:
			// Include image refs so the same prompt with a different image
			// gets a distinct chat_record_id.
			h.Write([]byte{0})
			if b, err := marshalNoEscape(c); err == nil {
				h.Write(b)
			}
		}
	}
	if tools != nil {
		h.Write([]byte{0})
		if b, err := marshalNoEscape(tools); err == nil {
			h.Write(b)
		}
	}
	h.Write([]byte(fmt.Sprintf("\x00mt=%d", maxTokens)))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// ============ Response handling ============

// cancelReadCloser cancels the request context when the caller closes the body,
// which unblocks any goroutine still reading the upstream response.
type cancelReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelReadCloser) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// wrapStream returns the OpenAI-SSE view of an upstream envelope stream. The
// goroutine owns the upstream body and always closes it (and releases the
// request context) when the stream ends or the caller closes the pipe.
func wrapStream(resp *http.Response, model string, cancel context.CancelFunc) *http.Response {
	pr, pw := io.Pipe()
	go func() {
		defer cancel()
		defer resp.Body.Close()
		err := pumpEnvelopes(resp.Body, model, func(payload string) error {
			_, werr := io.WriteString(pw, "data: "+payload+"\n\n")
			return werr
		})
		// A pipe write error means the caller went away; either way the pipe
		// is finished. Close (not CloseWithError) so downstream sees EOF.
		_ = err
		_ = pw.Close()
	}()
	return &http.Response{
		Status:        "200 OK",
		StatusCode:    http.StatusOK,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Content-Type": []string{"text/event-stream"}, "Cache-Control": []string{"no-cache"}},
		Body:          &cancelReadCloser{ReadCloser: pr, cancel: cancel},
		ContentLength: -1,
		Request:       resp.Request,
	}
}

// pumpEnvelopes reads Qoder's `{"statusCodeValue":N,"body":"<inner>"}` SSE
// envelope and emits each inner OpenAI payload (including "[DONE]"). A non-200
// statusCodeValue becomes a synthetic OpenAI error chunk followed by "[DONE]";
// nothing is emitted after the terminator.
func pumpEnvelopes(src io.Reader, model string, emit func(string) error) error {
	br := bufio.NewReaderSize(src, 64<<10)
	for {
		line, readErr := br.ReadString('\n')
		if line != "" {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "data:") {
				data := strings.TrimSpace(trimmed[len("data:"):])
				switch data {
				case "":
					// keepalive / empty frame
				case "[DONE]":
					return emit("[DONE]")
				default:
					var env struct {
						StatusCodeValue any    `json:"statusCodeValue"`
						Body            string `json:"body"`
					}
					if err := json.Unmarshal([]byte(data), &env); err == nil {
						status := http.StatusOK
						if n, ok := numberToInt(env.StatusCodeValue); ok {
							status = n
						}
						if status != http.StatusOK {
							if werr := emit(errorChunk(model, status, env.Body)); werr != nil {
								return werr
							}
							return emit("[DONE]")
						}
						switch env.Body {
						case "":
							// Nothing to forward.
						case "[DONE]":
							return emit("[DONE]")
						default:
							// The inner payload must stay on one SSE data line;
							// embedded newlines would split the frame.
							inner := strings.NewReplacer("\r\n", "", "\n", "").Replace(env.Body)
							if werr := emit(inner); werr != nil {
								return werr
							}
						}
					}
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				// Upstream closed without an explicit terminator; flush one so
				// downstream parsers finish cleanly.
				return emit("[DONE]")
			}
			if werr := emit(errorChunk(model, 0, readErr.Error())); werr != nil {
				return werr
			}
			return emit("[DONE]")
		}
	}
}

// errorChunk builds the synthetic OpenAI error chunk used for envelope failures
// and stream read errors.
func errorChunk(model string, status int, message string) string {
	if message == "" {
		message = fmt.Sprintf("upstream status %d", status)
	}
	text := fmt.Sprintf("\n[qoder stream error: %s]", truncate(message, 200))
	if status != 0 {
		text = fmt.Sprintf("\n[qoder error %d: %s]", status, truncate(message, 200))
	}
	chunk := map[string]any{
		"id":      fmt.Sprintf("qoder-error-%d", time.Now().UnixMilli()),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         map[string]any{"content": text},
			"finish_reason": "stop",
		}},
	}
	b, err := marshalNoEscape(chunk)
	if err != nil {
		return "{}"
	}
	return string(b)
}

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

// randHex returns n random bytes as hex, or "" if the entropy source fails.
func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
