package integration

import (
	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/admin"
	"cline-go-proxy/internal/egress"
	"cline-go-proxy/internal/providers"
	"cline-go-proxy/internal/routing"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestProviderTransportFollowsHTTPClientReplacement(t *testing.T) {
	oldClient := egress.Client
	t.Cleanup(func() { egress.Client = oldClient })

	called := false
	egress.Client = &http.Client{Transport: freeModelRoundTripper(func(req *http.Request) (*http.Response, error) {
		called = true
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
	resp, err := providers.Call(&CustomProvider{ID: "rebind", Name: "Rebind", BaseURL: "https://provider-rebind.test/v1"}, map[string]any{
		"model": "m", "messages": []any{},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !called {
		t.Fatal("provider transport did not follow replacement egress.Client")
	}
}

// 自定义 provider 命中时优先走 provider 上游。
func TestCustomProviderServesModel(t *testing.T) {
	oldPool := accounts.Pool
	oldConfig := admin.GetProxyConfig()
	oldTransport := egress.Client.Transport
	t.Cleanup(func() {
		accounts.Pool = oldPool
		admin.SetProxyConfig(oldConfig)
		egress.Client.Transport = oldTransport
		_ = providers.Delete("prov_test1")
	})

	accounts.Pool = &AccountPool{Accounts: []*Account{{
		AccountID: "a", Email: "a@x.com", AccessToken: "t",
		ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), Status: "active",
	}}}
	admin.SetProxyConfig(admin.DefaultProxyConfig())

	upstreamModel := ""
	providerCalls := 0
	clineCalls := 0
	egress.Client.Transport = freeModelRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "provider.test" {
			providerCalls++
			body, _ := io.ReadAll(req.Body)
			var params map[string]any
			json.Unmarshal(body, &params)
			upstreamModel, _ = params["model"].(string)
			// 校验自定义头
			if req.Header.Get("X-Custom") != "abc" {
				t.Fatalf("custom header missing, got %q", req.Header.Get("X-Custom"))
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"id":"p1","choices":[{"message":{"role":"assistant","content":"from-provider"}}]}`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}
		clineCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"id":"c1","choices":[{"message":{"role":"assistant","content":"from-cline"}}]}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})

	providers.Upsert(&CustomProvider{
		ID: "prov_test1", Name: "TestProv", BaseURL: "http://provider.test/v1",
		APIKey: "sk-x", ModelIDs: []string{"custom-model-1"},
		Headers: map[string]string{"X-Custom": "abc"}, Enabled: true, Priority: 10,
	})

	params := map[string]any{"model": "custom-model-1", "max_tokens": 16, "messages": []any{map[string]any{"role": "user", "content": "hi"}}}
	resp, acc, err := routing.CallClineAPI(params, false)
	if err != nil {
		t.Fatalf("expected provider success, got %v", err)
	}
	defer resp.Body.Close()
	if acc != nil {
		t.Fatal("provider-served request should not consume a cline account")
	}
	if providerCalls != 1 || clineCalls != 0 {
		t.Fatalf("providerCalls=%d clineCalls=%d, want 1/0", providerCalls, clineCalls)
	}
	if upstreamModel != "custom-model-1" {
		t.Fatalf("upstream model = %q", upstreamModel)
	}
}

// provider 失败（冷却）后自动降级到回退链 → cline 池，客户端无感知。
func TestCustomProviderFailureFallsBackToChain(t *testing.T) {
	oldPool := accounts.Pool
	oldConfig := admin.GetProxyConfig()
	oldTransport := egress.Client.Transport
	t.Cleanup(func() {
		accounts.Pool = oldPool
		admin.SetProxyConfig(oldConfig)
		egress.Client.Transport = oldTransport
		_ = providers.Delete("prov_test2")
	})

	accounts.Pool = &AccountPool{Accounts: []*Account{{
		AccountID: "a", Email: "a@x.com", AccessToken: "t",
		ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), Status: "active",
	}}}
	admin.SetProxyConfig(admin.DefaultProxyConfig())

	egress.Client.Transport = freeModelRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "provider.test" {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(strings.NewReader(`{"error":"boom"}`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"id":"c1","choices":[{"message":{"role":"assistant","content":"from-cline"}}]}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})

	providers.Upsert(&CustomProvider{
		ID: "prov_test2", Name: "BrokenProv", BaseURL: "http://provider.test/v1",
		APIKey: "sk-x", ModelIDs: []string{"z-ai/glm-5.3-flash"},
		Enabled: true, Priority: 10,
	})
	defer providers.SetCooldown("prov_test2", "z-ai/glm-5.3-flash", time.Time{})

	params := map[string]any{"model": "z-ai/glm-5.3-flash", "max_tokens": 16, "messages": []any{map[string]any{"role": "user", "content": "hi"}}}
	resp, _, err := routing.CallClineAPI(params, false)
	if err != nil {
		t.Fatalf("expected fallback success, got %v", err)
	}
	defer resp.Body.Close()
}
