package adminapi

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	configpkg "routenest/internal/config"
)

func TestConfigGetRuntimeStateSecureDefaults(t *testing.T) {
	h, f := newConfigHandler()
	f.cfg.RuntimeState.OutagePolicy = configpkg.RuntimeStateFailOpen
	rec := httptest.NewRecorder()
	h.Get(rec, httptest.NewRequest(http.MethodGet, "/admin/api/config", nil))
	data := decodeEnvelope(t, rec).Data.(map[string]any)
	got := data["runtimeState"].(map[string]any)
	want := map[string]any{
		"outagePolicy": "fail_closed", "acknowledgeFailOpen": false,
		"deploymentTopology": "single_node", "snapshotMaxAgeSeconds": float64(30),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runtimeState = %#v, want %#v", got, want)
	}
	if f.cfg.RuntimeState.OutagePolicy != configpkg.RuntimeStateFailOpen {
		t.Fatal("GET mutated config while reporting effective policy")
	}
}

func TestConfigUpdateRuntimeStateAcknowledgedFailOpen(t *testing.T) {
	h, f := newConfigHandler()
	rec := httptest.NewRecorder()
	h.Update(rec, postJSON("/admin/api/config", `{"runtimeState":{"outagePolicy":"fail_open","acknowledgeFailOpen":true,"deploymentTopology":"multi_replica","snapshotMaxAgeSeconds":60}}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	want := configpkg.RuntimeStatePolicy{
		OutagePolicy: configpkg.RuntimeStateFailOpen, AcknowledgeFailOpen: true,
		DeploymentTopology: configpkg.RuntimeStateMultiReplica, SnapshotMaxAgeSeconds: 60,
	}
	if f.cfg.RuntimeState != want {
		t.Fatalf("runtimeState = %+v, want %+v", f.cfg.RuntimeState, want)
	}
	data := decodeEnvelope(t, rec).Data.(map[string]any)
	echo := data["runtimeState"].(map[string]any)
	if echo["outagePolicy"] != "fail_open" || echo["acknowledgeFailOpen"] != true ||
		echo["deploymentTopology"] != "multi_replica" || echo["snapshotMaxAgeSeconds"] != float64(60) {
		t.Fatalf("runtimeState echo = %#v", echo)
	}
}

func TestConfigUpdateRuntimeStatePartialPatch(t *testing.T) {
	h, f := newConfigHandler()
	f.cfg.RuntimeState = configpkg.RuntimeStatePolicy{
		OutagePolicy: configpkg.RuntimeStateFailOpen, AcknowledgeFailOpen: true,
		DeploymentTopology: configpkg.RuntimeStateMultiReplica, SnapshotMaxAgeSeconds: 60,
	}
	rec := httptest.NewRecorder()
	h.Update(rec, postJSON("/admin/api/config", `{"runtimeState":{"snapshotMaxAgeSeconds":90}}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if f.cfg.RuntimeState.OutagePolicy != configpkg.RuntimeStateFailOpen || !f.cfg.RuntimeState.AcknowledgeFailOpen ||
		f.cfg.RuntimeState.DeploymentTopology != configpkg.RuntimeStateMultiReplica || f.cfg.RuntimeState.SnapshotMaxAgeSeconds != 90 {
		t.Fatalf("partial patch lost policy: %+v", f.cfg.RuntimeState)
	}
	// Switching to closed and clearing acknowledgement in one patch is safe.
	rec = httptest.NewRecorder()
	h.Update(rec, postJSON("/admin/api/config", `{"runtimeState":{"outagePolicy":"fail_closed","acknowledgeFailOpen":false}}`))
	if rec.Code != http.StatusOK || f.cfg.RuntimeState.OutagePolicy != configpkg.RuntimeStateFailClosed || f.cfg.RuntimeState.AcknowledgeFailOpen {
		t.Fatalf("safe switch rejected: %d %s %+v", rec.Code, rec.Body.String(), f.cfg.RuntimeState)
	}
}

func TestConfigUpdateRejectsInvalidRuntimeStateWithoutMutation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		patch string
	}{
		{"unknown outage policy", `{"outagePolicy":"unknown"}`},
		{"empty outage policy", `{"outagePolicy":""}`},
		{"missing acknowledgement", `{"outagePolicy":"fail_open"}`},
		{"false acknowledgement", `{"outagePolicy":"fail_open","acknowledgeFailOpen":false}`},
		{"unknown topology", `{"deploymentTopology":"cluster"}`},
		{"empty topology", `{"deploymentTopology":""}`},
		{"zero snapshot age", `{"snapshotMaxAgeSeconds":0}`},
		{"negative snapshot age", `{"snapshotMaxAgeSeconds":-1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, f := newConfigHandler()
			before := f.cfg.Clone()
			rec := httptest.NewRecorder()
			h.Update(rec, postJSON("/admin/api/config", `{"strategy":"fill","runtimeState":`+tc.patch+`}`))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			if !reflect.DeepEqual(f.cfg, before) {
				t.Fatalf("rejected patch mutated config: %+v", f.cfg)
			}
		})
	}
}

func TestConfigUpdateCannotRevokeActiveFailOpenAcknowledgement(t *testing.T) {
	h, f := newConfigHandler()
	f.cfg.RuntimeState = configpkg.DefaultRuntimeStatePolicy()
	f.cfg.RuntimeState.OutagePolicy = configpkg.RuntimeStateFailOpen
	f.cfg.RuntimeState.AcknowledgeFailOpen = true
	rec := httptest.NewRecorder()
	h.Update(rec, postJSON("/admin/api/config", `{"runtimeState":{"acknowledgeFailOpen":false}}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if !f.cfg.RuntimeState.AcknowledgeFailOpen {
		t.Fatal("unsafe partial patch changed acknowledgement")
	}
}

func TestConfigRejectedPatchDoesNotPublishRuntimeState(t *testing.T) {
	h, f := newConfigHandler()
	before := f.cfg.Clone()
	rec := httptest.NewRecorder()
	h.Update(rec, postJSON("/admin/api/config", `{"strategy":"invalid","runtimeState":{"outagePolicy":"fail_open","acknowledgeFailOpen":true}}`))
	if rec.Code != http.StatusBadRequest || !reflect.DeepEqual(f.cfg, before) {
		t.Fatalf("rejected patch published fail open: %d %+v", rec.Code, f.cfg.RuntimeState)
	}
}
