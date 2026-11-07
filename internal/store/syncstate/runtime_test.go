package syncstate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"routenest/internal/domain"
)

func runtimeFixture() State {
	at := time.Date(2026, time.November, 7, 7, 10, 41, 0, time.UTC)
	allowed := true
	levels := []string{"low", "high"}
	scope := domain.AvailabilityScope{
		ProviderID: "p1", CredentialID: "credential-1", ModelID: "model/1", EgressID: "egress-1",
	}
	observation := domain.AvailabilityObservation{
		Kind: domain.ObservationQuotaExhausted, Scope: scope, Until: at.Add(time.Hour),
		ResetAt: at.Add(24 * time.Hour), Source: "upstream", Confidence: domain.ConfidenceExplicit,
	}
	return State{
		LastCheckedMs: at.UnixMilli(), BatchID: "batch-1", DelistedModels: []string{"old/model"},
		Offerings: map[string]domain.ModelOfferingState{"model/1": {ThinkingLevels: &levels}},
		Runtime: &RuntimeState{
			SchemaVersion: RuntimeSchemaVersion, ObservationRevision: 8,
			Budget: &BudgetState{
				RPM:    map[string][]time.Time{"model/1": {at.Add(-time.Second), at}},
				Daily:  map[string]int64{"2026-11-07": 2},
				Tokens: map[string]int64{"2026-11-07": 37, "2026-11": 41},
				Admissions: map[string]AdmissionReceipt{
					"attempt-1": {
						ProviderID: "p1", ModelID: "model/1", AttemptID: "attempt-1", At: at,
						Day: "2026-11-07", Month: "2026-11", TokenWindow: "monthly", Allowed: &allowed,
						Limits: BudgetLimits{RPM: 5, Daily: 10, Tokens: 100, TokenWindow: "monthly"},
						Reason: "admitted", ResetAt: at.Add(time.Minute), Fingerprint: "admission-payload",
					},
				},
				TokenEvents: map[string]TokenEventReceipt{
					"event-1": {ProviderID: "p1", AttemptID: "attempt-1", EventID: "event-1", Tokens: 37, At: at, Fingerprint: "usage-payload"},
				},
			},
			Observations: []VersionedObservation{{Observation: observation, Revision: 7}},
			Tombstones:   []ObservationTombstone{{Scope: domain.AvailabilityScope{ProviderID: "p1"}, Revision: 8, At: at}},
			ObservationEvents: map[string]ObservationEventReceipt{
				"observation-1": {EventID: "observation-1", Observation: observation, Scope: scope, Kind: observation.Kind, Revision: 7, At: at, Fingerprint: "observation-payload"},
			},
		},
	}
}

func TestRuntimeSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	want := runtimeFixture()
	if err := Save(path, map[string]State{"p1": want, "legacy": {LastCheckedMs: 12}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded["p1"] == nil || !reflect.DeepEqual(*loaded["p1"], want) {
		t.Fatalf("runtime round trip lost fields: got %#v, want %#v", loaded["p1"], want)
	}
	if loaded["legacy"] == nil || loaded["legacy"].Runtime != nil {
		t.Fatalf("legacy state gained runtime: %#v", loaded["legacy"])
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var encoded map[string]map[string]json.RawMessage
	if err := json.Unmarshal(raw, &encoded); err != nil {
		t.Fatal(err)
	}
	if len(encoded) != 2 || encoded["p1"]["runtime"] == nil {
		t.Fatalf("provider-keyed shape changed: %s", raw)
	}
	if _, exists := encoded["legacy"]["runtime"]; exists {
		t.Fatalf("legacy runtime must be omitted: %s", raw)
	}
}

func TestLoadLegacyJSONWithoutRuntime(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	legacy := `{"p1":{"lastCheckedMs":42,"lastError":"timeout","delistedModels":[],"offerings":{"model/1":{"thinkingLevels":[]}}},"unknown":null}`
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load legacy: %v", err)
	}
	state := loaded["p1"]
	if state == nil || state.LastCheckedMs != 42 || state.LastError != "timeout" || state.Runtime != nil {
		t.Fatalf("legacy fields changed: %#v", state)
	}
	if state.DelistedModels == nil || state.Offerings["model/1"].ThinkingLevels == nil || loaded["unknown"] != nil {
		t.Fatalf("legacy collection/null semantics changed: %#v", loaded)
	}
}

func TestRuntimeAuthoritativeEmptyCollections(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	want := State{Runtime: &RuntimeState{
		SchemaVersion: RuntimeSchemaVersion,
		Budget: &BudgetState{
			RPM:   map[string][]time.Time{"empty": {}, "unknown": nil},
			Daily: map[string]int64{}, Tokens: map[string]int64{},
			Admissions: map[string]AdmissionReceipt{}, TokenEvents: map[string]TokenEventReceipt{},
		},
		Observations: []VersionedObservation{}, Tombstones: []ObservationTombstone{},
		ObservationEvents: map[string]ObservationEventReceipt{},
	}}
	if err := Save(path, map[string]State{"p1": want, "nil": {Runtime: &RuntimeState{Budget: &BudgetState{}}}}); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*loaded["p1"], want) {
		t.Fatalf("empty collections lost authority: %#v", loaded["p1"].Runtime)
	}
	nilRuntime := loaded["nil"].Runtime
	if nilRuntime.Observations != nil || nilRuntime.Tombstones != nil || nilRuntime.ObservationEvents != nil || nilRuntime.Budget.RPM != nil || nilRuntime.Budget.Daily != nil || nilRuntime.Budget.Tokens != nil || nilRuntime.Budget.Admissions != nil || nilRuntime.Budget.TokenEvents != nil {
		t.Fatalf("absent collections became authoritative: %#v", nilRuntime)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var encoded map[string]struct {
		Runtime map[string]json.RawMessage `json:"runtime"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"observations", "tombstones"} {
		if got := string(encoded["p1"].Runtime[key]); got != "[]" {
			t.Fatalf("%s encoded as %s, want []", key, got)
		}
	}
	if got := string(encoded["p1"].Runtime["observationEvents"]); got != "{}" {
		t.Fatalf("observationEvents encoded as %s, want {}", got)
	}
}

func TestCloneStateDetachesRuntimeAndOfferings(t *testing.T) {
	want := runtimeFixture()
	original := runtimeFixture()
	clone := CloneState(original)
	clone.DelistedModels[0] = "changed"
	levels := clone.Offerings["model/1"].ThinkingLevels
	(*levels)[0] = "changed"
	clone.Offerings["new"] = domain.ModelOfferingState{}
	clone.Runtime.SchemaVersion++
	clone.Runtime.Budget.RPM["model/1"][0] = time.Time{}
	clone.Runtime.Budget.RPM["new"] = []time.Time{}
	clone.Runtime.Budget.Daily["2026-11-07"]++
	clone.Runtime.Budget.Tokens["2026-11"]++
	admission := clone.Runtime.Budget.Admissions["attempt-1"]
	*admission.Allowed = false
	admission.Limits.Daily++
	clone.Runtime.Budget.Admissions["attempt-1"] = admission
	clone.Runtime.Budget.TokenEvents["new"] = TokenEventReceipt{}
	clone.Runtime.Observations[0].Observation.Scope.ModelID = "changed"
	clone.Runtime.Tombstones[0].Scope.ProviderID = "changed"
	event := clone.Runtime.ObservationEvents["observation-1"]
	event.Observation.Source = "changed"
	clone.Runtime.ObservationEvents["observation-1"] = event
	if !reflect.DeepEqual(original, want) {
		t.Fatalf("clone mutated original: got %#v, want %#v", original, want)
	}
}

func TestSyncFieldUpdatePreservesRuntime(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	want := runtimeFixture()
	if err := Save(path, map[string]State{"p1": want}); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := CloneState(*loaded["p1"])
	updated.LastCheckedMs++
	updated.LastError = "catalogue failure"
	updated.DelistedModels = []string{}
	updated.Offerings["new/model"] = domain.ModelOfferingState{Cost: "free"}
	if err := Save(path, map[string]State{"p1": updated}); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded["p1"].Runtime, want.Runtime) {
		t.Fatalf("sync update lost runtime fields: %#v", loaded["p1"].Runtime)
	}
}

func TestLoadNonMissingReadError(t *testing.T) {
	states, err := Load(t.TempDir())
	if err == nil || states != nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("directory read must fail, got states=%v err=%v", states, err)
	}
}

func TestLoadUnreadableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0600) })
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("current user can read files without permission bits")
	}
	states, err := Load(path)
	if err == nil || states != nil || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("permission failure must propagate, got states=%v err=%v", states, err)
	}
}

func TestRuntimeEncodeFailurePreservesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	want := runtimeFixture()
	if err := Save(path, map[string]State{"p1": want}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want.Runtime.Budget.RPM["model/1"][0] = time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := Save(path, map[string]State{"p1": want}); err == nil {
		t.Fatal("expected invalid runtime timestamp to fail JSON encoding")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("failed save replaced the previous durable document")
	}
}
