package integration

import (
	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/admin"
	"cline-go-proxy/internal/compaction"
	"cline-go-proxy/internal/egress"
	"cline-go-proxy/internal/platform"
	"cline-go-proxy/internal/zen"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestMaybeCompactFallsBackToClineSummary is the regression for "auto compact
// never works": zen returns 429, and the summary must still be produced through
// the Cline free chain instead of silently degrading to truncation.
func TestMaybeCompactFallsBackToClineSummary(t *testing.T) {
	t.Setenv(platform.DataDirEnv, t.TempDir())
	resetZenTestState(t)

	oldPool := accounts.Pool
	oldConfig := admin.GetProxyConfig()
	oldTransport := egress.Client.Transport
	oldZenClient := zen.HTTPClient
	t.Cleanup(func() {
		accounts.Pool = oldPool
		admin.SetProxyConfig(oldConfig)
		egress.Client.Transport = oldTransport
		zen.TransportMu.Lock()
		zen.HTTPClient = oldZenClient
		zen.TransportMu.Unlock()
	})

	cfg := admin.DefaultProxyConfig()
	cfg.ModelChain = []string{"summary-free"}
	cfg.OnlyFree = false
	admin.SetProxyConfig(cfg)

	accounts.Pool = &AccountPool{Accounts: []*Account{{
		AccountID:   "summary-account",
		Email:       "summary@example.com",
		AccessToken: "summary-token",
		ExpiresAt:   time.Now().Add(time.Hour).UnixMilli(),
		Status:      "active",
	}}}

	zenCfg := zen.DefaultConfig()
	zenCfg.Failover = true
	zenCfg.Compaction = zen.CompactConfig{Auto: true, Buffer: 100, KeepTokens: 200, MaxSummary: 64}
	zen.ConfigMu.Lock()
	zen.Config = zenCfg
	zen.ConfigMu.Unlock()

	// Zen is healthy (not in its failover window) but rate-limited: the first
	// summary candidate must fail fast and hand over to the Cline free chain.
	zen.TransportMu.Lock()
	zen.HTTPClient = &http.Client{Transport: freeModelRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Retry-After": []string{"3600"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"Free usage limit reached"}}`)),
			Request:    req,
		}, nil
	})}
	zen.TransportMu.Unlock()

	var clineCalls int
	egress.Client.Transport = freeModelRoundTripper(func(req *http.Request) (*http.Response, error) {
		clineCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"SUMMARY-OK"}}]}`)),
			Request:    req,
		}, nil
	})

	msgs := make([]any, 0, 30)
	for i := 0; i < 30; i++ {
		msgs = append(msgs, map[string]any{"role": "user", "content": strings.Repeat("x", 120)})
	}
	params := map[string]any{"model": "deepseek-v4-flash-free", "messages": msgs}
	zm := Model{ID: "deepseek-v4-flash-free", Context: 1000, Output: 100}

	out := compaction.MaybeCompact(params, zm, "")
	if !out.Changed {
		t.Fatal("MaybeCompact did not compact an over-budget request")
	}
	if !strings.Contains(out.Note, "compacted via summary") {
		t.Fatalf("compaction degraded instead of summarising: %q", out.Note)
	}
	if clineCalls == 0 {
		t.Fatal("Cline free chain was never asked for a summary")
	}
	compacted, _ := params["messages"].([]any)
	found := false
	for _, raw := range compacted {
		mm, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if c, _ := mm["content"].(string); strings.HasPrefix(c, "[Conversation Summary]") && strings.Contains(c, "SUMMARY-OK") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("compacted messages do not carry the summary: %+v", compacted)
	}
	keys := compaction.CompactStateKeys()
	if len(keys) == 0 {
		t.Fatal("compaction state was not persisted for the derived conversation key")
	}
	for _, key := range keys {
		if !strings.HasPrefix(key, "fp_") {
			t.Fatalf("expected a fingerprint session key, got %q", key)
		}
	}
}
