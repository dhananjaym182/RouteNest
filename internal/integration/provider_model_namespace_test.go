package integration

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"routenest/internal/admin"
	"routenest/internal/config"
	"routenest/internal/platform"
	"routenest/internal/providers"
	"routenest/internal/routing"
	"routenest/internal/zen"
)

const (
	namespaceAntSeedID       = "prov_namespace_antseed"
	namespaceAntSeedHost     = "antseed.namespace.test"
	namespaceOpenCodeHost    = "opencode.namespace.test"
	namespaceSiblingHost     = "sibling.namespace.test"
	namespacePrimaryModel    = "namespace-primary"
	namespaceBackupModel     = "namespace-global-backup"
	namespaceStrictModel     = "namespace-strict@revision"
	namespaceComboModel      = "namespace-combo@revision"
	namespaceFallbackModel   = "namespace-fallback@revision"
	namespaceFreePolicyModel = "namespace-onlyfree@revision"
)

type namespaceIngressCall struct {
	host  string
	model string
}

type namespaceIngressFixture struct {
	baseURL            string
	providerID         string
	provider           *CustomProvider
	models             []string
	mu                 sync.Mutex
	calls              []namespaceIngressCall
	failures           map[string]int
	primaryUnavailable bool
}

func newNamespaceIngressFixture(t *testing.T, mode config.DispatchMode) *namespaceIngressFixture {
	t.Helper()
	t.Setenv(platform.DataDirEnv, t.TempDir())
	previous := config.Snapshot()
	// Serial fixtures replace private test state because publication deliberately
	// cannot release a provider namespace reservation. No server is running here.
	config.Mu.Lock()
	*config.Held() = *config.DefaultProxy()
	config.Mu.Unlock()
	t.Cleanup(func() {
		config.Mu.Lock()
		*config.Held() = *previous.Clone()
		config.Mu.Unlock()
	})

	f := &namespaceIngressFixture{
		providerID: namespaceAntSeedID + "_" + string(mode),
		models:     []string{"deepseek-v4-flash", "org/deepseek-v4.1@revision", namespaceStrictModel, namespaceComboModel, namespaceFallbackModel, namespaceFreePolicyModel},
		failures:   map[string]int{},
	}
	poolModels := []Model{
		{ID: namespacePrimaryModel, Source: "remote", Cost: "free", TierSignal: "manual", Status: "active"},
		{ID: namespaceBackupModel, Source: "remote", Cost: "free", TierSignal: "manual", Status: "active"},
	}
	for _, model := range f.models {
		poolModels = append(poolModels, Model{ID: model, Provider: "OpenCode", Source: "zen", Cost: "free", TierSignal: "manual", Status: "active"})
	}
	installPool(t, poolModels)
	cfg := admin.DefaultProxyConfig()
	cfg.DispatchMode = mode
	cfg.ModelChain = []string{namespaceBackupModel}
	cfg.ProviderModelPrefixes = map[string]string{"as": f.providerID, "op": "opencode", "cl": "cline"}
	installConfig(t, cfg)
	if err := config.SaveProxy(platform.ResolveDataPath(config.ProxyPath, nil), cfg); err != nil {
		t.Fatal(err)
	}
	f.provider = &CustomProvider{
		ID: f.providerID, Name: "AntSeed", BaseURL: "http://" + namespaceAntSeedHost + "/v1",
		APIKey: "namespace-fixture", ModelIDs: f.models, Enabled: true, Priority: 10,
	}
	installProviders(t, f.provider, &CustomProvider{
		ID: "prov_namespace_sibling_" + string(mode), Name: f.providerID, BaseURL: "http://" + namespaceSiblingHost + "/v1",
		APIKey: "namespace-fixture", ModelIDs: f.models, Enabled: true, Priority: 1,
	})
	f.restoreProvider(t)
	installTransport(t, func(req *http.Request) *http.Response { return f.respond(req, false) })

	zen.TransportMu.Lock()
	previousZenClient := zen.HTTPClient
	zen.TransportMu.Unlock()
	t.Cleanup(func() {
		zen.TransportMu.Lock()
		zen.HTTPClient = previousZenClient
		zen.TransportMu.Unlock()
		zen.ResetFailover()
	})
	f.baseURL = protocolTestServer(t)
	zenCfg := zen.DefaultConfig()
	zenCfg.Enabled = true
	zenCfg.BaseURL = "http://" + namespaceOpenCodeHost + "/inference/v1"
	zenCfg.Key = "namespace-fixture-public"
	zenCfg.Retries = 0
	autoHarvest := false
	zenCfg.SessionAutoHarvest = &autoHarvest
	zenCfg.SessionIDs = []string{"namespace-fixture-session"}
	zen.ConfigMu.Lock()
	zen.Config = zenCfg
	zen.ConfigMu.Unlock()
	zen.ResetFailover()
	zen.TransportMu.Lock()
	zen.HTTPClient = &http.Client{Transport: freeModelRoundTripper(func(req *http.Request) (*http.Response, error) {
		return f.respond(req, true), nil
	})}
	zen.TransportMu.Unlock()
	return f
}

