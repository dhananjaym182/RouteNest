package integration

import (
	"fmt"
	"net/http"
	"sync"
	"testing"

	"routenest/internal/admin"
	"routenest/internal/combos"
	"routenest/internal/config"
)

// A custom provider's upstream only knows its own model IDs. Whatever name the
// client used (administrator alias, provider-pinned alias, or the compact
// provider namespace) must be resolved back to the provider's declared model ID
// before the request body is built. These tests record what the upstream
// actually receives, for every ingress and every dispatch mode.

const (
	aliasUpstreamProviderID = "prov_alias_upstream"
	aliasUpstreamModel      = "real-model-v1"
	aliasUpstreamPrefix     = "au"
)

type aliasUpstreamRecorder struct {
	mu     sync.Mutex
	models []string
}

func (r *aliasUpstreamRecorder) add(model string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.models = append(r.models, model)
}

func (r *aliasUpstreamRecorder) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.models
	r.models = nil
	return out
}

func installAliasUpstreamFixture(t *testing.T, mode config.DispatchMode) (string, *aliasUpstreamRecorder) {
	t.Helper()
	installPool(t, []Model{{
		ID: aliasUpstreamModel, Provider: "AliasUp", Source: "provider",
		Cost: "pass", TierSignal: "manual", Status: "active",
	}})
	cfg := admin.DefaultProxyConfig()
	cfg.DispatchMode = mode
	cfg.ProviderModelPrefixes = map[string]string{aliasUpstreamPrefix: aliasUpstreamProviderID, "op": "opencode", "cl": "cline"}
	cfg.ModelAliases = []config.ModelAlias{
		{Alias: "plain-alias", Target: aliasUpstreamModel},
		{Alias: "pinned-alias", Target: aliasUpstreamModel, Provider: aliasUpstreamProviderID},
		{Alias: "ns-alias", Target: aliasUpstreamPrefix + "/" + aliasUpstreamModel},
	}
	installConfig(t, cfg)
	installProviders(t, &CustomProvider{
		ID: aliasUpstreamProviderID, Name: "AliasUp", BaseURL: "http://aliasup.test/v1",
		APIKey: "sk-aliasup", ModelIDs: []string{aliasUpstreamModel}, Enabled: true,
	})
	recorder := &aliasUpstreamRecorder{}
	installTransport(t, func(req *http.Request) *http.Response {
		if isInferenceUpstreamRequest(req) {
			recorder.add(readReqModel(req))
		}
		return stubResp(req, http.StatusOK,
			`{"id":"ok","object":"chat.completion","model":"x","choices":[{"index":0,"message":{"role":"assistant","content":"alias-ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	})
	return protocolTestServer(t), recorder
}

func TestCustomProviderReceivesDeclaredModelIDNotClientName(t *testing.T) {
	clientNames := []string{
		"plain-alias",
		"pinned-alias",
		"ns-alias",
		aliasUpstreamPrefix + "/" + aliasUpstreamModel,
	}
	ingresses := []struct {
		path string
		body func(model string) string
	}{
		{"/v1/chat/completions", func(m string) string {
			return fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, m)
		}},
		{"/v1/messages", func(m string) string {
			return fmt.Sprintf(`{"model":%q,"max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`, m)
		}},
		{"/v1/responses", func(m string) string {
			return fmt.Sprintf(`{"model":%q,"input":"hi"}`, m)
		}},
	}
	for _, mode := range []config.DispatchMode{config.DispatchModeLegacy, config.DispatchModeShadow, config.DispatchModeEnforce} {
		t.Run(string(mode), func(t *testing.T) {
			baseURL, recorder := installAliasUpstreamFixture(t, mode)
			for _, name := range clientNames {
				for _, ingress := range ingresses {
					status, body := comboPost(t, baseURL+ingress.path, ingress.body(name))
					got := recorder.take()
					if status != http.StatusOK {
						t.Errorf("%s model=%q status=%d body=%s (upstream saw %v)", ingress.path, name, status, body, got)
						continue
					}
					if len(got) != 1 || got[0] != aliasUpstreamModel {
						t.Errorf("%s model=%q: upstream received %v, want exactly [%q]", ingress.path, name, got, aliasUpstreamModel)
					}
				}
			}
		})
	}
}

// ---- multiple providers, combos, streaming ----

type aliasUpstreamCall struct{ host, model string }

func installTwoProviderAliasFixture(t *testing.T, mode config.DispatchMode, combos []ModelCombo) (string, func() []aliasUpstreamCall) {
	t.Helper()
	const secondID = "prov_alias_upstream_two"
	installPool(t, []Model{{
		ID: aliasUpstreamModel, Provider: "AliasUp", Source: "provider",
		Cost: "pass", TierSignal: "manual", Status: "active",
	}})
	cfg := admin.DefaultProxyConfig()
	cfg.DispatchMode = mode
	cfg.ProviderModelPrefixes = map[string]string{aliasUpstreamPrefix: aliasUpstreamProviderID, "bt": secondID, "op": "opencode", "cl": "cline"}
	cfg.ModelAliases = []config.ModelAlias{
		{Alias: "first-alias", Target: aliasUpstreamModel, Provider: aliasUpstreamProviderID},
		{Alias: "second-alias", Target: aliasUpstreamModel, Provider: secondID},
		{Alias: "plain-alias", Target: aliasUpstreamModel},
	}
	cfg.Combos = combos
	installConfig(t, cfg)
	installProviders(t,
		&CustomProvider{ID: aliasUpstreamProviderID, Name: "AliasUp", BaseURL: "http://first.aliasup.test/v1",
			APIKey: "sk-1", ModelIDs: []string{aliasUpstreamModel}, Enabled: true, Priority: 10},
		&CustomProvider{ID: secondID, Name: "BetaUp", BaseURL: "http://second.aliasup.test/v1",
			APIKey: "sk-2", ModelIDs: []string{aliasUpstreamModel}, Enabled: true, Priority: 5},
	)
	var mu sync.Mutex
	var calls []aliasUpstreamCall
	installTransport(t, func(req *http.Request) *http.Response {
		if isInferenceUpstreamRequest(req) {
			mu.Lock()
			calls = append(calls, aliasUpstreamCall{req.URL.Host, readReqModel(req)})
			mu.Unlock()
		}
		return stubResp(req, http.StatusOK,
			`{"id":"ok","object":"chat.completion","model":"x","choices":[{"index":0,"message":{"role":"assistant","content":"alias-ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	})
	take := func() []aliasUpstreamCall {
		mu.Lock()
		defer mu.Unlock()
		out := calls
		calls = nil
		return out
	}
	return protocolTestServer(t), take
}

func TestPinnedAliasReachesOnlyItsProviderWithDeclaredModelID(t *testing.T) {
	cases := []struct{ client, host string }{
		{"first-alias", "first.aliasup.test"},
		{"second-alias", "second.aliasup.test"},
		{aliasUpstreamPrefix + "/" + aliasUpstreamModel, "first.aliasup.test"},
		{"bt/" + aliasUpstreamModel, "second.aliasup.test"},
	}
	for _, mode := range []config.DispatchMode{config.DispatchModeLegacy, config.DispatchModeShadow, config.DispatchModeEnforce} {
		t.Run(string(mode), func(t *testing.T) {
			baseURL, take := installTwoProviderAliasFixture(t, mode, nil)
			for _, tc := range cases {
				for _, path := range []string{"/v1/chat/completions", "/v1/messages", "/v1/responses"} {
					body := fmt.Sprintf(`{"model":%q,"max_tokens":64,"messages":[{"role":"user","content":"hi"}],"input":"hi"}`, tc.client)
					status, resp := comboPost(t, baseURL+path, body)
					got := take()
					if status != http.StatusOK {
						t.Errorf("%s model=%q status=%d %s (calls %v)", path, tc.client, status, resp, got)
						continue
					}
					if len(got) != 1 || got[0].host != tc.host || got[0].model != aliasUpstreamModel {
						t.Errorf("%s model=%q: upstream calls %v, want one call to %s with model %q", path, tc.client, got, tc.host, aliasUpstreamModel)
					}
				}
			}
		})
	}
}

func TestComboEntriesSendDeclaredModelIDUpstream(t *testing.T) {
	// Entry shapes the combo save API accepts. Administrator aliases are not
	// among them (see TestComboSaveRejectsAdministratorAliasEntries).
	entries := map[string]ComboEntry{
		"namespaced":   {Model: aliasUpstreamPrefix + "/" + aliasUpstreamModel},
		"explicit-pin": {Model: aliasUpstreamModel, Source: "provider", Provider: aliasUpstreamProviderID},
		"second-pin":   {Model: "bt/" + aliasUpstreamModel},
	}
	wantHost := map[string]string{
		"namespaced": "first.aliasup.test", "explicit-pin": "first.aliasup.test", "second-pin": "second.aliasup.test",
	}
	for _, mode := range []config.DispatchMode{config.DispatchModeLegacy, config.DispatchModeShadow, config.DispatchModeEnforce} {
		for name, entry := range entries {
			t.Run(string(mode)+"/"+name, func(t *testing.T) {
				combo := ModelCombo{ID: "alias-combo", Name: "Alias combo", Enabled: true, Entries: []ComboEntry{entry}}
				baseURL, take := installTwoProviderAliasFixture(t, mode, []ModelCombo{combo})
				for _, path := range []string{"/v1/chat/completions", "/v1/messages", "/v1/responses"} {
					body := `{"model":"alias-combo","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"input":"hi"}`
					status, resp := comboPost(t, baseURL+path, body)
					got := take()
					if status != http.StatusOK {
						t.Errorf("%s status=%d %s (upstream calls %v)", path, status, resp, got)
						continue
					}
					if len(got) != 1 || got[0].model != aliasUpstreamModel || got[0].host != wantHost[name] {
						t.Errorf("%s entry %+v: upstream calls %v, want one call to %s with model %q", path, entry, got, wantHost[name], aliasUpstreamModel)
					}
				}
			})
		}
	}
}

