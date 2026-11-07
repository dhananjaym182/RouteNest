package requestlog

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"routenest/internal/budget"
	"routenest/internal/platform"
	"routenest/internal/protocol/openaichat"
	"routenest/internal/providers"
)

func TestFinalizeReplaysOneIdempotentProviderTokenEvent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(platform.DataDirEnv, dir)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	backend, _, _, err := providers.NewLocalRuntimeStateBackend(func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	store := budget.NewStore(backend, nil, budget.Options{Now: func() time.Time { return now }})
	if err := budget.Publish(store); err != nil {
		t.Fatal(err)
	}
	receipt, err := store.CheckAndRecordWithPolicy(context.Background(), budget.AdmissionCommand{
		ProviderID: "provider-finalize", ModelID: "m", AttemptID: "attempt-finalize", At: now,
		Policy: budget.Policy{TokenLimit: 100, TokenWindow: "daily"},
	})
	if err != nil {
		t.Fatal(err)
	}

	Mu.Lock()
	oldLogs, oldPath := Logs, Path
	Logs = nil
	Path = filepath.Join(dir, ".routenest-request-logs.json")
	Mu.Unlock()
	t.Cleanup(func() {
		Mu.Lock()
		Logs, Path = oldLogs, oldPath
		Mu.Unlock()
	})

	entry := &requestLog{
		ID: "request-finalize", StartedAt: now,
		ProviderID: "provider-finalize", BudgetAttemptID: receipt.AttemptID,
	}
	usage := openaichat.Usage{Valid: true, Prompt: 3, Completion: 4, Total: 7}
	Finalize(entry, usage, time.Time{}, now, true, "")
	Finalize(entry, usage, time.Time{}, now, true, "")

	state, err := backend.Snapshot(context.Background(), "provider-finalize")
	if err != nil {
		t.Fatal(err)
	}
	if state.Tokens["2026-11-07"] != 7 || state.Tokens["2026-11"] != 7 || len(state.TokenEvents) != 1 {
		t.Fatalf("replayed finalization changed accounting: %+v", state)
	}
}
