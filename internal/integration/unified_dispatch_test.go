package integration

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"routenest/internal/admin"
	"routenest/internal/config"
	"routenest/internal/egress"
	"routenest/internal/providers"
	"routenest/internal/routing"
	"routenest/internal/zen"
)

func installEnforceConfig(t *testing.T, configure func(*admin.ProxyConfig)) {
	t.Helper()
	cfg := admin.DefaultProxyConfig()
	cfg.DispatchMode = config.DispatchModeEnforce
	if configure != nil {
		configure(cfg)
	}
	installConfig(t, cfg)
}

func assertProtocolResponse(t *testing.T, baseURL, path, body, want string) {
	t.Helper()
	status, responseBody := comboPost(t, baseURL+path, body)
	if status != http.StatusOK {
		t.Fatalf("%s status = %d, want 200: %s", path, status, responseBody)
	}
	if !strings.Contains(responseBody, want) {
		t.Fatalf("%s response missing %q: %s", path, want, responseBody)
	}
}

func isInferenceUpstreamRequest(req *http.Request) bool {
	return req != nil && req.Method == http.MethodPost
}

func TestUnifiedDispatchEnforceCustomProviderAcrossIngresses(t *testing.T) {
	const modelID = "unified/custom-model"
	installPool(t, []Model{{
		ID: modelID, Provider: "Unified", Source: "provider",
		Cost: "pass", TierSignal: "manual", Status: "active",
	}})
	installEnforceConfig(t, nil)
	installProviders(t, &CustomProvider{
		ID: "unified_custom", Name: "Unified", BaseURL: "http://unified.test/v1",
		APIKey: "sk-unified", ModelIDs: []string{modelID}, Enabled: true,
	})

	var models []string
	installTransport(t, func(req *http.Request) *http.Response {
		if isInferenceUpstreamRequest(req) {
			models = append(models, readReqModel(req))
		}
		return stubResp(req, http.StatusOK,
			`{"id":"custom-ok","object":"chat.completion","model":"unified/custom-model","choices":[{"index":0,"message":{"role":"assistant","content":"custom-ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	})

	baseURL := protocolTestServer(t)
	assertProtocolResponse(t, baseURL, "/v1/chat/completions",
		`{"model":"unified/custom-model","messages":[{"role":"user","content":"hi"}]}`, "custom-ok")
	assertProtocolResponse(t, baseURL, "/v1/messages",
		`{"model":"unified/custom-model","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`, "custom-ok")
	assertProtocolResponse(t, baseURL, "/v1/responses",
		`{"model":"unified/custom-model","input":"hi"}`, "custom-ok")

	if len(models) != 3 {
		t.Fatalf("upstream calls = %d, want one per ingress: %v", len(models), models)
	}
	for _, model := range models {
		if model != modelID {
			t.Fatalf("upstream models = %v, want identical logical order across ingresses", models)
		}
	}
}

func TestUnifiedDispatchEnforceFreeFirstAndStrictProviderPin(t *testing.T) {
	const modelID = "shared/free-first"
	installPool(t, []Model{{
		ID: modelID, Provider: "Paid", Source: "provider",
		Cost: "pass", TierSignal: "manual", Status: "active",
	}})
	installEnforceConfig(t, nil)
	installProviders(t,
		&CustomProvider{ID: "free_first_paid", Name: "Paid", BaseURL: "http://paid.test/v1", APIKey: "sk-paid", ModelIDs: []string{modelID}, Enabled: true, Priority: 1},
		&CustomProvider{ID: "free_first_free", Name: "Free", BaseURL: "http://free.test/v1", APIKey: "sk-free", ModelIDs: []string{modelID}, Enabled: true, Priority: 100},
	)
	if err := providers.SetModelCost("free_first_paid", modelID, "pass"); err != nil {
		t.Fatal(err)
	}
	if err := providers.SetModelCost("free_first_free", modelID, "free"); err != nil {
		t.Fatal(err)
	}

	var hosts []string
	installTransport(t, func(req *http.Request) *http.Response {
		if isInferenceUpstreamRequest(req) {
			hosts = append(hosts, req.URL.Host)
		}
		return stubResp(req, http.StatusOK,
			`{"id":"route-ok","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"route-ok"},"finish_reason":"stop"}]}`)
	})
	baseURL := protocolTestServer(t)
	assertProtocolResponse(t, baseURL, "/v1/chat/completions",
		`{"model":"shared/free-first","messages":[{"role":"user","content":"hi"}]}`, "route-ok")
	assertProtocolResponse(t, baseURL, "/v1/chat/completions",
		`{"model":"shared/free-first@Paid","messages":[{"role":"user","content":"hi"}]}`, "route-ok")

	if got := strings.Join(hosts, ","); got != "free.test,paid.test" {
		t.Fatalf("upstream hosts = %q, want free-first then strict paid pin", got)
	}
}

func TestUnifiedDispatchEnforceOnlyFreeUsesProviderScopedOfferingAcrossIngresses(t *testing.T) {
	const modelID = "gpt-5-turbo"
	installPool(t, []Model{{
		ID: modelID, Provider: "OpenCode", Source: "zen",
		Cost: "pass", TierSignal: "pricing-paid", Status: "active",
	}})
	installEnforceConfig(t, func(cfg *admin.ProxyConfig) { cfg.OnlyFree = true })
	installProviders(t, &CustomProvider{
		ID: "scoped_free", Name: "ScopedFree", BaseURL: "http://scoped-free.test/v1",
		APIKey: "sk-free", ModelIDs: []string{modelID}, Enabled: true,
	})
	if err := providers.SetModelCost("scoped_free", modelID, "free"); err != nil {
		t.Fatal(err)
	}

	var hosts []string
	installTransport(t, func(req *http.Request) *http.Response {
		if isInferenceUpstreamRequest(req) {
			hosts = append(hosts, req.URL.Host)
		}
		return stubResp(req, http.StatusOK,
			`{"id":"scoped-free-ok","object":"chat.completion","model":"gpt-5-turbo","choices":[{"index":0,"message":{"role":"assistant","content":"scoped-free-ok"},"finish_reason":"stop"}]}`)
	})
	baseURL := protocolTestServer(t)
	assertProtocolResponse(t, baseURL, "/v1/chat/completions",
		`{"model":"gpt-5-turbo","messages":[{"role":"user","content":"hi"}]}`, "scoped-free-ok")
	assertProtocolResponse(t, baseURL, "/v1/messages",
		`{"model":"gpt-5-turbo@scoped_free","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`, "scoped-free-ok")
	assertProtocolResponse(t, baseURL, "/v1/responses",
		`{"model":"gpt-5-turbo","input":"hi"}`, "scoped-free-ok")
	beforePaidPin := len(hosts)
	status, _ := comboPost(t, baseURL+"/v1/chat/completions",
		`{"model":"gpt-5-turbo@opencode","messages":[{"role":"user","content":"hi"}]}`)
	if status == http.StatusOK {
		t.Fatal("onlyFree unexpectedly allowed the paid provider pin")
	}
	if len(hosts) != beforePaidPin {
		t.Fatalf("paid provider pin reached an upstream: %v", hosts[beforePaidPin:])
	}

	if got := strings.Join(hosts, ","); got != "scoped-free.test,scoped-free.test,scoped-free.test" {
		t.Fatalf("upstream hosts = %q, want provider-scoped free offering for every ingress", got)
	}
}

func TestUnifiedDispatchEnforcePreservesProviderSessionAffinity(t *testing.T) {
	const modelID = "affinity/provider-model"
	installPool(t, []Model{{ID: modelID, Provider: "AffinityA", Source: "provider", Cost: "pass", Status: "active"}})
	installEnforceConfig(t, func(cfg *admin.ProxyConfig) { cfg.SessionAffinity.Enabled = true })
	providerA := &CustomProvider{ID: "affinity_provider_a", Name: "AffinityA", BaseURL: "http://affinity-a.test/v1", APIKey: "sk-a", ModelIDs: []string{modelID}, Enabled: true, Priority: 1}
	providerB := &CustomProvider{ID: "affinity_provider_b", Name: "AffinityB", BaseURL: "http://affinity-b.test/v1", APIKey: "sk-b", ModelIDs: []string{modelID}, Enabled: true, Priority: 2}
	installProviders(t, providerA, providerB)

	var hosts []string
	installTransport(t, func(req *http.Request) *http.Response {
		if isInferenceUpstreamRequest(req) {
			hosts = append(hosts, req.URL.Host)
		}
		return stubResp(req, http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"affinity-ok"},"finish_reason":"stop"}]}`)
	})
	baseURL := protocolTestServer(t)
	body := `{"model":"affinity/provider-model","messages":[{"role":"user","content":"stable provider session"}]}`
	assertProtocolResponse(t, baseURL, "/v1/chat/completions", body, "affinity-ok")
	providerA.Priority = 100
	providerB.Priority = 1
	providers.Upsert(providerA)
	providers.Upsert(providerB)
	assertProtocolResponse(t, baseURL, "/v1/chat/completions", body, "affinity-ok")
	if got := strings.Join(hosts, ","); got != "affinity-a.test,affinity-a.test" {
		t.Fatalf("provider affinity hosts = %q, want the first provider retained", got)
	}
}

func TestUnifiedDispatchEnforcePreservesClineAccountAffinity(t *testing.T) {
	const modelID = "affinity/cline-model"
	accountA := comboTestAccount("affinity-a")
	accountA.AccessToken = "token-affinity-a"
	accountB := comboTestAccount("affinity-b")
	accountB.AccessToken = "token-affinity-b"
	installPool(t, []Model{{ID: modelID, Source: "remote", Cost: "pass", Status: "active"}}, accountA, accountB)
	installEnforceConfig(t, func(cfg *admin.ProxyConfig) { cfg.SessionAffinity.Enabled = true })

	var tokens []string
	oldTransport := egress.Client.Transport
	t.Cleanup(func() { egress.Client.Transport = oldTransport })
	egress.Client.Transport = freeModelRoundTripper(func(req *http.Request) (*http.Response, error) {
		tokens = append(tokens, strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer "))
		return stubResp(req, http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"account-affinity-ok"},"finish_reason":"stop"}]}`), nil
	})
	baseURL := protocolTestServer(t)
	body := `{"model":"affinity/cline-model","messages":[{"role":"user","content":"stable account session"}]}`
	assertProtocolResponse(t, baseURL, "/v1/chat/completions", body, "account-affinity-ok")
	assertProtocolResponse(t, baseURL, "/v1/chat/completions", body, "account-affinity-ok")
	if got := strings.Join(tokens, ","); got != "token-affinity-a,token-affinity-a" {
		t.Fatalf("account affinity tokens = %q, want first account retained", got)
	}
}

