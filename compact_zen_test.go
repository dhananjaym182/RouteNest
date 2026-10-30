package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"cline-go-proxy/internal/zen"
)

// resetZenTestState 恢复与 zen 相关的全局可变状态，避免测试间相互污染。
func resetZenTestState(t *testing.T) {
	t.Helper()
	zen.RemoteEnabledMu.Lock()
	oldZenEnabled := zen.RemoteEnabled
	zen.RemoteEnabled = false
	zen.RemoteEnabledMu.Unlock()

	zen.ConfigMu.Lock()
	oldCfg := zen.Config
	zen.Config = nil
	zen.ConfigMu.Unlock()

	zen.StateMu.Lock()
	oldCount, oldUntil := zen.FailCount, zen.FailUntil
	zen.FailCount, zen.FailUntil = 0, time.Time{}
	zen.StateMu.Unlock()

	compactStatesMu.Lock()
	oldStates := compactStates
	compactStates = make(map[string]*compactState)
	compactStatesMu.Unlock()

	t.Cleanup(func() {
		zen.RemoteEnabledMu.Lock()
		zen.RemoteEnabled = oldZenEnabled
		zen.RemoteEnabledMu.Unlock()
		zen.ConfigMu.Lock()
		zen.Config = oldCfg
		zen.ConfigMu.Unlock()
		zen.StateMu.Lock()
		zen.FailCount, zen.FailUntil = oldCount, oldUntil
		zen.StateMu.Unlock()
		compactStatesMu.Lock()
		compactStates = oldStates
		compactStatesMu.Unlock()
	})
}

// ============ 压缩核心逻辑测试 ============

func TestSelectRecentBudgetSplit(t *testing.T) {
	msgs := []string{
		strings.Repeat("a", 800), // ~200 tokens
		strings.Repeat("b", 800), // ~200 tokens
		strings.Repeat("c", 80),  // ~20 tokens
	}
	// estimateText is conservative (~3 ASCII chars/token), so 500 tokens keeps
	// the last two messages (266+26) and overflows the first (266 -> split 1).
	sel := selectRecent(msgs, 500)
	if sel == nil {
		t.Fatal("expected selection")
	}
	if sel.split != 1 {
		t.Errorf("split=%d want 1 (last two messages fit)", sel.split)
	}
	recentText := strings.Join(sel.recent, "\n")
	if !strings.Contains(recentText, strings.Repeat("b", 10)) || !strings.Contains(recentText, "cccc") {
		t.Error("recent should contain tail messages/suffix")
	}
	if len(sel.head) == 0 || !strings.HasPrefix(sel.head[0], "aaaa") {
		t.Error("head should contain overflowed prefix")
	}
}

func TestSerializeMsgRoles(t *testing.T) {
	user := map[string]any{"role": "user", "content": "hello"}
	if s := serializeMsg(user); !strings.HasPrefix(s, "[User]: hello") {
		t.Errorf("user serialize: %q", s)
	}
	tool := map[string]any{"role": "tool", "content": strings.Repeat("x", 3000)}
	s := serializeMsg(tool)
	if len(s) > toolOutputMaxChars+50 {
		t.Errorf("tool output not truncated: %d", len(s))
	}
	asst := map[string]any{
		"role":    "assistant",
		"content": "doing it",
		"tool_calls": []any{map[string]any{
			"id": "c1", "type": "function",
			"function": map[string]any{"name": "edit_file", "arguments": `{"path":"a.go"}`},
		}},
	}
	s = serializeMsg(asst)
	if !strings.Contains(s, `[Assistant]: doing it`) || !strings.Contains(s, "[Assistant tool call]: edit_file") {
		t.Errorf("assistant serialize: %q", s)
	}
}

func TestEstimateJSONAndCompactDisabled(t *testing.T) {
	cfg := zen.GetConfig()
	if cfg.Compaction.Buffer <= 0 {
		t.Error("default compaction buffer should be positive")
	}
	params := map[string]any{
		"model":    "m",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}
	bts, _ := json.Marshal(params)
	if estimateJSON(params) != len(bts)/4 {
		t.Error("estimateJSON should be len/4")
	}
}
