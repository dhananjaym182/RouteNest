package zen

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/upstream/openaicompat"
)

func init() {
	BodyOptionsProvider = func() openaicompat.BodyOptions {
		return openaicompat.BodyOptions{
			PassThroughKeys:  []string{"tools", "temperature", "stream"},
			MinMaxTokens:     16,
			DefaultMaxTokens: 32000,
		}
	}
}

// resetZenTestState restores Zen-owned mutable state between tests.
func resetZenTestState(t *testing.T) {
	t.Helper()
	RemoteEnabledMu.Lock()
	oldZenEnabled := RemoteEnabled
	RemoteEnabled = false
	RemoteEnabledMu.Unlock()

	ConfigMu.Lock()
	oldCfg := Config
	Config = nil
	ConfigMu.Unlock()

	StateMu.Lock()
	oldCount, oldUntil := FailCount, FailUntil
	FailCount, FailUntil = 0, time.Time{}
	StateMu.Unlock()

	t.Cleanup(func() {
		RemoteEnabledMu.Lock()
		RemoteEnabled = oldZenEnabled
		RemoteEnabledMu.Unlock()
		ConfigMu.Lock()
		Config = oldCfg
		ConfigMu.Unlock()
		StateMu.Lock()
		FailCount, FailUntil = oldCount, oldUntil
		StateMu.Unlock()
	})
}

// ============ RouteModel / ResolveInfo 测试 ============

// currentZenModels / ResolveInfo 把 "zen" 与 "seed" 都视为 zen 来源
func withZenPool(t *testing.T, models []Model) {
	t.Helper()
	resetZenTestState(t)
	p := accounts.LoadPool()
	oldModels := p.Models
	p.Models = models
	accounts.SavePool()
	t.Cleanup(func() {
		q := accounts.LoadPool()
		q.Models = oldModels
		accounts.SavePool()
	})
}

func TestRouteModelZenFree(t *testing.T) {
	withZenPool(t, append([]Model{}, BuiltinModels()...))
	// 未同步过 → 种子表生效
	if got := RouteModel("deepseek-v4-flash-free"); got != "zen" {
		t.Errorf("routeModel(free seed model) = %q, want zen", got)
	}
	if got := RouteModel("opencode/deepseek-v4-flash-free"); got != "zen" {
		t.Errorf("routeModel(prefixed free seed model) = %q, want zen", got)
	}
	if got := RouteModel("opencode/mimo-v2.5-free"); got != "zen" {
		t.Errorf("routeModel(opencode/ prefix) = %q, want zen", got)
	}
	// 别名
	if got := RouteModel("deepseek-v4-flash"); got != "zen" {
		t.Errorf("routeModel(free model alias) = %q, want zen", got)
	}
	if got := RouteModel("deepseek-v4"); got != "zen" {
		t.Errorf("routeModel(alias) = %q, want zen", got)
	}
}

func TestRouteModelRejectPaid(t *testing.T) {
	withZenPool(t, []Model{
		{ID: "gpt-5-turbo", Provider: "opencode", Cost: "pass", Status: "active", Source: "zen"},
	})
	if got := RouteModel("gpt-5-turbo"); got != "reject" {
		t.Errorf("routeModel(paid zen model) = %q, want reject", got)
	}
	if got := RouteModel("opencode/gpt-5-turbo"); got != "reject" {
		t.Errorf("routeModel(opencode/paid) = %q, want reject", got)
	}
}

func TestRouteModelClinePassthrough(t *testing.T) {
	withZenPool(t, append([]Model{}, BuiltinModels()...))
	if got := RouteModel("cline-free/glm-5.2"); got != "cline" {
		t.Errorf("routeModel(cline model) = %q, want cline", got)
	}
	if got := RouteModel(""); got != "cline" {
		t.Errorf("routeModel(empty) = %q, want cline", got)
	}
}

