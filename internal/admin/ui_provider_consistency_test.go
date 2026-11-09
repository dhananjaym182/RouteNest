package admin

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAdminProviderStateConsistency(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	script := filepath.Join("..", "..", "tools", "admin_provider_consistency.test.mjs")
	if out, err := exec.Command(node, "--test", script).CombinedOutput(); err != nil {
		t.Fatalf("provider consistency tests failed: %v\n%s", err, out)
	}
}
