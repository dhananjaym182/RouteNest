package integration

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"routenest/internal/admin"
	"routenest/internal/combos"
	"routenest/internal/zen"
)

func TestAntSeedIdentityPinnedComboDoesNotRouteToOpenCode(t *testing.T) {
	const antseedID = "prov_18e5f4f23187e173"
	for _, modelID := range []string{"deepseek-v4-flash", "identity@deepseek-v4.1-flash"} {
		t.Run(modelID, func(t *testing.T) {
			installPool(t, []Model{{ID: modelID, Source: "zen", Cost: "free", Status: "active"}})
			installConfig(t, admin.DefaultProxyConfig())
			installProviders(t, &CustomProvider{
				ID: antseedID, Name: "AntSeed", BaseURL: "http://antseed.test/v1", APIKey: "fixture",
				ModelIDs: []string{modelID}, Enabled: true, Priority: 10,
			}, &CustomProvider{
				ID: "opencode-instance", Name: "OpenCode", BaseURL: "http://opencode.test/v1", APIKey: "fixture",
				ModelIDs: []string{modelID}, Enabled: true, Priority: 1,
			})
			var hosts, sentModels []string
			installTransport(t, func(req *http.Request) *http.Response {
				hosts = append(hosts, req.URL.Host)
				sentModels = append(sentModels, readReqModel(req))
				return stubResp(req, http.StatusOK, okCompletion)
			})
			combo := ModelCombo{ID: "antseed-combo", Name: "AntSeed only", Enabled: true, Entries: []ComboEntry{
				{Model: modelID, Source: "provider", Provider: antseedID},
			}}
			if err := combos.ValidateCombo(combo, nil); err != nil {
				t.Fatalf("stable AntSeed pin rejected: %v", err)
			}
			run := combos.RunCombo(chatParams(combo.ID), combo, false, "")
			if run.Err != nil {
				t.Fatal(run.Err)
			}
			if !reflect.DeepEqual(hosts, []string{"antseed.test"}) || !reflect.DeepEqual(sentModels, []string{modelID}) {
				t.Fatalf("pinned AntSeed request sent hosts=%v models=%v", hosts, sentModels)
			}
			if run.Upstream != zen.UpstreamProvider || len(run.Trace) != 1 || run.Trace[0].Provider != antseedID || run.Trace[0].ProviderName != "AntSeed" {
				t.Fatalf("pinned request attribution=%+v", run)
			}
			if run.Resp != nil {
				run.Resp.Body.Close()
			}
		})
	}
}

func TestAntSeedIdentityPinFailsWithoutCrossProviderFallback(t *testing.T) {
	const modelID = "deepseek-v4-flash"
	const antseedID = "prov_18e5f4f23187e173"
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			installPool(t, []Model{{ID: modelID, Source: "zen", Cost: "free", Status: "active"}})
			installConfig(t, admin.DefaultProxyConfig())
			installProviders(t, &CustomProvider{
				ID: antseedID, Name: "AntSeed", BaseURL: "http://antseed.test/v1", APIKey: "fixture",
				ModelIDs: []string{modelID}, Enabled: true,
			}, &CustomProvider{
				ID: "opencode-instance", Name: "OpenCode", BaseURL: "http://opencode.test/v1", APIKey: "fixture",
				ModelIDs: []string{modelID}, Enabled: true,
			})
			var hosts []string
			installTransport(t, func(req *http.Request) *http.Response {
				hosts = append(hosts, req.URL.Host)
				if req.URL.Host == "antseed.test" {
					return stubResp(req, status, `{"error":{"message":"fixture provider unavailable"}}`)
				}
				return stubResp(req, http.StatusOK, okCompletion)
			})
			run := combos.RunCombo(chatParams("antseed-only"), ModelCombo{
				ID: "antseed-only", Name: "AntSeed only", Enabled: true,
				Entries: []ComboEntry{{Model: modelID, Source: "provider", Provider: antseedID}},
			}, false, "")
			if run.Err == nil || len(hosts) == 0 {
				t.Fatalf("failed AntSeed pin crossed provider boundary: hosts=%v error=%v", hosts, run.Err)
			}
			for _, host := range hosts {
				if host != "antseed.test" {
					t.Fatalf("failed AntSeed pin crossed provider boundary: hosts=%v", hosts)
				}
			}
			if len(run.Trace) != 1 || run.Trace[0].Provider != antseedID || run.Trace[0].ProviderName != "AntSeed" {
				t.Fatalf("failed pinned request attribution=%+v", run.Trace)
			}
		})
	}
}

func TestAntSeedPaidSharedModelValidationUsesPinnedProvider(t *testing.T) {
	const modelID = "shared-paid-model"
	const antseedID = "prov_18e5f4f23187e173"
	installPool(t, []Model{{ID: modelID, Source: "zen", Cost: "pass", Status: "active"}})
	installConfig(t, admin.DefaultProxyConfig())
	installProviders(t, &CustomProvider{
		ID: antseedID, Name: "AntSeed", BaseURL: "http://antseed.test/v1",
		ModelIDs: []string{modelID}, Enabled: true,
	})
	for _, pin := range []string{antseedID, "AntSeed"} {
		combo := ModelCombo{ID: "pinned-paid", Name: "Pinned AntSeed", Enabled: true, Entries: []ComboEntry{
			{Model: modelID, Source: "provider", Provider: pin},
		}}
		if err := combos.ValidateCombo(combo, nil); err != nil {
			t.Errorf("AntSeed pin %q inherited OpenCode's paid-model rejection: %v", pin, err)
		}
	}
	for _, source := range []string{"", "zen"} {
		combo := ModelCombo{ID: "zen-paid", Name: "OpenCode paid", Enabled: true, Entries: []ComboEntry{{Model: modelID, Source: source}}}
		if err := combos.ValidateCombo(combo, nil); err == nil {
			t.Errorf("source=%q should still reject a paid OpenCode model", source)
		}
	}
}

func TestUnavailableAntSeedIdentityPinDoesNotUseSharedOpenCodeModel(t *testing.T) {
	const modelID = "deepseek-v4-flash"
	const antseedID = "prov_18e5f4f23187e173"
	installPool(t, []Model{{ID: modelID, Source: "zen", Cost: "free", Status: "active"}})
	installConfig(t, admin.DefaultProxyConfig())
	installProviders(t, &CustomProvider{
		ID: antseedID, Name: "AntSeed", BaseURL: "http://antseed.test/v1",
		ModelIDs: []string{modelID}, Enabled: false,
	}, &CustomProvider{
		ID: "other-provider", Name: antseedID, BaseURL: "http://other.test/v1",
		ModelIDs: []string{modelID}, Enabled: true,
	})
	var hosts []string
	installTransport(t, func(req *http.Request) *http.Response {
		hosts = append(hosts, req.URL.Host)
		return stubResp(req, http.StatusOK, okCompletion)
	})
	run := combos.RunCombo(chatParams("antseed-only"), ModelCombo{
		ID: "antseed-only", Name: "AntSeed only", Enabled: true,
		Entries: []ComboEntry{{Model: modelID, Source: "provider", Provider: antseedID}},
	}, false, "")
	if run.Err == nil || len(hosts) != 0 {
		t.Fatalf("unavailable AntSeed pin used another offering: hosts=%v error=%v", hosts, run.Err)
	}
}