func (f *namespaceIngressFixture) respond(req *http.Request, sse bool) *http.Response {
	model := readReqModel(req)
	f.mu.Lock()
	if model != "" {
		f.calls = append(f.calls, namespaceIngressCall{host: req.URL.Host, model: model})
	}
	status := http.StatusOK
	if req.URL.Host == namespaceAntSeedHost && f.failures[model] != 0 {
		status = f.failures[model]
	} else if model == namespacePrimaryModel && f.primaryUnavailable {
		status = http.StatusServiceUnavailable
	}
	f.mu.Unlock()
	if status != http.StatusOK {
		return stubResp(req, status, `{"error":{"message":"namespace fixture unavailable"}}`)
	}
	label := "Global fallback"
	switch req.URL.Host {
	case namespaceAntSeedHost:
		label = "AntSeed isolated"
	case namespaceOpenCodeHost:
		label = "OpenCode isolated"
	case namespaceSiblingHost:
		label = "Sibling fallback"
	}
	if sse {
		chunk, _ := json.Marshal(map[string]any{"id": "namespace-ok", "object": "chat.completion.chunk", "model": model,
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": label}, "finish_reason": "stop"}}})
		return stubSSE(req, "data: "+string(chunk)+"\n\ndata: [DONE]\n\n")
	}
	body, _ := json.Marshal(map[string]any{"id": "namespace-ok", "object": "chat.completion", "model": model,
		"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": label}, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}})
	return stubResp(req, http.StatusOK, string(body))
}

func (f *namespaceIngressFixture) checkpoint() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *namespaceIngressFixture) callsAfter(checkpoint int) []namespaceIngressCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]namespaceIngressCall(nil), f.calls[checkpoint:]...)
}

func (f *namespaceIngressFixture) fail(model string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[model] = status
}

