package onboarding

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	state, err := Load(path)
	if err != nil || state.Version != stateVersion || len(state.VerifiedProviders) != 0 {
		t.Fatalf("empty state = %+v, %v", state, err)
	}
	want := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	state.VerifiedProviders["provider-1"] = want
	state.FirstSuccessfulGatewayRequestAt = &want
	if err := Save(path, state); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || !got.VerifiedProviders["provider-1"].Equal(want) || got.FirstSuccessfulGatewayRequestAt == nil || !got.FirstSuccessfulGatewayRequestAt.Equal(want) {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
}

func TestStateLoadsVersionOneWithoutCompletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(`{"version":1,"verifiedProviders":{"provider-1":"2026-11-09T12:00:00Z"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	state, err := Load(path)
	if err != nil || state.FirstSuccessfulGatewayRequestAt != nil || state.VerifiedProviders["provider-1"].IsZero() {
		t.Fatalf("existing version-one state = %+v, %v", state, err)
	}
}

func TestRecordSuccessfulGatewayRequestIsMonotonic(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	want := time.Date(2026, 11, 9, 12, 0, 0, 0, time.FixedZone("IST", 19800))
	if err := RecordSuccessfulGatewayRequest(path, time.Time{}); err == nil {
		t.Fatal("zero completion time was accepted")
	}
	if err := RecordSuccessfulGatewayRequest(path, want); err != nil {
		t.Fatal(err)
	}
	if err := RecordSuccessfulGatewayRequest(path, want.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	state, err := Load(path)
	if err != nil || state.FirstSuccessfulGatewayRequestAt == nil || !state.FirstSuccessfulGatewayRequestAt.Equal(want) || state.FirstSuccessfulGatewayRequestAt.Location() != time.UTC {
		t.Fatalf("first completion was changed = %+v, %v", state, err)
	}
}

func TestConcurrentVerificationAndRequestCompletionPreserveBoth(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	want := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	var workers sync.WaitGroup
	for i := 0; i < 20; i++ {
		workers.Go(func() {
			_, err := Update(path, func(state *State) bool {
				state.VerifiedProviders[fmt.Sprintf("provider-%d", i)] = want
				return true
			})
			if err != nil {
				t.Errorf("verification update: %v", err)
			}
		})
	}
	workers.Go(func() {
		if err := RecordSuccessfulGatewayRequest(path, want); err != nil {
			t.Errorf("completion update: %v", err)
		}
	})
	workers.Wait()
	state, err := Load(path)
	if err != nil || len(state.VerifiedProviders) != 20 || state.FirstSuccessfulGatewayRequestAt == nil || !state.FirstSuccessfulGatewayRequestAt.Equal(want) {
		t.Fatalf("concurrent updates lost evidence = %+v, %v", state, err)
	}
}

func TestRecordSuccessfulGatewayRequestDoesNotOverwriteInvalidState(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	invalid := []byte(`{"version":99,"verifiedProviders":{}}`)
	if err := os.WriteFile(path, invalid, 0600); err != nil {
		t.Fatal(err)
	}
	if err := RecordSuccessfulGatewayRequest(path, time.Now()); err == nil {
		t.Fatal("invalid state accepted")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(invalid) {
		t.Fatalf("invalid state was overwritten: %q, %v", data, err)
	}
}
