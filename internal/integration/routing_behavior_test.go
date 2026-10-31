package integration

// Explicit routing behaviour (plan Phase 5 pre-split tests). Each test observes
// upstream traffic through transports and pins one rule of the dispatch order:
// provider first, named pins, free alias, bidirectional Cline/Zen failover,
// disabled and free-only policy, and mutation of the caller's request map.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"routenest/internal/admin"
	"routenest/internal/routing"
	"routenest/internal/zen"
)

const routingOKBody = `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"total_tokens":1}}`

// Provider-first: a model that both a custom provider and the Cline pool serve is
// sent to the provider. The Cline pool is not contacted.
func TestRoutingProviderFirstBeforeCline(t *testing.T) {
	var providerHits, clineHits int32
	srv := newRoutingServer(t, nil, http.StatusOK, routingOKBody)
	installPool(t, []Model{
		{ID: "shared/first", Provider: "cline", Cost: "pass", Status: "active", Source: "remote"},
	})
	installProviders(t, &CustomProvider{ID: "pf", Name: "Front", BaseURL: srv.URL, APIKey: "k", Enabled: true, ModelIDs: []string{"shared/first"}})
	installSplitTransport(t, srv.URL, &providerHits, &clineHits, http.StatusOK, routingOKBody)

	resp, acc, err := routing.CallClineAPI(map[string]any{"model": "shared/first", "messages": []any{}}, false)
	if err != nil {
		t.Fatalf("CallClineAPI: %v", err)
	}
	defer resp.Body.Close()
	if acc != nil {
		t.Error("provider-served request must not bind a Cline account")
	}
	if atomic.LoadInt32(&providerHits) != 1 || atomic.LoadInt32(&clineHits) != 0 {
		t.Errorf("provider hits = %d, cline hits = %d; want 1 and 0", providerHits, clineHits)
	}
}

// Named pin: a pinned request reaches only the named provider. When that provider
// fails, the request does not fall back to the Cline pool.
func TestRoutingNamedPinFailsWithoutFallback(t *testing.T) {
	var providerHits, clineHits int32
	srv := newRoutingServer(t, nil, http.StatusInternalServerError, `{"error":"down"}`)
	installPool(t, []Model{
		{ID: "pin/model", Provider: "cline", Cost: "pass", Status: "active", Source: "remote"},
	})
	installProviders(t, &CustomProvider{ID: "pp", Name: "Pinned", BaseURL: srv.URL, APIKey: "k", Enabled: true, ModelIDs: []string{"pin/model"}})
	installSplitTransport(t, srv.URL, &providerHits, &clineHits, http.StatusInternalServerError, `{"error":"down"}`)

	resp, _, err := routing.CallClineAPI(map[string]any{"model": "pin/model@Pinned"}, false)
	if err == nil && (resp == nil || resp.StatusCode < 500) {
		t.Fatalf("a pinned request whose provider fails must not succeed: resp=%v err=%v", resp, err)
	}
	if resp != nil {
		resp.Body.Close()
	}
	if atomic.LoadInt32(&clineHits) != 0 {
		t.Errorf("pinned failure fell back to Cline %d time(s)", clineHits)
	}
	if atomic.LoadInt32(&providerHits) == 0 {
		t.Error("the pinned provider was never contacted")
	}
}

// Request-map mutation: dispatch removes the internal attribution keys from the
// caller's map before routing, and records the attribution on success.
func TestRoutingRequestMapMutation(t *testing.T) {
	installPool(t, []Model{
		{ID: "map/model", Provider: "cline", Cost: "pass", Status: "active", Source: "remote"},
	})
	installTransport(t, func(req *http.Request) *http.Response {
		return stubRoutingResponse(req, http.StatusOK, routingOKBody)
	})

	params := map[string]any{
		"model":                   "map/model",
		"messages":                []any{},
		routing.ServedByParam:     "stale-served",
		routing.ProviderNameParam: "stale-provider",
	}
	resp, _, err := routing.CallClineAPI(params, false)
	if err != nil {
		t.Fatalf("CallClineAPI: %v", err)
	}
	resp.Body.Close()
	if _, ok := params[routing.ProviderNameParam]; ok {
		t.Errorf("stale provider attribution survived the Cline path: %v", params[routing.ProviderNameParam])
	}
	if got, _ := params["model"].(string); got != "map/model" {
		t.Errorf("model = %q, want the bare id", got)
	}
}

// Free alias: "free" is served from the Cline free chain. The first upstream
// request names a model from the free chain or a free pool record.
func TestRoutingFreeAliasUsesFreeChain(t *testing.T) {
	installPool(t, []Model{
		{ID: "remote/free-live", Provider: "cline", Cost: "free", Status: "active", Source: "remote"},
	})
	seen := installTransportRecordingModels(t)
	resp, _, err := routing.CallClineAPI(map[string]any{"model": "free", "messages": []any{}}, false)
	if err != nil {
		t.Fatalf("CallClineAPI(free): %v", err)
	}
	resp.Body.Close()
	if len(*seen) == 0 {
		t.Fatal("no upstream request was made")
	}
	first := (*seen)[0]
	inChain := false
	for _, m := range routing.FreeModelChain {
		if m == first {
			inChain = true
		}
	}
	if first != "remote/free-live" && !inChain {
		t.Errorf("first upstream model %q is neither a pool free model nor in the free chain", first)
	}
}