// Saving a combo whose entry is an administrator alias is refused up front, so
// a running combo can never carry an unresolved alias name.
func TestComboSaveRejectsAdministratorAliasEntries(t *testing.T) {
	installTwoProviderAliasFixture(t, config.DispatchModeShadow, nil)
	for _, entry := range []ComboEntry{
		{Model: "plain-alias"},
		{Model: "first-alias"},
		{Model: "second-alias", Source: "provider", Provider: "prov_alias_upstream_two"},
	} {
		err := combos.ValidateCombo(ModelCombo{ID: "alias-combo", Name: "Alias combo", Enabled: true, Entries: []ComboEntry{entry}}, nil)
		if err == nil {
			t.Errorf("combo entry %+v was accepted, want a validation error", entry)
		}
	}
}

func TestStreamingRequestSendsDeclaredModelID(t *testing.T) {
	for _, mode := range []config.DispatchMode{config.DispatchModeLegacy, config.DispatchModeShadow, config.DispatchModeEnforce} {
		t.Run(string(mode), func(t *testing.T) {
			baseURL, take := installTwoProviderAliasFixture(t, mode, nil)
			for _, client := range []string{"first-alias", aliasUpstreamPrefix + "/" + aliasUpstreamModel} {
				status, resp := comboPost(t, baseURL+"/v1/chat/completions",
					fmt.Sprintf(`{"model":%q,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, client))
				got := take()
				if status != http.StatusOK {
					t.Errorf("stream model=%q status=%d %s (calls %v)", client, status, resp, got)
					continue
				}
				if len(got) < 1 || got[0].model != aliasUpstreamModel {
					t.Errorf("stream model=%q: upstream calls %v, want model %q", client, got, aliasUpstreamModel)
				}
			}
		})
	}
}

// ---- ModelMap: a client-visible name backed by a different upstream ID ----

const (
	mappedClientName    = "friendly-name"
	mappedUpstreamModel = "vendor/real-model-2025-01"
)

func installMappedProviderFixture(t *testing.T, mode config.DispatchMode) (string, func() []aliasUpstreamCall) {
	t.Helper()
	installPool(t, []Model{{
		ID: mappedClientName, Provider: "MapUp", Source: "provider",
		Cost: "pass", TierSignal: "manual", Status: "active",
	}})
	cfg := admin.DefaultProxyConfig()
	cfg.DispatchMode = mode
	cfg.ProviderModelPrefixes = map[string]string{"mu": "prov_map_up", "op": "opencode", "cl": "cline"}
	cfg.ModelAliases = []config.ModelAlias{
		{Alias: "map-alias", Target: mappedClientName},
		{Alias: "map-pinned-alias", Target: mappedClientName, Provider: "prov_map_up"},
	}
	installConfig(t, cfg)
	installProviders(t, &CustomProvider{
		ID: "prov_map_up", Name: "MapUp", BaseURL: "http://mapup.test/v1", APIKey: "sk-map",
		ModelIDs: []string{mappedClientName, "unmapped-model"},
		ModelMap: map[string]string{mappedClientName: mappedUpstreamModel},
		Enabled:  true,
	})
	var mu sync.Mutex
	var calls []aliasUpstreamCall
	installTransport(t, func(req *http.Request) *http.Response {
		if isInferenceUpstreamRequest(req) {
			mu.Lock()
			calls = append(calls, aliasUpstreamCall{req.URL.Host, readReqModel(req)})
			mu.Unlock()
		}
		return stubResp(req, http.StatusOK,
			`{"id":"ok","object":"chat.completion","model":"x","choices":[{"index":0,"message":{"role":"assistant","content":"map-ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	})
	take := func() []aliasUpstreamCall {
		mu.Lock()
		defer mu.Unlock()
		out := calls
		calls = nil
		return out
	}
	return protocolTestServer(t), take
}

// The client names a model by its friendly name (directly, through an alias, or
// through the provider namespace); the provider's upstream must receive the
// mapped ID in every dispatch mode and on every ingress.
func TestMappedModelNameSendsMappedUpstreamID(t *testing.T) {
	clientNames := []string{mappedClientName, "map-alias", "map-pinned-alias", "mu/" + mappedClientName}
	for _, mode := range []config.DispatchMode{config.DispatchModeLegacy, config.DispatchModeShadow, config.DispatchModeEnforce} {
		t.Run(string(mode), func(t *testing.T) {
			baseURL, take := installMappedProviderFixture(t, mode)
			for _, name := range clientNames {
				for _, path := range []string{"/v1/chat/completions", "/v1/messages", "/v1/responses"} {
					body := fmt.Sprintf(`{"model":%q,"max_tokens":64,"messages":[{"role":"user","content":"hi"}],"input":"hi"}`, name)
					status, resp := comboPost(t, baseURL+path, body)
					got := take()
					if status != http.StatusOK {
						t.Errorf("%s model=%q status=%d %s (calls %v)", path, name, status, resp, got)
						continue
					}
					if len(got) != 1 || got[0].model != mappedUpstreamModel {
						t.Errorf("%s model=%q: upstream calls %v, want one call with model %q", path, name, got, mappedUpstreamModel)
					}
				}
			}
		})
	}
}

// A model without a mapping entry keeps being sent unchanged.
func TestUnmappedModelOnMappedProviderIsSentUnchanged(t *testing.T) {
	for _, mode := range []config.DispatchMode{config.DispatchModeLegacy, config.DispatchModeShadow, config.DispatchModeEnforce} {
		t.Run(string(mode), func(t *testing.T) {
			baseURL, take := installMappedProviderFixture(t, mode)
			status, resp := comboPost(t, baseURL+"/v1/chat/completions",
				`{"model":"unmapped-model","messages":[{"role":"user","content":"hi"}]}`)
			got := take()
			if status != http.StatusOK || len(got) != 1 || got[0].model != "unmapped-model" {
				t.Fatalf("status=%d %s calls=%v, want one call with model unmapped-model", status, resp, got)
			}
		})
	}
}

// A mapped model inside a combo is also sent with its mapped upstream ID.
func TestMappedModelInComboSendsMappedUpstreamID(t *testing.T) {
	combo := ModelCombo{ID: "map-combo", Name: "Map combo", Enabled: true,
		Entries: []ComboEntry{{Model: "mu/" + mappedClientName}}}
	for _, mode := range []config.DispatchMode{config.DispatchModeLegacy, config.DispatchModeShadow, config.DispatchModeEnforce} {
		t.Run(string(mode), func(t *testing.T) {
			baseURL, take := installMappedProviderFixture(t, mode)
			cfg := admin.SnapshotProxyConfig()
			cfg.Combos = []ModelCombo{combo}
			admin.SetProxyConfig(cfg)
			for _, path := range []string{"/v1/chat/completions", "/v1/messages", "/v1/responses"} {
				status, resp := comboPost(t, baseURL+path,
					`{"model":"map-combo","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"input":"hi"}`)
				got := take()
				if status != http.StatusOK || len(got) != 1 || got[0].model != mappedUpstreamModel {
					t.Errorf("%s status=%d %s calls=%v, want one call with model %q", path, status, resp, got, mappedUpstreamModel)
				}
			}
		})
	}
}
