package main

import (
	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/egress"
	"cline-go-proxy/internal/zen"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestEstimateRequestTokensIsConservative locks down the estimator switch:
// tool schemas and non-ASCII text must count, and ASCII must be estimated at
// ~3 chars/token instead of the old JSON-bytes/4 (~4 chars/token).
func TestEstimateRequestTokensIsConservative(t *testing.T) {
	base := map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": strings.Repeat("a", 300)}},
	}
	if got := estimateRequestTokens(base); got < 100 {
		t.Fatalf("300 ASCII chars estimated at %d tokens, want >= 100", got)
	}
	toolSchema := map[string]any{
		"name":        "edit_file",
		"description": "edit a file on disk",
		"parameters":  map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
	}
	withTools := map[string]any{
		"messages": base["messages"],
		"tools":    []any{map[string]any{"type": "function", "function": toolSchema}},
	}
	if estimateRequestTokens(withTools) <= estimateRequestTokens(base) {
		t.Fatal("tool schemas must contribute to the input token estimate")
	}
	cjk := map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": strings.Repeat("中", 300)}},
	}
	if got := estimateRequestTokens(cjk); got < 250 {
		t.Fatalf("300 CJK chars estimated at %d tokens, want >= 250", got)
	}
}

// TestSummaryCandidatesSkipZenWhenDegraded ensures a rate-limited zen window
// does not make every compaction wait through zen retries before falling back.
func TestSummaryCandidatesSkipZenWhenDegraded(t *testing.T) {
	resetZenTestState(t)
	cfg := zen.DefaultConfig()
	cfg.Failover = true
	cfg.FailoverCount = 1
	zen.ConfigMu.Lock()
	zen.Config = cfg
	zen.ConfigMu.Unlock()
	zen.MarkFail()

	for _, m := range summaryModelCandidates("deepseek-v4-flash-free") {
		if _, ok := zen.ResolveInfo(m); ok {
			t.Fatalf("summary candidates include zen model %q while zen is degraded: %v", m, summaryModelCandidates("deepseek-v4-flash-free"))
		}
	}
}

// TestMaybeCompactFallsBackToClineSummary is the regression for "auto compact
// never works": zen returns 429, and the summary must still be produced through
// the Cline free chain instead of silently degrading to truncation.
func TestMaybeCompactFallsBackToClineSummary(t *testing.T) {
	t.Setenv(dataDirEnv, t.TempDir())
	resetZenTestState(t)

	oldPool := accounts.Pool
	oldConfig := getProxyConfig()
	oldTransport := egress.Client.Transport
	oldZenClient := zen.HTTPClient
	t.Cleanup(func() {
		accounts.Pool = oldPool
		setProxyConfig(oldConfig)
		egress.Client.Transport = oldTransport
		zen.TransportMu.Lock()
		zen.HTTPClient = oldZenClient
		zen.TransportMu.Unlock()
	})

	cfg := defaultProxyConfig()
	cfg.ModelChain = []string{"summary-free"}
	cfg.OnlyFree = false
	setProxyConfig(cfg)

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

	out := maybeCompact(params, zm, "")
	if !out.changed {
		t.Fatal("maybeCompact did not compact an over-budget request")
	}
	if !strings.Contains(out.note, "compacted via summary") {
		t.Fatalf("compaction degraded instead of summarising: %q", out.note)
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
		if c := strField(mm, "content"); strings.HasPrefix(c, "[Conversation Summary]") && strings.Contains(c, "SUMMARY-OK") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("compacted messages do not carry the summary: %+v", compacted)
	}
	if len(compactStates) == 0 {
		t.Fatal("compaction state was not persisted for the derived conversation key")
	}
	for key := range compactStates {
		if !strings.HasPrefix(key, "fp_") {
			t.Fatalf("expected a fingerprint session key, got %q", key)
		}
	}
}

// TestComboCompactionModelUsesNarrowestWindow ensures a combo budgets its
// compaction for the smallest candidate context, not the first entry.
func TestComboCompactionModelUsesNarrowestWindow(t *testing.T) {
	oldPool := accounts.Pool
	t.Cleanup(func() { accounts.Pool = oldPool })
	accounts.Pool = &AccountPool{Models: []Model{
		{ID: "wide", Context: 128000, Output: 32000, MetaLocked: true},
		{ID: "narrow", Context: 32000, Output: 8000, MetaLocked: true},
	}}
	combo := ModelCombo{Entries: []ComboEntry{{Model: "wide"}, {Model: "narrow"}}}
	if got := comboCompactionModel(combo); got.ID != "narrow" || got.Context != 32000 {
		t.Fatalf("combo compaction model = %+v, want narrow/32000", got)
	}
}

// TestEstimateJSONStaysByteBased keeps the legacy helper semantics used by
// callers that only need a coarse payload size.
func TestEstimateJSONStaysByteBased(t *testing.T) {
	v := map[string]any{"k": strings.Repeat("x", 40)}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if got := estimateJSON(v); got != len(b)/4 {
		t.Fatalf("estimateJSON = %d, want %d", got, len(b)/4)
	}
}
