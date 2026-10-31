package requestlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRequestLogsClear(t *testing.T) {
	oldLogs := Logs
	oldPath := Path
	dir := t.TempDir()
	Path = filepath.Join(dir, ".cline-request-logs.json")
	Mu.Lock()
	Logs = []requestLog{
		{ID: "req_2", StartedAt: time.Now()},
		{ID: "req_1", StartedAt: time.Now().Add(-time.Minute)},
	}
	saveRequestLogsLocked()
	Mu.Unlock()
	defer func() {
		Mu.Lock()
		Logs = oldLogs
		Mu.Unlock()
		Path = oldPath
	}()

	Clear()

	Mu.Lock()
	n := len(Logs)
	Mu.Unlock()
	if n != 0 {
		t.Fatalf("logs remain after clear: %d", n)
	}
	data, err := os.ReadFile(Path)
	if err != nil {
		t.Fatalf("persisted file missing: %v", err)
	}
	var persisted []requestLog
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("persisted file unreadable: %v (%q)", err, data)
	}
	if len(persisted) != 0 {
		t.Fatalf("persisted logs = %d, want 0", len(persisted))
	}
}
