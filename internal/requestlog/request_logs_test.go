package requestlog

import (
	"testing"
	"time"
)

func TestPruneRequestLogsBoundsAgeAndCount(t *testing.T) {
	now := time.Now()
	old := now.Add(-31 * 24 * time.Hour)
	entries := []requestLog{
		{ID: "old", StartedAt: old},
		{ID: "keep1", StartedAt: now.Add(-time.Hour)},
		{ID: "keep2", StartedAt: now.Add(-2 * time.Hour)},
	}

	Mu.Lock()
	Logs = pruneRequestLogsLocked(entries)
	Mu.Unlock()

	if len(Logs) != 2 {
		t.Fatalf("expected 2 entries after age pruning, got %d", len(Logs))
	}
	if Logs[0].ID != "keep1" {
		t.Fatalf("expected newest first, got %q", Logs[0].ID)
	}
}

func TestListRequestLogsCursorPagination(t *testing.T) {
	now := time.Now()
	entries := make([]requestLog, 0, 75)
	for i := 0; i < 75; i++ {
		entries = append(entries, requestLog{
			ID:        "req_" + string(rune('a'+i)),
			StartedAt: now.Add(-time.Duration(75-i) * time.Second),
		})
	}

	Mu.Lock()
	Logs = pruneRequestLogsLocked(entries)
	Mu.Unlock()

	page1, err := List(50, "")
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1.Items) != 50 || !page1.HasMore {
		t.Fatalf("page1 unexpected: len=%d hasMore=%v", len(page1.Items), page1.HasMore)
	}
	if page1.Items[0].ID != Logs[0].ID {
		t.Fatalf("page1 first item = %q, want %q", page1.Items[0].ID, Logs[0].ID)
	}

	page2, err := List(50, page1.NextCursor)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2.Items) != 25 || page2.HasMore {
		t.Fatalf("page2 unexpected: len=%d hasMore=%v", len(page2.Items), page2.HasMore)
	}

	if page2.Items[0].ID != Logs[50].ID {
		t.Fatalf("page2 first item = %q, want %q", page2.Items[0].ID, Logs[50].ID)
	}
}

func TestListRequestLogsRejectsInvalidCursor(t *testing.T) {
	if _, err := List(50, "not-a-valid-cursor"); err == nil {
		t.Fatal("expected error for invalid cursor")
	}
}
