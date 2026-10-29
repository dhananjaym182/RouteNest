package zen

import (
	"strings"
	"testing"
)

func TestIdentifierShape(t *testing.T) {
	g := NewIdentifier()
	id := g.New("ses", true)
	parts := strings.SplitN(id, "_", 2)
	if len(parts) != 2 || parts[0] != "ses" {
		t.Fatalf("id = %q", id)
	}
	if len(parts[1]) != 26 {
		t.Fatalf("suffix length = %d (%q)", len(parts[1]), parts[1])
	}
	for _, r := range parts[1] {
		if !strings.ContainsRune(base62Alphabet, r) && !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("unexpected character %q in %q", r, id)
		}
	}
	if other := g.New("msg", false); !strings.HasPrefix(other, "msg_") {
		t.Fatalf("prefix not honored: %q", other)
	}
}

func TestRandHelpers(t *testing.T) {
	if got := RandHex(8); len(got) != 16 {
		t.Fatalf("RandHex length = %d", len(got))
	}
	for i := 0; i < 20; i++ {
		if n := RandIntn(7); n < 0 || n >= 7 {
			t.Fatalf("RandIntn out of range: %d", n)
		}
	}
	if RandIntn(0) != 0 {
		t.Fatal("RandIntn(0) must be 0")
	}
	if got := WithRetryJitter(-1); got != -1 {
		t.Fatalf("non-positive delay must pass through, got %v", got)
	}
	if got := WithRetryJitter(100); got < 100 || got > 125 {
		t.Fatalf("jitter out of range: %v", got)
	}
}

func TestConversationSeedAndSessions(t *testing.T) {
	params := map[string]any{
		"metadata": map[string]any{"session_id": "session-1"},
		"messages": []any{map[string]any{"role": "user", "content": "hello"}},
	}
	if got := ConversationSeed(params); got != "session-1" {
		t.Fatalf("seed = %q", got)
	}
	noMeta := map[string]any{"messages": []any{
		map[string]any{"role": "system", "content": "sys"},
		map[string]any{"role": "user", "content": "first"},
	}}
	if got := ConversationSeed(noMeta); got != `"first"` {
		t.Fatalf("seed = %q", got)
	}
	if got := ConversationSeed(map[string]any{}); got != "" {
		t.Fatalf("empty seed = %q", got)
	}

	cache := NewSessionCache()
	s1 := cache.SessionID(params)
	s2 := cache.SessionID(params)
	if s1 != s2 {
		t.Fatalf("same conversation must reuse the session: %q vs %q", s1, s2)
	}
	if !strings.HasPrefix(s1, "ses_") {
		t.Fatalf("session = %q", s1)
	}
	if other := cache.SessionID(map[string]any{}); other == "" || strings.HasPrefix(other, "ses__") {
		t.Fatalf("random session = %q", other)
	}
}

func TestUserAgent(t *testing.T) {
	ua := UserAgent("responses")
	if !strings.Contains(ua, "opencode/"+ClientVersion) || !strings.Contains(ua, "provider-utils/4.0.40") {
		t.Fatalf("ua = %q", ua)
	}
	if ua := UserAgent("unknown"); !strings.Contains(ua, "provider-utils/4.0.23") {
		t.Fatalf("fallback ua = %q", ua)
	}
}

func TestEnsureAnonymousTools(t *testing.T) {
	body := map[string]any{}
	EnsureAnonymousTools(body)
	tools := body["tools"].([]any)
	if len(tools) != len(AnonymousCoreTools) {
		t.Fatalf("tools = %d", len(tools))
	}

	body = map[string]any{"tools": []any{
		map[string]any{"type": "function", "function": map[string]any{"name": "read"}},
	}}
	EnsureAnonymousTools(body)
	tools = body["tools"].([]any)
	if len(tools) != len(AnonymousCoreTools) {
		t.Fatalf("tools = %d", len(tools))
	}
	found := 0
	for _, item := range tools {
		fn := item.(map[string]any)["function"].(map[string]any)
		if fn["name"] == "read" {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("declared read tool duplicated: %d", found)
	}

	body = map[string]any{"tools": "not-a-list"}
	EnsureAnonymousTools(body)
	if body["tools"] != "not-a-list" {
		t.Fatal("non-array tools must be left untouched")
	}
}

func TestBuildChatBody(t *testing.T) {
	params := map[string]any{
		"model":            "opencode/zen-free",
		"max_tokens":       4096,
		"temperature":      0.2,
		"reasoning_effort": "high",
		"messages": []any{
			map[string]any{"role": "user", "content": "hi"},
			map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"id": "call_bad", "function": map[string]any{"name": ""}},
			}},
		},
	}
	opts := BodyOptions{
		PassThroughKeys: []string{"temperature", "tools"},
		ResolveModel: func(model string) (string, bool) {
			if model == "opencode/zen-free" {
				return "zen-free", true
			}
			return "", false
		},
	}
	body := BuildChatBody(params, false, true, opts)
	if body["model"] != "zen-free" || body["max_tokens"] != 4096 || body["temperature"] != 0.2 {
		t.Fatalf("body = %v", body)
	}
	if body["stream"] != true {
		t.Fatal("anonymous requests must force streaming")
	}
	if so, _ := body["stream_options"].(map[string]any); so["include_usage"] != true {
		t.Fatalf("stream_options = %v", body["stream_options"])
	}
	if _, ok := body["reasoning_effort"]; ok {
		t.Fatal("reasoning_effort must be removed")
	}
	msgs := body["messages"].([]any)
	var assistant map[string]any
	for _, raw := range msgs {
		m, _ := raw.(map[string]any)
		if role, _ := m["role"].(string); role == "assistant" {
			assistant = m
		}
	}
	if assistant == nil {
		t.Fatal("assistant message missing")
	}
	if _, ok := assistant["tool_calls"]; ok {
		t.Fatal("invalid tool_calls must be removed")
	}
	if tools, _ := body["tools"].([]any); len(tools) != len(AnonymousCoreTools) {
		t.Fatalf("core tools missing: %v", body["tools"])
	}

	body = BuildChatBody(map[string]any{"model": "opencode/other"}, false, false, BodyOptions{
		ResolveModel: func(string) (string, bool) { return "", false },
	})
	if body["model"] != "other" {
		t.Fatalf("model = %v", body["model"])
	}
	if body["stream"] != false {
		t.Fatal("non-anonymous non-stream request must stay non-streaming")
	}
	if _, ok := body["stream_options"]; ok {
		t.Fatal("stream_options only applies to anonymous streams")
	}
}