func TestUnifiedDispatchEnforceComboPreservesSameModelPinOrder(t *testing.T) {
	const modelID = "shared/combo-model"
	installPool(t, []Model{{ID: modelID, Provider: "ComboOne", Source: "provider", Cost: "pass", Status: "active"}})
	installEnforceConfig(t, func(cfg *admin.ProxyConfig) {
		cfg.Combos = []ModelCombo{{
			ID: "unified-combo", Name: "Unified combo", Enabled: true,
			Entries: []ComboEntry{
				{Model: modelID, Source: "provider", Provider: "ComboOne"},
				{Model: modelID, Source: "provider", Provider: "ComboTwo"},
			},
		}}
	})
	installProviders(t,
		&CustomProvider{ID: "combo_one", Name: "ComboOne", BaseURL: "http://combo-one.test/v1", APIKey: "sk-one", ModelIDs: []string{modelID}, Enabled: true},
		&CustomProvider{ID: "combo_two", Name: "ComboTwo", BaseURL: "http://combo-two.test/v1", APIKey: "sk-two", ModelIDs: []string{modelID}, Enabled: true},
	)

	var hosts []string
	installTransport(t, func(req *http.Request) *http.Response {
		if isInferenceUpstreamRequest(req) {
			hosts = append(hosts, req.URL.Host)
		}
		if req.URL.Host == "combo-one.test" {
			return stubResp(req, http.StatusServiceUnavailable, `{"error":"busy"}`)
		}
		return stubResp(req, http.StatusOK,
			`{"id":"combo-ok","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"combo-ok"},"finish_reason":"stop"}]}`)
	})
	baseURL := protocolTestServer(t)
	assertProtocolResponse(t, baseURL, "/v1/chat/completions",
		`{"model":"unified-combo","messages":[{"role":"user","content":"hi"}]}`, "combo-ok")
	if got := strings.Join(hosts, ","); got != "combo-one.test,combo-two.test" {
		t.Fatalf("combo upstream order = %q, want configured pin order", got)
	}
}

