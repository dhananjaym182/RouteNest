package main

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"cline-go-proxy/internal/config"
	"cline-go-proxy/internal/egress"
	"cline-go-proxy/internal/providers"
)

func installRuleConfig(t *testing.T, rules ...config.RequestErrorRule) {
	t.Helper()
	cfg := defaultProxyConfig()
	cfg.RequestErrorRules = rules
	cfg.Normalize()
	setProxyConfig(cfg)
	t.Cleanup(func() { setProxyConfig(defaultProxyConfig()) })
}

func TestErrorRuleStopsAndCooldownsProviderLoop(t *testing.T) {
	var mu sync.Mutex
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte(`{"error":{"message":"budget exceeded for plan"}}`))
	}))
	defer srv.Close()

	installProviders(t,
		&CustomProvider{ID: "p1", Name: "One", BaseURL: srv.URL, APIKey: "k1", Enabled: true, Priority: 1, ModelIDs: []string{"x"}},
		&CustomProvider{ID: "p2", Name: "Two", BaseURL: srv.URL, APIKey: "k2", Enabled: true, Priority: 2, ModelIDs: []string{"x"}},
	)
	params := map[string]any{"model": "x", "messages": []any{}}

	// Without a rule both providers are attempted and the API error is returned.
	installRuleConfig(t)
	_, err, attempted, _ := providers.CallAPI(map[string]any{"model": "x", "messages": []any{}}, false)
	if !attempted || err == nil {
		t.Fatalf("control: attempted=%v err=%v", attempted, err)
	}
	if _, stopped := egress.StopCause(err); stopped {
		t.Fatal("control: error must not be stop-wrapped without rules")
	}
	mu.Lock()
	controlHits := hits
	mu.Unlock()
	if controlHits != 2 {
		t.Fatalf("control: both providers must be tried, hits=%d", controlHits)
	}

	// A stop rule aborts after the first provider and wraps its API error.
	mu.Lock()
	hits = 0
	mu.Unlock()
	installRuleConfig(t, config.RequestErrorRule{
		StatusCodes: []int{http.StatusTeapot}, Match: "budget exceeded", Action: config.ErrorActionStop,
	})
	_, err, attempted, _ = providers.CallAPI(params, false)
	if !attempted || err == nil {
		t.Fatalf("stop rule: attempted=%v err=%v", attempted, err)
	}
	cause, stopped := egress.StopCause(err)
	if !stopped || egress.ErrorStatus(cause) != http.StatusTeapot {
		t.Fatalf("stop rule: error not wrapped with API cause: %v", err)
	}
	mu.Lock()
	stopHits := hits
	mu.Unlock()
	if stopHits != 1 {
		t.Fatalf("stop rule must skip remaining providers, hits=%d", stopHits)
	}

	// continue_and_cooldown tries both providers and records the cooldown.
	mu.Lock()
	hits = 0
	mu.Unlock()
	installRuleConfig(t, config.RequestErrorRule{
		StatusCodes: []int{http.StatusTeapot}, Match: "budget exceeded", Action: config.ErrorActionContinueAndCooldown,
	})
	_, err, attempted, _ = providers.CallAPI(params, false)
	if !attempted || err == nil {
		t.Fatalf("continue rule: attempted=%v err=%v", attempted, err)
	}
	if _, stopped := egress.StopCause(err); stopped {
		t.Fatalf("continue rule leaked stop marker: %v", err)
	}
	mu.Lock()
	continueHits := hits
	mu.Unlock()
	if continueHits != 2 {
		t.Fatalf("continue rule must try all providers, hits=%d", continueHits)
	}
	providers.Mu.Lock()
	until, cooled := providers.Cooldowns["p1:x"]
	providers.Mu.Unlock()
	if !cooled || !until.After(time.Now()) {
		t.Fatalf("rule cooldown not recorded: until=%v cooled=%v", until, cooled)
	}
	if stop, cool, ok := egress.RuleDecision(egress.NewAPIError(http.StatusTeapot, "budget exceeded")); stop || !cool || !ok {
		t.Fatalf("decision sanity after cooldown: stop:%v cool:%v ok:%v", stop, cool, ok)
	}
}

func TestErrorRuleStopPreventsAccountAndModelFallback(t *testing.T) {
	oldTransport := egress.Client.Transport
	t.Cleanup(func() { egress.Client.Transport = oldTransport })
	installPool(t, []Model{{ID: "stop/model", Status: "active"}}, &Account{
		AccountID: "fallback", Email: "fallback@example.test", AccessToken: "token",
		ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), Status: "active",
	})
	installProviders(t, &CustomProvider{
		ID: "stop-provider", Name: "StopProvider", BaseURL: "https://provider-stop.test/v1",
		Enabled: true, ModelIDs: []string{"stop/model"},
	})
	installRuleConfig(t, config.RequestErrorRule{
		StatusCodes: []int{http.StatusTeapot}, Match: "terminal budget", Action: config.ErrorActionStop,
	})

	providerHits, accountHits := 0, 0
	egress.Client.Transport = freeModelRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "provider-stop.test" {
			providerHits++
			return &http.Response{
				StatusCode: http.StatusTeapot,
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"terminal budget"}}`)),
				Header:     make(http.Header), Request: req,
			}, nil
		}
		accountHits++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"unexpected"}}]}`)),
			Header:     make(http.Header), Request: req,
		}, nil
	})

	_, _, err := callClineAPI(map[string]any{"model": "stop/model", "messages": []any{}}, false)
	if err == nil {
		t.Fatal("stop rule must surface the provider error")
	}
	var apiErr *egress.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusTeapot {
		t.Fatalf("outer router must return the underlying API error, got %T: %v", err, err)
	}
	if _, stopped := egress.StopCause(err); stopped {
		t.Fatalf("outer router leaked stop marker: %v", err)
	}
	if providerHits != 1 || accountHits != 0 {
		t.Fatalf("dispatch counts provider/account = %d/%d, want 1/0", providerHits, accountHits)
	}
}