func (f *namespaceIngressFixture) restoreProvider(t *testing.T) {
	t.Helper()
	providers.Upsert(f.provider)
	for _, model := range f.models {
		if err := providers.SetModelCost(f.providerID, model, "free"); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *namespaceIngressFixture) configure(update func(*admin.ProxyConfig)) {
	cfg := admin.GetProxyConfig().Clone()
	update(cfg)
	admin.SetProxyConfig(cfg)
}

var namespaceIngressPaths = []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"}

func namespaceIngressBody(path, model string) string {
	payload := map[string]any{"model": model, "stream": false}
	if path == "/v1/responses" {
		payload["input"] = "namespace isolation"
	} else {
		payload["messages"] = []any{map[string]any{"role": "user", "content": "namespace isolation"}}
		payload["max_tokens"] = 16
	}
	body, _ := json.Marshal(payload)
	return string(body)
}

func (f *namespaceIngressFixture) expectSuccess(t *testing.T, path, requested, raw, host, label string) []namespaceIngressCall {
	t.Helper()
	checkpoint := f.checkpoint()
	status, body := comboPost(t, f.baseURL+path, namespaceIngressBody(path, requested))
	if status != http.StatusOK || !strings.Contains(body, label) {
		t.Fatalf("%s %q status=%d body=%s, want %s", path, requested, status, body, label)
	}
	calls := f.callsAfter(checkpoint)
	if len(calls) == 0 || calls[len(calls)-1] != (namespaceIngressCall{host: host, model: raw}) {
		t.Fatalf("%s %q dispatched %+v, want provider=%s raw model=%q", path, requested, calls, host, raw)
	}
	return calls
}

func TestProviderModelNamespaceIngresses(t *testing.T) {
	for _, mode := range []config.DispatchMode{config.DispatchModeLegacy, config.DispatchModeShadow, config.DispatchModeEnforce} {
		t.Run(string(mode), func(t *testing.T) {
			f := newNamespaceIngressFixture(t, mode)
			t.Run("same-name-isolation", func(t *testing.T) {
				for _, raw := range f.models[:2] {
					for _, target := range []struct{ prefix, host, label string }{{"as", namespaceAntSeedHost, "AntSeed isolated"}, {"op", namespaceOpenCodeHost, "OpenCode isolated"}} {
						for _, path := range namespaceIngressPaths {
							t.Run(target.prefix+"/"+raw+path, func(t *testing.T) {
								calls := f.expectSuccess(t, path, target.prefix+"/"+raw, raw, target.host, target.label)
								if len(calls) != 1 {
									t.Fatalf("isolated successful request contacted additional upstreams: %+v", calls)
								}
							})
						}
					}
				}
			})
			t.Run("strict-failure", func(t *testing.T) {
				for _, state := range []string{"disabled", "failed"} {
					t.Run(state, func(t *testing.T) {
						f.restoreProvider(t)
						t.Cleanup(func() { f.fail(namespaceStrictModel, 0); f.restoreProvider(t); resetRoutingState() })
						switch state {
						case "disabled":
							p := providers.Find(f.providerID)
							p.Enabled = false
							providers.Upsert(p)
						case "failed":
							f.fail(namespaceStrictModel, http.StatusServiceUnavailable)
						}
						attempted := 0
						for _, path := range namespaceIngressPaths {
							checkpoint := f.checkpoint()
							status, body := comboPost(t, f.baseURL+path, namespaceIngressBody(path, "as/"+namespaceStrictModel))
							if status < http.StatusBadRequest {
								t.Errorf("%s %s pin succeeded through fallback: status=%d body=%s", state, path, status, body)
							}
							for _, call := range f.callsAfter(checkpoint) {
								attempted++
								if state != "failed" || call != (namespaceIngressCall{host: namespaceAntSeedHost, model: namespaceStrictModel}) {
									t.Errorf("%s %s pin crossed to %+v", state, path, call)
								}
							}
						}
						if state == "failed" && attempted == 0 {
							plan, _, err := routing.SnapshotAndPlan(routing.SnapshotOptions{ModelID: "as/" + namespaceStrictModel}, nil)
							t.Fatalf("failure test never reached the pinned provider: candidates=%d exclusions=%+v err=%v", len(plan.Candidates), plan.Excluded, err)
						}
					})
				}
			})
			t.Run("combo-aliases", func(t *testing.T) {
				f.configure(func(cfg *admin.ProxyConfig) {
					cfg.Combos = []ModelCombo{
						{ID: "namespace-single", Name: "AntSeed only", Enabled: true, Entries: []ComboEntry{{Model: "as/" + namespaceComboModel}}},
						{ID: "namespace-ordered", Name: "Explicit providers", Enabled: true, Entries: []ComboEntry{{Model: "as/" + namespaceComboModel}, {Model: "op/" + namespaceComboModel}}},
					}
				})
				for _, path := range namespaceIngressPaths {
					calls := f.expectSuccess(t, path, "namespace-single", namespaceComboModel, namespaceAntSeedHost, "AntSeed isolated")
					if len(calls) != 1 {
						t.Fatalf("single-provider combo contacted additional upstreams: %+v", calls)
					}
				}
				f.fail(namespaceComboModel, http.StatusServiceUnavailable)
				t.Cleanup(func() { f.fail(namespaceComboModel, 0) })
				for _, path := range namespaceIngressPaths {
					calls := f.expectSuccess(t, path, "namespace-ordered", namespaceComboModel, namespaceOpenCodeHost, "OpenCode isolated")
					for _, call := range calls[:len(calls)-1] {
						if call != (namespaceIngressCall{host: namespaceAntSeedHost, model: namespaceComboModel}) {
							t.Fatalf("combo escaped configured provider order: %+v", calls)
						}
					}
				}
			})
			t.Run("configured-fallback-aliases", func(t *testing.T) {
				f.configure(func(cfg *admin.ProxyConfig) {
					cfg.ModelChain = []string{"as/" + namespaceFallbackModel, "op/" + namespaceFallbackModel}
				})
				f.mu.Lock()
				f.primaryUnavailable = true
				f.mu.Unlock()
				f.fail(namespaceFallbackModel, http.StatusServiceUnavailable)
				t.Cleanup(func() { f.fail(namespaceFallbackModel, 0) })
				for _, path := range namespaceIngressPaths {
					calls := f.expectSuccess(t, path, namespacePrimaryModel, namespaceFallbackModel, namespaceOpenCodeHost, "OpenCode isolated")
					for _, call := range calls[:len(calls)-1] {
						if call.model != namespacePrimaryModel && call != (namespaceIngressCall{host: namespaceAntSeedHost, model: namespaceFallbackModel}) {
							t.Fatalf("configured chain escaped aliases: %+v", calls)
						}
					}
				}
			})
			t.Run("provider-scoped-onlyFree", func(t *testing.T) {
				if err := providers.SetModelCost(f.providerID, namespaceFreePolicyModel, "pass"); err != nil {
					t.Fatal(err)
				}
				f.configure(func(cfg *admin.ProxyConfig) {
					cfg.OnlyFree = true
					cfg.Combos = []ModelCombo{{ID: "namespace-free-combo", Name: "Free OpenCode after paid AntSeed", Enabled: true,
						Entries: []ComboEntry{{Model: "as/" + namespaceFreePolicyModel}, {Model: "op/" + namespaceFreePolicyModel}}}}
				})
				for _, path := range namespaceIngressPaths {
					checkpoint := f.checkpoint()
					status, body := comboPost(t, f.baseURL+path, namespaceIngressBody(path, "as/"+namespaceFreePolicyModel))
					if status < http.StatusBadRequest || len(f.callsAfter(checkpoint)) != 0 {
						t.Errorf("paid AntSeed inherited free OpenCode access: path=%s status=%d body=%s calls=%+v", path, status, body, f.callsAfter(checkpoint))
					}
					for _, request := range []string{"op/" + namespaceFreePolicyModel, "namespace-free-combo"} {
						calls := f.expectSuccess(t, path, request, namespaceFreePolicyModel, namespaceOpenCodeHost, "OpenCode isolated")
						if len(calls) != 1 {
							t.Fatalf("onlyFree contacted a paid or fallback offering: %+v", calls)
						}
					}
				}
			})
			t.Run("removed-provider", func(t *testing.T) {
				// Removal is the final scenario: restoring a deleted identity would
				// test a separate provider lifecycle rather than model isolation.
				f.configure(func(cfg *admin.ProxyConfig) { cfg.OnlyFree = false })
				providers.Delete(f.providerID)
				if config.Live().ProviderModelPrefixes["as"] != f.providerID {
					t.Fatal("provider deletion released its namespace reservation")
				}
				for _, path := range namespaceIngressPaths {
					checkpoint := f.checkpoint()
					status, body := comboPost(t, f.baseURL+path, namespaceIngressBody(path, "as/"+namespaceStrictModel))
					if status < http.StatusBadRequest || len(f.callsAfter(checkpoint)) != 0 {
						t.Errorf("removed provider escaped its namespace: path=%s status=%d body=%s calls=%+v", path, status, body, f.callsAfter(checkpoint))
					}
				}
			})
		})
	}
}