func TestUnifiedDispatchEnforceRequestErrorRules(t *testing.T) {
	t.Run("continue overrides deterministic 400", func(t *testing.T) {
		const modelID = "rules/continue-model"
		installPool(t, []Model{{ID: modelID, Provider: "RuleOne", Source: "provider", Cost: "pass", Status: "active"}})
		installEnforceConfig(t, func(cfg *admin.ProxyConfig) {
			cfg.RequestErrorRules = []config.RequestErrorRule{{
				StatusCodes: []int{http.StatusBadRequest}, Match: "try next", Action: config.ErrorActionContinue,
			}}
		})
		installProviders(t,
			&CustomProvider{ID: "rule_continue_one", Name: "RuleOne", BaseURL: "http://rule-one.test/v1", APIKey: "sk-one", ModelIDs: []string{modelID}, Enabled: true, Priority: 1},
			&CustomProvider{ID: "rule_continue_two", Name: "RuleTwo", BaseURL: "http://rule-two.test/v1", APIKey: "sk-two", ModelIDs: []string{modelID}, Enabled: true, Priority: 2},
		)
		var hosts []string
		installTransport(t, func(req *http.Request) *http.Response {
			if isInferenceUpstreamRequest(req) {
				hosts = append(hosts, req.URL.Host)
			}
			if req.URL.Host == "rule-one.test" {
				return stubResp(req, http.StatusBadRequest, `{"error":"try next"}`)
			}
			return stubResp(req, http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"rule-ok"},"finish_reason":"stop"}]}`)
		})
		baseURL := protocolTestServer(t)
		assertProtocolResponse(t, baseURL, "/v1/chat/completions",
			`{"model":"rules/continue-model","messages":[{"role":"user","content":"hi"}]}`, "rule-ok")
		if got := strings.Join(hosts, ","); got != "rule-one.test,rule-two.test" {
			t.Fatalf("rule continue hosts = %q", got)
		}
	})

	t.Run("stop blocks retryable 500", func(t *testing.T) {
		const modelID = "rules/stop-model"
		installPool(t, []Model{{ID: modelID, Provider: "StopOne", Source: "provider", Cost: "pass", Status: "active"}})
		installEnforceConfig(t, func(cfg *admin.ProxyConfig) {
			cfg.RequestErrorRules = []config.RequestErrorRule{{
				StatusCodes: []int{http.StatusInternalServerError}, Match: "stop now", Action: config.ErrorActionStop,
			}}
		})
		installProviders(t,
			&CustomProvider{ID: "rule_stop_one", Name: "StopOne", BaseURL: "http://stop-one.test/v1", APIKey: "sk-one", ModelIDs: []string{modelID}, Enabled: true, Priority: 1},
			&CustomProvider{ID: "rule_stop_two", Name: "StopTwo", BaseURL: "http://stop-two.test/v1", APIKey: "sk-two", ModelIDs: []string{modelID}, Enabled: true, Priority: 2},
		)
		var hosts []string
		installTransport(t, func(req *http.Request) *http.Response {
			if isInferenceUpstreamRequest(req) {
				hosts = append(hosts, req.URL.Host)
			}
			return stubResp(req, http.StatusInternalServerError, `{"error":"stop now"}`)
		})
		baseURL := protocolTestServer(t)
		status, _ := comboPost(t, baseURL+"/v1/chat/completions",
			`{"model":"rules/stop-model","messages":[{"role":"user","content":"hi"}]}`)
		if status == http.StatusOK {
			t.Fatal("stop rule unexpectedly allowed fallback success")
		}
		if got := strings.Join(hosts, ","); got != "stop-one.test" {
			t.Fatalf("rule stop hosts = %q, want first provider only", got)
		}
	})
}

func TestUnifiedDispatchRollbackModesDoNotFailClosedOnShadowPlanErrors(t *testing.T) {
	for _, mode := range []config.DispatchMode{config.DispatchModeShadow, config.DispatchModeLegacy} {
		t.Run(string(mode), func(t *testing.T) {
			const modelID = "rollback/ambiguous-pin"
			installPool(t, []Model{{ID: modelID, Provider: "Shared", Source: "provider", Cost: "pass", Status: "active"}})
			cfg := admin.DefaultProxyConfig()
			cfg.DispatchMode = mode
			installConfig(t, cfg)
			installProviders(t,
				&CustomProvider{ID: "rollback_one_" + string(mode), Name: "Shared", BaseURL: "http://rollback-one.test/v1", APIKey: "sk-one", ModelIDs: []string{modelID}, Enabled: true, Priority: 1},
				&CustomProvider{ID: "rollback_two_" + string(mode), Name: "Shared", BaseURL: "http://rollback-two.test/v1", APIKey: "sk-two", ModelIDs: []string{modelID}, Enabled: true, Priority: 2},
			)
			var hosts []string
			installTransport(t, func(req *http.Request) *http.Response {
				if isInferenceUpstreamRequest(req) {
					hosts = append(hosts, req.URL.Host)
				}
				return stubResp(req, http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"rollback-ok"},"finish_reason":"stop"}]}`)
			})
			baseURL := protocolTestServer(t)
			assertProtocolResponse(t, baseURL, "/v1/chat/completions",
				`{"model":"rollback/ambiguous-pin@Shared","messages":[{"role":"user","content":"hi"}]}`, "rollback-ok")
			if len(hosts) != 1 || hosts[0] != "rollback-one.test" {
				t.Fatalf("rollback mode %s hosts = %v", mode, hosts)
			}
		})
	}
}

