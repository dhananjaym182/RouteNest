package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"routenest/internal/admin"
	"routenest/internal/config"
	"routenest/internal/platform"
	"routenest/internal/providers"
	"routenest/internal/zen"
)

const namespaceProbeRawModel = "org/shared-model@revision"

type namespaceProbeCall struct {
	provider string
	model    string
}

type namespaceProbeFixture struct {
	providerID string
	retiredID  string
	provider   *CustomProvider
	sibling    *CustomProvider
	mu         sync.Mutex
	calls      []namespaceProbeCall
	status     int
}

func newNamespaceProbeFixture(t *testing.T) *namespaceProbeFixture {
	t.Helper()
	t.Setenv(platform.DataDirEnv, t.TempDir())
	previous := config.Snapshot()
	// Serial disposable fixtures may initialize private test state; public
	// publication deliberately cannot release a real namespace reservation.
	config.Mu.Lock()
	*config.Held() = *config.DefaultProxy()
	config.Mu.Unlock()
	t.Cleanup(func() {
		config.Mu.Lock()
		*config.Held() = *previous.Clone()
		config.Mu.Unlock()
	})
	fixtureName := strings.ReplaceAll(t.Name(), "/", "_")
	f := &namespaceProbeFixture{providerID: "prov_probe_" + fixtureName, retiredID: "retired_probe_" + fixtureName}
	antseed := httptest.NewServer(f.upstream("antseed"))
	sibling := httptest.NewServer(f.upstream("sibling"))
	cline := httptest.NewServer(f.upstream("cline"))
	opencode := httptest.NewServer(f.upstream("opencode"))
	t.Cleanup(antseed.Close)
	t.Cleanup(sibling.Close)
	t.Cleanup(cline.Close)
	t.Cleanup(opencode.Close)

	installPool(t, []Model{
		{ID: namespaceProbeRawModel, Source: "zen", Provider: "OpenCode", Cost: "free", TierSignal: "manual", Status: "active"},
		{ID: namespaceProbeRawModel, Source: "remote", Provider: "Cline", Cost: "free", TierSignal: "manual", Status: "active"},
	})
	cfg := admin.DefaultProxyConfig()
	cfg.ProviderModelPrefixes = map[string]string{"as": f.providerID, "op": "opencode", "cl": "cline", "retired_probe": f.retiredID}
	installConfig(t, cfg)
	f.provider = &CustomProvider{ID: f.providerID, Name: "AntSeed", BaseURL: antseed.URL + "/v1", Enabled: true, ModelIDs: []string{namespaceProbeRawModel}, Priority: 10}
	f.sibling = &CustomProvider{ID: "sibling_" + fixtureName, Name: f.providerID, BaseURL: sibling.URL + "/v1", Enabled: true, ModelIDs: []string{namespaceProbeRawModel}, Priority: 1}
	installProviders(t, f.provider, f.sibling)

	// Custom providers use their local URLs directly. Every other egress request
	// is redirected to the local Cline fixture, so no probe can reach live APIs.
	clineURL, err := url.Parse(cline.URL)
	if err != nil {
		t.Fatal(err)
	}
	localTransport := &http.Transport{}
	t.Cleanup(localTransport.CloseIdleConnections)
	installTransport(t, func(req *http.Request) *http.Response {
		request := req.Clone(req.Context())
		upstreamURL := *req.URL
		if req.URL.Host != strings.TrimPrefix(antseed.URL, "http://") && req.URL.Host != strings.TrimPrefix(sibling.URL, "http://") {
			upstreamURL.Scheme, upstreamURL.Host = clineURL.Scheme, clineURL.Host
		}
		request.URL = &upstreamURL
		response, roundTripErr := localTransport.RoundTrip(request)
		if roundTripErr != nil {
			return stubResp(req, http.StatusBadGateway, `{"error":"local probe fixture failed"}`)
		}
		return response
	})
	zen.ConfigMu.Lock()
	previousZenConfig := zen.Config
	zenCfg := zen.DefaultConfig()
	zenCfg.Enabled, zenCfg.BaseURL, zenCfg.Key = true, opencode.URL+"/inference/v1", "fixture-public"
	zenCfg.Retries = 0
	noHarvest := false
	zenCfg.SessionAutoHarvest = &noHarvest
	zenCfg.SessionIDs = []string{"namespace-probe-fixture-session"}
	zen.Config = zenCfg
	zen.ConfigMu.Unlock()
	zen.TransportMu.Lock()
	previousZenClient := zen.HTTPClient
	zen.HTTPClient = opencode.Client()
	zen.TransportMu.Unlock()
	zen.ResetFailover()
	t.Cleanup(func() {
		zen.ConfigMu.Lock()
		zen.Config = previousZenConfig
		zen.ConfigMu.Unlock()
		zen.TransportMu.Lock()
		zen.HTTPClient = previousZenClient
		zen.TransportMu.Unlock()
		zen.ResetFailover()
	})
	return f
}

