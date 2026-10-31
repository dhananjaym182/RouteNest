package requestlog

import (
	"testing"

	"routenest/internal/domain"
)

// Snapshot is a copy: changing it does not change the stored logs.
func TestSnapshotIsCopy(t *testing.T) {
	Mu.Lock()
	old := Logs
	Logs = []domain.RequestLog{{ID: "a", AccountID: "acc"}}
	Mu.Unlock()
	t.Cleanup(func() {
		Mu.Lock()
		Logs = old
		Mu.Unlock()
	})

	snap := Snapshot()
	if len(snap) != 1 || snap[0].ID != "a" {
		t.Fatalf("snapshot = %+v", snap)
	}
	snap[0].ID = "changed"
	Mu.Lock()
	defer Mu.Unlock()
	if Logs[0].ID != "a" {
		t.Fatal("editing the snapshot changed the stored logs")
	}
}