func TestUnifiedDispatchEnforceClineResponses(t *testing.T) {
	const modelID = "cline/enforce-model"
	installPool(t, []Model{{ID: modelID, Source: "remote", Cost: "pass", Status: "active"}}, comboTestAccount("cline-enforce"))
	installEnforceConfig(t, nil)

	oldTransport := egress.Client.Transport
	t.Cleanup(func() { egress.Client.Transport = oldTransport })
	egress.Client.Transport = freeModelRoundTripper(func(req *http.Request) (*http.Response, error) {
		if got := readReqModel(req); got != modelID {
			t.Fatalf("cline upstream model = %q, want %q", got, modelID)
		}
		return stubResp(req, http.StatusOK,
			`{"id":"cline-ok","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"cline-ok"},"finish_reason":"stop"}]}`), nil
	})
	baseURL := protocolTestServer(t)
	assertProtocolResponse(t, baseURL, "/v1/responses", `{"model":"cline/enforce-model","input":"hi"}`, "cline-ok")
}

func TestUnifiedDispatchEnforceZenChat(t *testing.T) {
	const modelID = "deepseek-v4-flash-free"
	installPool(t, []Model{{ID: modelID, Source: "zen", Cost: "free", TierSignal: "suffix-free", Status: "active"}})
	installEnforceConfig(t, nil)
	baseURL := protocolTestServer(t)

	zen.ConfigMu.Lock()
	oldConfig := zen.Config
	zen.Config = &zen.ConfigData{Enabled: true, BaseURL: "http://zen.test/inference/v1", Key: "public"}
	zen.ConfigMu.Unlock()
	zen.TransportMu.Lock()
	oldClient := zen.HTTPClient
	zen.HTTPClient = &http.Client{Transport: freeModelRoundTripper(func(req *http.Request) (*http.Response, error) {
		if got := readReqModel(req); got != modelID {
			t.Fatalf("zen upstream model = %q, want %q", got, modelID)
		}
		return stubSSE(req, "data: {\"id\":\"zen-ok\",\"model\":\"deepseek-v4-flash-free\",\"choices\":[{\"delta\":{\"content\":\"zen-ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"), nil
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

	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		// This test failed on hosted Linux runners but not locally. Record why the planner found
		// no candidate so the next failure explains itself instead of only reporting a 503.
		_, diagnostics, err := routing.SnapshotAndPlan(routing.SnapshotOptions{ModelID: modelID}, routing.ModelFallbackChain(modelID))
		cfg := zen.GetConfig()
		t.Logf("planner diagnostics: err=%v candidates=%v exclusions=%+v", err, diagnostics.Candidates, diagnostics.Exclusions)
		t.Logf("zen state: enabled=%v key=%q sessions=%d routeModel=%q remoteActive=%v currentModels=%d",
			cfg.Enabled, cfg.Key, len(cfg.SessionIDs), zen.RouteModel(modelID), zen.RemoteActive(), len(zen.CurrentModels()))
	})

	assertProtocolResponse(t, baseURL, "/v1/chat/completions",
		`{"model":"deepseek-v4-flash-free","messages":[{"role":"user","content":"hi"}]}`, "zen-ok")
}

func TestUnifiedDispatchEnforceAnthropicEmptyStreamFallbackAndCommitBoundary(t *testing.T) {
	const (
		primary = "cline/stream-primary"
		backup  = "cline/stream-backup"
	)
	account := comboTestAccount("stream-enforce")
	installPool(t, []Model{
		{ID: primary, Source: "remote", Cost: "pass", Status: "active"},
		{ID: backup, Source: "remote", Cost: "pass", Status: "active"},
	}, account)
	installEnforceConfig(t, func(cfg *admin.ProxyConfig) { cfg.ModelChain = []string{backup} })

	var attempts []string
	oldTransport := egress.Client.Transport
	t.Cleanup(func() { egress.Client.Transport = oldTransport })
	egress.Client.Transport = freeModelRoundTripper(func(req *http.Request) (*http.Response, error) {
		model := readReqModel(req)
		attempts = append(attempts, model)
		if model == primary {
			return stubSSE(req, "data: {\"error\":{\"code\":504,\"message\":\"idle\"}}\n\n"), nil
		}
		return stubSSE(req, "data: {\"id\":\"stream-ok\",\"model\":\"cline/stream-backup\",\"choices\":[{\"delta\":{\"content\":\"stream-ok\"}}]}\n\ndata: [DONE]\n\n"), nil
	})
	baseURL := protocolTestServer(t)
	assertProtocolResponse(t, baseURL, "/v1/messages",
		`{"model":"cline/stream-primary","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, "stream-ok")
	if got := strings.Join(attempts, ","); got != primary+","+backup {
		t.Fatalf("empty-stream attempts = %q, want primary then backup", got)
	}
	routing.SetModelCooldown(account, primary, time.Now().Add(-time.Second))
	_ = routing.ModelCooldownActive(account, primary)

	attempts = nil
	egress.Client.Transport = freeModelRoundTripper(func(req *http.Request) (*http.Response, error) {
		attempts = append(attempts, readReqModel(req))
		return stubSSE(req, "data: {\"id\":\"partial\",\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\ndata: {\"error\":{\"code\":504,\"message\":\"late\"}}\n\n"), nil
	})
	assertProtocolResponse(t, baseURL, "/v1/messages",
		`{"model":"cline/stream-primary","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, "event: error")
	if len(attempts) != 1 || attempts[0] != primary {
		t.Fatalf("committed stream attempts = %v, want primary only", attempts)
	}
}