func (f *namespaceProbeFixture) upstream(provider string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		model := readReqModel(req)
		f.mu.Lock()
		f.calls = append(f.calls, namespaceProbeCall{provider: provider, model: model})
		status := f.status
		f.mu.Unlock()
		if provider == "antseed" && status != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"fixture upstream unavailable"}}`))
			return
		}
		if provider == "opencode" {
			w.Header().Set("Content-Type", "text/event-stream")
			chunk, _ := json.Marshal(map[string]any{"id": "probe-ok", "model": model, "choices": []any{
				map[string]any{"index": 0, "delta": map[string]any{"content": "ok"}, "finish_reason": "stop"},
			}})
			_, _ = w.Write([]byte("data: " + string(chunk) + "\n\ndata: [DONE]\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(okCompletion))
	}
}

func (f *namespaceProbeFixture) checkpoint() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *namespaceProbeFixture) callsAfter(checkpoint int) []namespaceProbeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]namespaceProbeCall(nil), f.calls[checkpoint:]...)
}

func (f *namespaceProbeFixture) setCombo(entries ...ComboEntry) {
	cfg := admin.GetProxyConfig().Clone()
	cfg.Combos = []ModelCombo{{ID: "namespace-probe", Name: "Namespace probe", Enabled: true, Entries: entries}}
	admin.SetProxyConfig(cfg)
}

func (f *namespaceProbeFixture) comboResult(t *testing.T, filter string) map[string]any {
	t.Helper()
	payload := map[string]string{"id": "namespace-probe"}
	if filter != "" {
		payload["model"] = filter
	}
	body, _ := json.Marshal(payload)
	code, response := postAdminJSON(t, admin.HandleComboTest, string(body))
	if code != http.StatusOK || !response.Success {
		t.Fatalf("combo probe status=%d error=%s", code, response.Error)
	}
	results, _ := dataMap(t, response)["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("combo probe results=%v, want one selected offering", results)
	}
	result, _ := results[0].(map[string]any)
	return result
}

func (f *namespaceProbeFixture) requireCall(t *testing.T, checkpoint int, provider string) {
	t.Helper()
	calls := f.callsAfter(checkpoint)
	if len(calls) != 1 || calls[0] != (namespaceProbeCall{provider: provider, model: namespaceProbeRawModel}) {
		t.Fatalf("probe calls=%+v, want only %s with unchanged raw ID %q", calls, provider, namespaceProbeRawModel)
	}
}

func TestAdminNamespaceProbeProviderIdentityLookup(t *testing.T) {
	f := newNamespaceProbeFixture(t)
	for _, pin := range []string{f.providerID, "  " + f.providerID + "  ", " antseed "} {
		provider := admin.FindEnabledProviderByName(pin)
		if provider == nil || provider.ID != f.providerID {
			t.Errorf("pin %q resolved %+v, want AntSeed ID %s", pin, provider, f.providerID)
		}
	}
	f.setCombo(ComboEntry{Model: namespaceProbeRawModel, Source: "provider", Provider: f.providerID})
	checkpoint := f.checkpoint()
	result := f.comboResult(t, "")
	if result["ok"] != true || result["providerId"] != f.providerID || result["providerName"] != "AntSeed" {
		t.Fatalf("ID-pinned combo probe attributed wrong provider: %+v", result)
	}
	f.requireCall(t, checkpoint, "antseed")
}

func TestAdminNamespaceProbeAliasesPreserveRawModel(t *testing.T) {
	f := newNamespaceProbeFixture(t)
	for _, target := range []struct{ prefix, upstream string }{{"as", "provider"}, {"op", "opencode"}, {"cl", "cline"}} {
		t.Run(target.prefix, func(t *testing.T) {
			model := target.prefix + "/" + namespaceProbeRawModel
			provider := target.upstream
			if provider == "provider" {
				provider = "antseed"
			}
			checkpoint := f.checkpoint()
			result := admin.QuickModelTest(model, nil)
			if result["ok"] != true || result["model"] != model || result["upstream"] != target.upstream {
				t.Fatalf("qualified quick probe result=%+v", result)
			}
			f.requireCall(t, checkpoint, provider)
			f.setCombo(ComboEntry{Model: model})
			checkpoint = f.checkpoint()
			result = f.comboResult(t, "")
			if result["ok"] != true || result["model"] != model || result["upstream"] != target.upstream {
				t.Fatalf("qualified combo probe result=%+v", result)
			}
			f.requireCall(t, checkpoint, provider)
		})
	}
}

func TestAdminNamespaceProbeUnavailableIdentitiesNeverFallback(t *testing.T) {
	for _, state := range []string{"disabled", "deleted", "reserved", "generated"} {
		t.Run(state, func(t *testing.T) {
			f := newNamespaceProbeFixture(t)
			pin, reference := f.providerID, "as/"+namespaceProbeRawModel
			switch state {
			case "disabled":
				provider := providers.Find(f.providerID)
				provider.Enabled = false
				providers.Upsert(provider)
			case "deleted":
				providers.Delete(f.providerID)
			case "reserved":
				pin, reference = f.retiredID, "retired_probe/"+namespaceProbeRawModel
			case "generated":
				pin = "prov_1234567890abcdef"
				reference = namespaceProbeRawModel + "@" + pin
			}
			peer := *f.sibling
			peer.Name = pin
			providers.Upsert(&peer)
			if provider := admin.FindEnabledProviderByName(pin); provider != nil {
				t.Errorf("unavailable %s pin became a display-name lookup: %+v", state, provider)
			}
			f.setCombo(ComboEntry{Model: namespaceProbeRawModel, Source: "provider", Provider: pin})
			checkpoint := f.checkpoint()
			result := f.comboResult(t, "")
			if result["ok"] != false || len(f.callsAfter(checkpoint)) != 0 {
				t.Errorf("unavailable %s combo pin used a fallback: result=%+v calls=%+v", state, result, f.callsAfter(checkpoint))
			}
			checkpoint = f.checkpoint()
			result = admin.QuickModelTest(reference, nil)
			if result["ok"] != false || len(f.callsAfter(checkpoint)) != 0 {
				t.Errorf("unavailable %s qualified pin used a fallback: result=%+v calls=%+v", state, result, f.callsAfter(checkpoint))
			}
		})
	}
}

func TestAdminNamespaceProbeFailureAndPinConflictsStayStrict(t *testing.T) {
	f := newNamespaceProbeFixture(t)
	model := "as/" + namespaceProbeRawModel
	f.mu.Lock()
	f.status = http.StatusServiceUnavailable
	f.mu.Unlock()
	checkpoint := f.checkpoint()
	result := admin.QuickModelTest(model, nil)
	if result["ok"] != false {
		t.Fatalf("failed strict quick probe succeeded: %+v", result)
	}
	f.requireCall(t, checkpoint, "antseed")
	f.setCombo(ComboEntry{Model: model})
	checkpoint = f.checkpoint()
	result = f.comboResult(t, "")
	if result["ok"] != false {
		t.Fatalf("failed strict combo probe succeeded: %+v", result)
	}
	f.requireCall(t, checkpoint, "antseed")
	f.mu.Lock()
	f.status = 0
	f.mu.Unlock()
	for _, conflict := range []struct {
		source string
		forced *CustomProvider
	}{{"zen", nil}, {"cline", nil}, {"provider", f.sibling}} {
		checkpoint = f.checkpoint()
		result = admin.RunQuickModelTest(model, conflict.forced, conflict.source, 0)
		if result["ok"] != false || len(f.callsAfter(checkpoint)) != 0 {
			t.Errorf("conflicting alias/source/provider reached an upstream: source=%s result=%+v calls=%+v", conflict.source, result, f.callsAfter(checkpoint))
		}
	}
	f.setCombo(ComboEntry{Model: model, Source: "zen"})
	checkpoint = f.checkpoint()
	result = f.comboResult(t, "")
	if result["ok"] != false || len(f.callsAfter(checkpoint)) != 0 {
		t.Errorf("conflicting combo alias reached an upstream: result=%+v calls=%+v", result, f.callsAfter(checkpoint))
	}
}

func TestAdminNamespaceProbeBypassesSelectionCooldown(t *testing.T) {
	f := newNamespaceProbeFixture(t)
	f.setCombo(ComboEntry{Model: namespaceProbeRawModel, Source: "provider", Provider: f.providerID})
	for _, probe := range []string{"quick", "combo"} {
		providers.SetCooldown(f.providerID, namespaceProbeRawModel, time.Now().Add(time.Hour))
		if len(providers.ResolveNamedForModel(namespaceProbeRawModel, f.providerID)) != 0 {
			t.Fatal("fixture failed to activate provider-selection cooldown")
		}
		checkpoint := f.checkpoint()
		var result map[string]any
		if probe == "quick" {
			result = admin.QuickModelTest("as/"+namespaceProbeRawModel, nil)
		} else {
			result = f.comboResult(t, "")
		}
		if result["ok"] != true {
			t.Fatalf("%s probe incorrectly honored provider-selection cooldown: %+v", probe, result)
		}
		f.requireCall(t, checkpoint, "antseed")
	}
}

func TestAdminNamespaceProbeDisabledOpenCodeDoesNotDispatch(t *testing.T) {
	f := newNamespaceProbeFixture(t)
	zen.ConfigMu.Lock()
	zen.Config.Enabled = false
	zen.ConfigMu.Unlock()
	checkpoint := f.checkpoint()
	result := admin.QuickModelTest("op/"+namespaceProbeRawModel, nil)
	if result["ok"] != false || len(f.callsAfter(checkpoint)) != 0 {
		t.Errorf("disabled OpenCode namespace probe reached an upstream: result=%+v calls=%+v", result, f.callsAfter(checkpoint))
	}
	f.setCombo(ComboEntry{Model: namespaceProbeRawModel, Source: "zen"})
	checkpoint = f.checkpoint()
	result = f.comboResult(t, "")
	if result["ok"] != false || len(f.callsAfter(checkpoint)) != 0 {
		t.Errorf("disabled OpenCode combo probe reached an upstream: result=%+v calls=%+v", result, f.callsAfter(checkpoint))
	}
}

func TestAdminNamespaceComboProbeQualifiedFilterSelectsOneOffering(t *testing.T) {
	f := newNamespaceProbeFixture(t)
	f.setCombo(
		ComboEntry{Model: namespaceProbeRawModel, Source: "provider", Provider: f.sibling.ID},
		ComboEntry{Model: namespaceProbeRawModel, Source: "provider", Provider: f.providerID},
		ComboEntry{Model: namespaceProbeRawModel, Source: "zen"},
		ComboEntry{Model: namespaceProbeRawModel, Source: "cline"},
	)
	for _, target := range []struct{ prefix, provider string }{{"as", "antseed"}, {"op", "opencode"}, {"cl", "cline"}} {
		checkpoint := f.checkpoint()
		result := f.comboResult(t, target.prefix+"/"+namespaceProbeRawModel)
		if result["ok"] != true || result["model"] != namespaceProbeRawModel {
			t.Fatalf("qualified combo filter result=%+v", result)
		}
		f.requireCall(t, checkpoint, target.provider)
	}
	f.setCombo(
		ComboEntry{Model: namespaceProbeRawModel, Source: "provider", Provider: f.sibling.ID},
		ComboEntry{Model: namespaceProbeRawModel, Source: "provider", Provider: "AntSeed"},
		ComboEntry{Model: namespaceProbeRawModel, Source: "zen"},
	)
	checkpoint := f.checkpoint()
	result := f.comboResult(t, "as/"+namespaceProbeRawModel)
	if result["ok"] != true || result["model"] != namespaceProbeRawModel || result["provider"] != "AntSeed" || result["providerId"] != f.providerID {
		t.Fatalf("stable-ID filter changed the legacy entry's UI association: %+v", result)
	}
	f.requireCall(t, checkpoint, "antseed")
}