// 僵尸模型回归：同步成功后（RemoteEnabled=true），官方已下架的种子模型必须休眠。
func TestDelistedSeedModelGoesDormant(t *testing.T) {
	RemoteEnabledMu.Lock()
	oldEnabled := RemoteEnabled
	RemoteEnabled = true
	RemoteEnabledMu.Unlock()
	t.Cleanup(func() { restoreRemoteZen(oldEnabled) })

	// 池中只有官方还存在的模型，longcat-2.0-free 已被下架（不在池里）
	withZenPool(t, []Model{
		{ID: "deepseek-v4-flash-free", Provider: "opencode", Cost: "free", Source: "zen", Context: 200000},
	})
	if _, ok := ResolveInfo("longcat-2.0-free"); ok {
		t.Error("delisted seed model should be dormant after sync (no zombie)")
	}
	if _, ok := ResolveInfo("big-pickle"); ok {
		t.Error("delisted seed model big-pickle should be dormant")
	}
	m, ok := ResolveInfo("deepseek-v4-flash-free")
	if !ok || m.ID != "deepseek-v4-flash-free" {
		t.Errorf("surviving model should resolve, got %+v ok=%v", m, ok)
	}
}

func restoreRemoteZen(v bool) {
	RemoteEnabledMu.Lock()
	RemoteEnabled = v
	RemoteEnabledMu.Unlock()
}

func TestAliasNotShadowedBySyncedPaidModel(t *testing.T) {
	restoreRemoteZen(false)
	withZenPool(t, append([]Model{}, BuiltinModels()...))
	// 同步来一个付费模型 ID 恰好等于 free 别名（参考项目的隐患场景）
	withZenPool(t, append(BuiltinModels(), Model{
		ID: "deepseek-v4-flash", Provider: "opencode", Cost: "pass", Source: "zen",
	}))
	// 别名 deepseek-v4-flash 应解析到免费正式 ID 而非付费条目
	m, ok := ResolveInfo("deepseek-v4-flash")
	if !ok {
		t.Fatal("alias should still resolve")
	}
	if m.Cost != "free" || !strings.HasSuffix(m.ID, "-free") {
		t.Errorf("alias resolved to paid model %+v, want free -free model", m)
	}
}

// ============ 故障转移状态机测试 ============

func TestFailoverStateMachine(t *testing.T) {
	// 隔离全局状态
	StateMu.Lock()
	oldCount, oldUntil := FailCount, FailUntil
	StateMu.Unlock()
	t.Cleanup(func() {
		StateMu.Lock()
		FailCount, FailUntil = oldCount, oldUntil
		StateMu.Unlock()
	})

	StateMu.Lock()
	FailCount, FailUntil = 0, time.Time{}
	StateMu.Unlock()

	if FailedNow() {
		t.Fatal("should not be in failover initially")
	}
	for i := 0; i < 3; i++ {
		MarkFail()
	}
	if !FailedNow() {
		t.Error("3 consecutive failures should arm failover")
	}
	markZenSuccess()
	if FailedNow() {
		t.Error("success should reset failover state")
	}
}

