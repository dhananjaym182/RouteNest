package admin

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The assembled page's inline script must parse. Runs `node --check` when node is
// installed; the test is skipped otherwise.
func TestAdminAssembledScriptParses(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	start := strings.LastIndex(adminHTML, "<script>\n")
	end := strings.LastIndex(adminHTML, "</script>")
	if start < 0 || end < start {
		t.Fatal("assembled page has no script block")
	}
	script := adminHTML[start+len("<script>\n") : end]
	file := filepath.Join(t.TempDir(), "admin.js")
	if err := os.WriteFile(file, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, "--check", file).CombinedOutput(); err != nil {
		t.Fatalf("assembled script does not parse: %v\n%s", err, out)
	}
}