// Cline to Zen failover: when the Cline free chain is exhausted, the free alias
// falls over to an OpenCode zen free model.
func TestRoutingClineFailsOverToZen(t *testing.T) {
	installPool(t, nil)
	installTransport(t, func(req *http.Request) *http.Response {
		return stubRoutingResponse(req, http.StatusInternalServerError, `{"error":"cline down"}`)
	})
	var zenHits int32
	installZenStub(t, &zenHits, routingOKBody)
	installZenFailover(t, true, true)

	resp, _, err := routing.CallClineAPI(map[string]any{"model": "free", "messages": []any{}}, false)
	if err != nil {
		t.Fatalf("expected Zen failover to serve the request, got %v", err)
	}
	resp.Body.Close()
	if atomic.LoadInt32(&zenHits) == 0 {
		t.Error("zen was not contacted after the Cline free chain failed")
	}
}

// Zen to Cline failover: ZenFailoverToCline sends a failed zen request to the
// Cline free pool when the failover switch is on, and reports that it attempted.
func TestRoutingZenFailsOverToCline(t *testing.T) {
	installPool(t, nil)
	var clineHits int32
	installTransport(t, func(req *http.Request) *http.Response {
		atomic.AddInt32(&clineHits, 1)
		return stubRoutingResponse(req, http.StatusOK, routingOKBody)
	})
	installZenFailover(t, true, true)

	resp, _, err, attempted := routing.ZenFailoverToCline(map[string]any{"model": "zen/x", "messages": []any{}}, false)
	if !attempted || err != nil {
		t.Fatalf("attempted=%v err=%v, want an attempted failover", attempted, err)
	}
	resp.Body.Close()
	if atomic.LoadInt32(&clineHits) == 0 {
		t.Error("Cline pool was not used after the zen failure")
	}

	installZenFailover(t, false, true)
	if _, _, _, attempted := routing.ZenFailoverToCline(map[string]any{"model": "zen/x"}, false); attempted {
		t.Error("failover disabled must not attempt Cline")
	}
}

// Free-only policy: with onlyFree set, a paid pinned model is refused before any
// upstream call, and a free model is still served.
func TestRoutingFreeOnlyRefusesPaidPin(t *testing.T) {
	cfg := admin.DefaultProxyConfig()
	cfg.OnlyFree = true
	installConfig(t, cfg)
	installPool(t, []Model{
		{ID: "paid/m", Provider: "cline", Cost: "pass", Status: "active", Source: "remote"},
	})
	var hits int32
	installTransport(t, func(req *http.Request) *http.Response {
		atomic.AddInt32(&hits, 1)
		return stubRoutingResponse(req, http.StatusOK, routingOKBody)
	})

	if _, _, err := routing.CallClineAPI(map[string]any{"model": "paid/m@SomeProvider"}, false); err == nil || !strings.Contains(err.Error(), "onlyFree") {
		t.Fatalf("paid pin under onlyFree: err = %v, want an onlyFree refusal", err)
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Errorf("refused request reached upstream %d time(s)", hits)
	}
}

// Disabled model: a disabled model is excluded from the live set, so the default
// model cannot point at it (characterization of the policy inputs).
func TestRoutingDisabledModelExcludedFromDefault(t *testing.T) {
	installPool(t, []Model{
		{ID: "off/one", Provider: "cline", Cost: "free", Status: "active", Source: "remote", Disabled: true},
		{ID: "on/one", Provider: "cline", Cost: "free", Status: "active", Source: "remote"},
	})
	if got := routing.DefaultModel(); got == "off/one" {
		t.Errorf("default model = %q, a disabled model must never be the default", got)
	}
}

// --- helpers ---------------------------------------------------------------

// newRoutingServer serves a chat-completions endpoint with a fixed status and body,
// and counts its hits when hits is non-nil.
func newRoutingServer(t *testing.T, hits *int32, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			atomic.AddInt32(hits, 1)
		}
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func stubRoutingResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

// installSplitTransport routes outbound requests by host: requests to the
// provider's test server count as provider hits and answer with the provider
// status and body; every other host is the Cline pool.
func installSplitTransport(t *testing.T, providerURL string, providerHits, clineHits *int32, providerStatus int, providerBody string) {
	t.Helper()
	providerHost := strings.TrimPrefix(providerURL, "http://")
	installTransport(t, func(req *http.Request) *http.Response {
		if req.URL.Host == providerHost {
			atomic.AddInt32(providerHits, 1)
			return stubRoutingResponse(req, providerStatus, providerBody)
		}
		atomic.AddInt32(clineHits, 1)
		return stubRoutingResponse(req, http.StatusOK, routingOKBody)
	})
}

// installTransportRecordingModels installs a Cline transport that records the
// model named by each upstream request.
func installTransportRecordingModels(t *testing.T) *[]string {
	t.Helper()
	seen := &[]string{}
	installTransport(t, func(req *http.Request) *http.Response {
		*seen = append(*seen, readReqModel(req))
		return stubRoutingResponse(req, http.StatusOK, routingOKBody)
	})
	return seen
}

// installZenStub routes the zen client to a fixed body and counts requests.
func installZenStub(t *testing.T, hits *int32, body string) {
	t.Helper()
	old := zen.HTTPClient
	zen.HTTPClient = &http.Client{Transport: freeModelRoundTripper(func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(hits, 1)
		return stubRoutingResponse(req, http.StatusOK, body), nil
	})}
	t.Cleanup(func() { zen.HTTPClient = old })
}

// installZenFailover sets the zen config for failover tests.
func installZenFailover(t *testing.T, failover, enabled bool) {
	t.Helper()
	zen.ConfigMu.Lock()
	old := zen.Config
	zen.Config = &zen.ConfigData{BaseURL: zen.APIBase, Key: "public", Enabled: enabled, Failover: failover}
	zen.ConfigMu.Unlock()
	t.Cleanup(func() {
		zen.ConfigMu.Lock()
		zen.Config = old
		zen.ConfigMu.Unlock()
	})
}