func TestIsRateLimited(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   bool
	}{
		{429, `{"error":"x"}`, true},
		{503, "", true},
		{403, `"ResourceExhausted"`, true},
		{502, "rate limit reached", true},
		{400, "rate limit in body only", false}, // 400 不算限流信号
		{500, "", false},
	}
	for _, c := range cases {
		if got := isRateLimited(c.status, c.body); got != c.want {
			t.Errorf("isRateLimited(%d,%q)=%v want %v", c.status, c.body, got, c.want)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	if d := parseRetryAfter("120"); d != 120*time.Second {
		t.Errorf("seconds parse: got %v", d)
	}
	if d := parseRetryAfter(""); d != 0 {
		t.Errorf("empty: got %v", d)
	}
	future := time.Now().Add(2 * time.Minute).UTC().Format(http.TimeFormat)
	d := parseRetryAfter(future)
	if d <= 0 || d > 3*time.Minute {
		t.Errorf("http-date parse: got %v", d)
	}
}

// ============ 配置校验测试 ============

func TestValidateProxyList(t *testing.T) {
	if err := ValidateProxyList([]string{"socks5://127.0.0.1:1080", "http://user:pass@p.com:8080"}); err != nil {
		t.Errorf("valid list rejected: %v", err)
	}
	for _, bad := range []string{"ftp://x:1", "http://noport", "not-a-url"} {
		if err := ValidateProxyList([]string{bad}); err == nil {
			t.Errorf("bad proxy %q accepted", bad)
		}
	}
	if err := ValidateProxyList([]string{"", "  "}); err != nil {
		t.Errorf("blank lines should pass: %v", err)
	}
}

// ============ buildZenBody 测试 ============

func TestBuildZenBodyRewritesModelAndStripsReasoning(t *testing.T) {
	params := map[string]any{
		"model":      "deepseek-v4-flash",
		"messages":   []any{map[string]any{"role": "user", "content": "hi"}},
		"max_tokens": 100.0,
		"stream":     true,
		"tools":      []any{},
	}
	body := buildZenBody(params, true, false)
	if body["model"] != "deepseek-v4-flash-free" {
		t.Errorf("model alias rewrite failed: %v", body["model"])
	}
	if body["stream"] != true {
		t.Error("stream flag lost")
	}
	for _, k := range []string{"reasoning_effort", "reasoningEffort"} {
		if _, ok := body[k]; ok {
			t.Errorf("%s should be stripped", k)
		}
	}
	if body["session_id"] != nil {
		t.Error("zen body must not carry cline session_id")
	}
}

// ============ 匿名免费层 agent 形态伪装测试 ============

func TestBuildZenBodyAnonymousInjectsToolsAndStream(t *testing.T) {
	params := map[string]any{
		"model":    "mimo-v2.6-flash-free",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}
	body := buildZenBody(params, false, true)
	if body["stream"] != true {
		t.Error("anonymous body must force stream=true upstream")
	}
	so, ok := body["stream_options"].(map[string]any)
	if !ok || so["include_usage"] != true {
		t.Error("anonymous body must set stream_options.include_usage")
	}
	tools, ok := body["tools"].([]any)
	if !ok {
		t.Fatal("anonymous body must inject tools")
	}
	names := map[string]bool{}
	for _, item := range tools {
		entry, _ := item.(map[string]any)
		fn, _ := entry["function"].(map[string]any)
		name, _ := fn["name"].(string)
		names[name] = true
	}
	for _, want := range anonymousCoreTools {
		if !names[want] {
			t.Errorf("missing core tool %q", want)
		}
	}

	// 客户端已声明的工具保持原样，缺的才补；read/shell 由网关校验，必须替换成
	// CLI 原始定义
	params2 := map[string]any{
		"model":    "mimo-v2.6-flash-free",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"tools":    []any{map[string]any{"type": "function", "function": map[string]any{"name": "bash", "parameters": map[string]any{}}}},
	}
	body2 := buildZenBody(params2, false, true)
	tools2, _ := body2["tools"].([]any)
	if len(tools2) != len(anonymousCoreTools)+1 {
		t.Fatalf("want %d tools (1 caller tool + %d injected), got %d", len(anonymousCoreTools)+1, len(anonymousCoreTools), len(tools2))
	}
	kept := false
	for _, item := range tools2 {
		entry, _ := item.(map[string]any)
		fn, _ := entry["function"].(map[string]any)
		name, _ := fn["name"].(string)
		if name == "bash" {
			kept = true
		}
	}
	if !kept {
		t.Error("caller tool must be preserved")
	}
}

func TestCanonicalZenSessionFormat(t *testing.T) {
	pattern := `^ses_[0-9a-f]{12}[0-9A-Za-z]{14}$`
	re := regexp.MustCompile(pattern)
	// 无信号 → 随机规范形态
	s1 := zenSessionID(map[string]any{})
	if !re.MatchString(s1) {
		t.Errorf("random session %q does not match canonical format", s1)
	}
	// 同一对话 → 稳定派生
	params := map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "hello world"}},
	}
	s2 := zenSessionID(params)
	s3 := zenSessionID(params)
	if s2 != s3 {
		t.Errorf("same conversation must derive stable session: %q vs %q", s2, s3)
	}
	if !re.MatchString(s2) {
		t.Errorf("derived session %q does not match canonical format", s2)
	}
	// 不同对话 → 不同会话
	params4 := map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "different"}},
	}
	if zenSessionID(params4) == s2 {
		t.Error("different conversations must derive different sessions")
	}
}

func TestZenAPIKeyForFreeModel(t *testing.T) {
	if got := zenAPIKeyFor(map[string]any{"model": "mimo-v2.6-flash-free"}, "sk-real"); got != "public" {
		t.Fatalf("free model key = %q, want public", got)
	}
	if got := zenAPIKeyFor(map[string]any{"model": "mimo-v2.6-flash"}, "sk-real"); got != "public" {
		t.Fatalf("free alias key = %q, want public", got)
	}
	if got := zenAPIKeyFor(map[string]any{"model": "gpt-5"}, "sk-real"); got != "sk-real" {
		t.Fatalf("paid model key = %q, want sk-real", got)
	}
}
