package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"routenest/internal/admin"
	"routenest/internal/config"
	"routenest/internal/requestlog"
	"routenest/internal/routing"
	"routenest/internal/zen"
	"strings"
	"testing"
)

func installZeroAccountPool(t *testing.T, models []Model) {
	t.Helper()
	old := getTestPool()
	t.Cleanup(func() { setTestPool(old) })
	setTestPool(&AccountPool{Models: models})
}

func installDisabledZen(t *testing.T) {
	t.Helper()
	zen.ConfigMu.Lock()
	old := zen.Config
	zen.Config = &zen.ConfigData{Enabled: false, BaseURL: zen.APIBase}
	zen.ConfigMu.Unlock()
	t.Cleanup(func() {
		zen.ConfigMu.Lock()
		zen.Config = old
		zen.ConfigMu.Unlock()
	})
}

func assertIngressStatus(t *testing.T, baseURL, path, body string, want int) string {
	t.Helper()
	status, responseBody := comboPost(t, baseURL+path, body)
	if status != want {
		t.Fatalf("%s status = %d, want %d: %s", path, status, want, responseBody)
	}
	return responseBody
}

func TestProviderOnlyIngressWorksWithZeroClineAccounts(t *testing.T) {
	const modelID = "provider-only/model"
	installZeroAccountPool(t, []Model{{
		ID: modelID, Provider: "ProviderOnly", Source: "provider",
		Cost: "pass", TierSignal: "manual", Status: "active",
	}})
	installConfig(t, admin.DefaultProxyConfig())
	installDisabledZen(t)
	installProviders(t, &CustomProvider{
		ID: "provider_only", Name: "ProviderOnly", BaseURL: "http://provider-only.test/v1",
		APIKey: "sk-provider-only", ModelIDs: []string{modelID}, Enabled: true,
	})

	var upstreamModels []string
	installTransport(t, func(req *http.Request) *http.Response {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read provider-only request: %v", err)
		}
		var params map[string]any
		if err := json.Unmarshal(body, &params); err != nil {
			t.Fatalf("decode provider-only request: %v", err)
		}
		model, _ := params["model"].(string)
		upstreamModels = append(upstreamModels, model)
		stream, _ := params["stream"].(bool)
		if stream {
			return stubSSE(req, "data: {\"id\":\"provider-stream\",\"object\":\"chat.completion.chunk\",\"model\":\"provider-only/model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"provider-stream-ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
		return stubResp(req, http.StatusOK,
			`{"id":"provider-only","object":"chat.completion","model":"provider-only/model","choices":[{"index":0,"message":{"role":"assistant","content":"provider-only-ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	})

	baseURL := protocolTestServer(t)
	tests := []struct {
		name string
		path string
		body string
		want string
	}{
		{name: "chat", path: "/v1/chat/completions", body: `{"model":"provider-only/model","messages":[{"role":"user","content":"hi"}]}`, want: "provider-only-ok"},
		{name: "messages", path: "/v1/messages", body: `{"model":"provider-only/model","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`, want: "provider-only-ok"},
		{name: "responses", path: "/v1/responses", body: `{"model":"provider-only/model","input":"hi"}`, want: "provider-only-ok"},
		{name: "chat stream", path: "/v1/chat/completions", body: `{"model":"provider-only/model","stream":true,"messages":[{"role":"user","content":"hi"}]}`, want: "provider-stream-ok"},
		{name: "messages stream", path: "/v1/messages", body: `{"model":"provider-only/model","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, want: "provider-stream-ok"},
		{name: "responses stream", path: "/v1/responses", body: `{"model":"provider-only/model","stream":true,"input":"hi"}`, want: "provider-stream-ok"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			responseBody := assertIngressStatus(t, baseURL, tc.path, tc.body, http.StatusOK)
			if !strings.Contains(responseBody, tc.want) {
				t.Fatalf("%s response missing %q: %s", tc.path, tc.want, responseBody)
			}
		})
	}
	if len(upstreamModels) != len(tests) {
		t.Fatalf("upstream calls = %d, want %d: %v", len(upstreamModels), len(tests), upstreamModels)
	}
	for _, model := range upstreamModels {
		if model != modelID {
			t.Fatalf("upstream models = %v, want only %q", upstreamModels, modelID)
		}
	}
}

func TestZenOnlyIngressWorksWithZeroClineAccounts(t *testing.T) {
	const modelID = "deepseek-v4-flash-free"
	installZeroAccountPool(t, []Model{{
		ID: modelID, Provider: "OpenCode", Source: "zen",
		Cost: "free", TierSignal: "suffix-free", Status: "active",
	}})
	installConfig(t, admin.DefaultProxyConfig())
	installProviders(t)
	baseURL := protocolTestServer(t)

	zen.ConfigMu.Lock()
	oldConfig := zen.Config
	zen.Config = &zen.ConfigData{Enabled: true, BaseURL: "http://zen-only.test/inference/v1", Key: "public"}
	zen.ConfigMu.Unlock()
	zen.TransportMu.Lock()
	oldClient := zen.HTTPClient
	zen.HTTPClient = &http.Client{Transport: freeModelRoundTripper(func(req *http.Request) (*http.Response, error) {
		return stubSSE(req, "data: {\"id\":\"zen-only\",\"model\":\"deepseek-v4-flash-free\",\"choices\":[{\"delta\":{\"content\":\"zen-only-ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"), nil
	})}
	zen.TransportMu.Unlock()
	t.Cleanup(func() {
		zen.ConfigMu.Lock()
		zen.Config = oldConfig
		zen.ConfigMu.Unlock()
		zen.TransportMu.Lock()
		zen.HTTPClient = oldClient
		zen.TransportMu.Unlock()
	})

	responseBody := assertIngressStatus(t, baseURL, "/v1/chat/completions",
		`{"model":"deepseek-v4-flash-free","messages":[{"role":"user","content":"hi"}]}`, http.StatusOK)
	if !strings.Contains(responseBody, "zen-only-ok") {
		t.Fatalf("Zen-only response missing content: %s", responseBody)
	}
}

func TestStrictClinePinWithoutCredentialsReturnsAvailabilityAndExclusion(t *testing.T) {
	installZeroAccountPool(t, nil)
	installConfig(t, admin.DefaultProxyConfig())
	installDisabledZen(t)
	installProviders(t)

	baseURL := protocolTestServer(t)
	before := len(requestlog.Snapshot())
	responseBody := assertIngressStatus(t, baseURL, "/v1/chat/completions",
		`{"model":"missing/strict@cline","messages":[{"role":"user","content":"hi"}]}`, http.StatusServiceUnavailable)
	if strings.Contains(responseBody, "No accounts in pool") {
		t.Fatalf("strict Cline pin leaked the legacy account-pool error: %s", responseBody)
	}

	logs := requestlog.Snapshot()
	if len(logs) != before+1 {
		t.Fatalf("request log count = %d, want %d", len(logs), before+1)
	}
	found := false
	for _, exclusion := range logs[0].PlannerExclusions {
		if exclusion.ProviderID == "cline" && exclusion.Model == "missing/strict" && exclusion.Reason == string(routing.ExclusionCredentialMissing) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("planner exclusions = %+v, want cline credential_missing", logs[0].PlannerExclusions)
	}
}

func TestNoEligibleProviderReturnsServiceUnavailableAcrossIngresses(t *testing.T) {
	modes := []struct {
		name string
		mode config.DispatchMode
	}{
		{name: "default shadow"},
		{name: "legacy", mode: config.DispatchModeLegacy},
		{name: "enforce", mode: config.DispatchModeEnforce},
	}
	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			installZeroAccountPool(t, nil)
			cfg := admin.DefaultProxyConfig()
			if mode.mode != "" {
				cfg.DispatchMode = mode.mode
			}
			installConfig(t, cfg)
			installDisabledZen(t)
			installProviders(t)

			baseURL := protocolTestServer(t)
			ingresses := []struct {
				name string
				path string
				body string
			}{
				{name: "chat", path: "/v1/chat/completions", body: `{"model":"missing/any","messages":[{"role":"user","content":"hi"}]}`},
				{name: "messages", path: "/v1/messages", body: `{"model":"missing/any","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`},
				{name: "responses", path: "/v1/responses", body: `{"model":"missing/any","input":"hi"}`},
			}
			for _, ingress := range ingresses {
				t.Run(ingress.name, func(t *testing.T) {
					responseBody := assertIngressStatus(t, baseURL, ingress.path, ingress.body, http.StatusServiceUnavailable)
					if strings.Contains(responseBody, "No accounts in pool") {
						t.Fatalf("%s leaked the legacy account-pool error: %s", ingress.path, responseBody)
					}
				})
			}
		})
	}
}

func TestHealthRemainsReadyWithProviderAndZeroClineAccounts(t *testing.T) {
	const modelID = "health/provider-only"
	installZeroAccountPool(t, []Model{{
		ID: modelID, Provider: "HealthProvider", Source: "provider",
		Cost: "pass", TierSignal: "manual", Status: "active",
	}})
	installConfig(t, admin.DefaultProxyConfig())
	installDisabledZen(t)
	installProviders(t, &CustomProvider{
		ID: "health_provider", Name: "HealthProvider", BaseURL: "http://health-provider.test/v1",
		APIKey: "sk-health", ModelIDs: []string{modelID}, Enabled: true,
	})

	baseURL := protocolTestServer(t)
	resp, err := http.Get(baseURL + "/health")
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", resp.StatusCode)
	}
	var payload struct {
		Status         string `json:"status"`
		ActiveAccounts int    `json:"activeAccounts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if payload.Status != "ok" || payload.ActiveAccounts != 0 {
		t.Fatalf("health payload = %+v, want status ok and zero active accounts", payload)
	}
}
