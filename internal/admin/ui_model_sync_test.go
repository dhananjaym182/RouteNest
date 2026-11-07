package admin

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// Exercise the shipped controllers so delisting popup regressions are included
// in the normal Go test gates as well as the standalone Node test command.
func TestAdminModelSyncNotifications(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	script := filepath.Join("..", "..", "tools", "admin_model_sync.test.mjs")
	if out, err := exec.Command(node, "--test", script).CombinedOutput(); err != nil {
		t.Fatalf("model sync notification tests failed: %v\n%s", err, out)
	}
}
