package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestRuntimeStatePolicySecureDefaults(t *testing.T) {
	want := DefaultRuntimeStatePolicy()
	if want.OutagePolicy != RuntimeStateFailClosed || want.AcknowledgeFailOpen ||
		want.DeploymentTopology != RuntimeStateSingleNode || want.SnapshotMaxAgeSeconds != 30 {
		t.Fatalf("unsafe defaults: %+v", want)
	}
	if got := DefaultProxy().RuntimeState; got != want {
		t.Fatalf("proxy defaults = %+v, want %+v", got, want)
	}
	for _, policy := range []RuntimeStatePolicy{
		{},
		{OutagePolicy: "unknown", DeploymentTopology: "unknown", SnapshotMaxAgeSeconds: -1},
		{OutagePolicy: RuntimeStateFailOpen},
	} {
		if got := policy.EffectiveOutagePolicy(); got != RuntimeStateFailClosed {
			t.Fatalf("unnormalized policy permits fail open: %+v", policy)
		}
		policy.Normalize()
		if policy != want {
			t.Fatalf("normalized policy = %+v, want %+v", policy, want)
		}
		if err := policy.Validate(); err != nil {
			t.Fatalf("default policy rejected: %v", err)
		}
	}
}

func TestRuntimeStatePolicyAcknowledgedFailOpen(t *testing.T) {
	policy := RuntimeStatePolicy{
		OutagePolicy: RuntimeStateFailOpen, AcknowledgeFailOpen: true,
		DeploymentTopology: RuntimeStateMultiReplica, SnapshotMaxAgeSeconds: 45,
	}
	policy.Normalize()
	if policy.EffectiveOutagePolicy() != RuntimeStateFailOpen || policy.DeploymentTopology != RuntimeStateMultiReplica {
		t.Fatalf("explicit policy lost: %+v", policy)
	}
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	if policy.SnapshotMaxAge() != 45*time.Second {
		t.Fatalf("max age = %v", policy.SnapshotMaxAge())
	}
}

func TestRuntimeStatePolicyValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*RuntimeStatePolicy)
	}{
		{"empty outage policy", func(p *RuntimeStatePolicy) { p.OutagePolicy = "" }},
		{"unknown outage policy", func(p *RuntimeStatePolicy) { p.OutagePolicy = "future" }},
		{"unacknowledged fail open", func(p *RuntimeStatePolicy) { p.OutagePolicy = RuntimeStateFailOpen }},
		{"empty topology", func(p *RuntimeStatePolicy) { p.DeploymentTopology = "" }},
		{"unknown topology", func(p *RuntimeStatePolicy) { p.DeploymentTopology = "cluster" }},
		{"zero max age", func(p *RuntimeStatePolicy) { p.SnapshotMaxAgeSeconds = 0 }},
		{"negative max age", func(p *RuntimeStatePolicy) { p.SnapshotMaxAgeSeconds = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := DefaultRuntimeStatePolicy()
			tc.edit(&p)
			if p.Validate() == nil {
				t.Fatalf("accepted invalid policy: %+v", p)
			}
		})
	}
}

func TestLoadProxyRuntimeStateNormalizesUnsafeFiles(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"legacy file", `{"strategy":"fill"}`},
		{"missing policy", `{"runtimeState":{}}`},
		{"unknown values", `{"runtimeState":{"outagePolicy":"unknown","deploymentTopology":"unknown","snapshotMaxAgeSeconds":-1}}`},
		{"unacknowledged fail open", `{"runtimeState":{"outagePolicy":"fail_open"}}`},
		{"explicit false acknowledgement", `{"runtimeState":{"outagePolicy":"fail_open","acknowledgeFailOpen":false}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadProxy(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.RuntimeState != DefaultRuntimeStatePolicy() {
				t.Fatalf("unsafe loaded policy: %+v", cfg.RuntimeState)
			}
		})
	}
}

func TestRuntimeStateConfigRoundTripAndClone(t *testing.T) {
	cfg := DefaultProxy()
	cfg.RuntimeState = RuntimeStatePolicy{
		OutagePolicy: RuntimeStateFailOpen, AcknowledgeFailOpen: true,
		DeploymentTopology: RuntimeStateMultiReplica, SnapshotMaxAgeSeconds: 90,
	}
	clone := cfg.Clone()
	if !reflect.DeepEqual(clone.RuntimeState, cfg.RuntimeState) {
		t.Fatalf("clone lost policy: %+v", clone.RuntimeState)
	}
	clone.RuntimeState.AcknowledgeFailOpen = false
	clone.RuntimeState.DeploymentTopology = RuntimeStateSingleNode
	if !cfg.RuntimeState.AcknowledgeFailOpen || cfg.RuntimeState.DeploymentTopology != RuntimeStateMultiReplica {
		t.Fatal("clone mutation changed source policy")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := SaveProxy(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadProxy(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RuntimeState != cfg.RuntimeState {
		t.Fatalf("round trip = %+v, want %+v", loaded.RuntimeState, cfg.RuntimeState)
	}
}

func TestPublishRuntimeStateNormalizesWithoutMutatingCaller(t *testing.T) {
	t.Setenv("ROUTENEST_DATA_DIR", t.TempDir())
	resetLive(t)
	unsafe := DefaultProxy()
	unsafe.RuntimeState.OutagePolicy = RuntimeStateFailOpen
	Publish(unsafe)
	if Live().RuntimeState.OutagePolicy != RuntimeStateFailClosed {
		t.Fatal("unacknowledged fail open was published")
	}
	if unsafe.RuntimeState.OutagePolicy != RuntimeStateFailOpen {
		t.Fatal("normalization mutated the caller config")
	}
}
