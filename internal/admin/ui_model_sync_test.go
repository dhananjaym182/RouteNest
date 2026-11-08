package admin

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// Exercise the shipped controllers so delisting and provider-scoped removal
// regressions run in the normal Go gates as well as the standalone Node command.
func TestAdminModelSyncNotifications(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	script := filepath.Join("..", "..", "tools", "admin_model_sync.test.mjs")
	providerActions := filepath.Join("..", "..", "tools", "admin_model_provider_actions.test.mjs")
	if out, err := exec.Command(node, "--test", script, providerActions).CombinedOutput(); err != nil {
		t.Fatalf("model sync notification tests failed: %v\n%s", err, out)
	}
}