func TestFingerprintToolSchemasExact(t *testing.T) {
	tools := FingerprintTools()
	if len(tools) != 2 {
		t.Fatalf("tools = %d, want read+shell", len(tools))
	}
	byName := map[string]map[string]any{}
	for _, raw := range tools {
		entry, _ := raw.(map[string]any)
		fn, _ := entry["function"].(map[string]any)
		name, _ := fn["name"].(string)
		byName[name] = fn
	}
	read, ok := byName["read"]
	if !ok {
		t.Fatal("read tool missing")
	}
	if desc, _ := read["description"].(string); !strings.Contains(desc, "1-based line number") || !strings.Contains(desc, "PDFs") {
		t.Fatalf("read description = %q", desc)
	}
	rp, _ := read["parameters"].(map[string]any)
	if rp["additionalProperties"] != false {
		t.Fatal("read parameters must set additionalProperties=false")
	}
	if req, _ := rp["required"].([]any); len(req) != 1 || req[0] != "path" {
		t.Fatalf("read required = %v", rp["required"])
	}
	props, _ := rp["properties"].(map[string]any)
	for _, k := range []string{"path", "offset", "limit"} {
		if _, ok := props[k]; !ok {
			t.Fatalf("read parameter %q missing", k)
		}
	}

	shell, ok := byName["shell"]
	if !ok {
		t.Fatal("shell tool missing")
	}
	if desc, _ := shell["description"].(string); !strings.Contains(desc, "Execute a shell command") || !strings.Contains(desc, "using ") {
		t.Fatalf("shell description = %q", desc)
	}
	sp, _ := shell["parameters"].(map[string]any)
	if sp["additionalProperties"] != false {
		t.Fatal("shell parameters must set additionalProperties=false")
	}
	if req, _ := sp["required"].([]any); len(req) != 1 || req[0] != "command" {
		t.Fatalf("shell required = %v", sp["required"])
	}
}

func TestBuildChatBodyInjectsEnv(t *testing.T) {
	params := map[string]any{
		"model":    "deepseek-v4.1-flash-free",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}
	body := BuildChatBody(params, true, true, BodyOptions{})
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want system+user", len(msgs))
	}
	first, _ := msgs[0].(map[string]any)
	if role, _ := first["role"].(string); role != "system" {
		t.Fatalf("first role = %v, want system", first["role"])
	}
	content, _ := first["content"].(string)
	for _, want := range []string{"<env>", "</env>", "Working directory:", "Workspace root folder:", "Is directory a git repo:", "Platform:", "Prefer "} {
		if !strings.Contains(content, want) {
			t.Fatalf("env block missing %q: %q", want, content)
		}
	}

	// idempotent: a second injection must not duplicate the block
	InjectEnv(body)
	msgs, _ = body["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("env injection not idempotent: %d messages", len(msgs))
	}
	first, _ = msgs[0].(map[string]any)
	if n := strings.Count(first["content"].(string), "<env>"); n != 1 {
		t.Fatalf("env block duplicated: %d", n)
	}
}

func TestBuildChatBodyEnvAppendsToExistingSystem(t *testing.T) {
	params := map[string]any{
		"model": "deepseek-v4.1-flash-free",
		"messages": []any{
			map[string]any{"role": "system", "content": "be brief"},
			map[string]any{"role": "user", "content": "hi"},
		},
	}
	body := BuildChatBody(params, true, true, BodyOptions{})
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d", len(msgs))
	}
	first, _ := msgs[0].(map[string]any)
	content, _ := first["content"].(string)
	if !strings.HasPrefix(content, "be brief") {
		t.Fatalf("existing system prompt lost: %q", content)
	}
	if !strings.Contains(content, "<env>") {
		t.Fatalf("env not appended: %q", content)
	}
}
