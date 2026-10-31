package compaction

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The override.md file in the working directory replaces the request's system
// prompt, and the conversion keeps the request model.
func TestAnthropicToOpenAIAppliesOverride(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "override.md"), []byte("OVERRIDE-PROMPT"), 0o600); err != nil {
		t.Fatal(err)
	}
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	body := []byte(`{"model":"m-1","max_tokens":16,"system":"ORIGINAL","messages":[{"role":"user","content":"hi"}]}`)
	out, err := AnthropicToOpenAI(body)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), "OVERRIDE-PROMPT") {
		t.Fatalf("override.md not applied: %s", raw)
	}
	if strings.Contains(string(raw), "ORIGINAL") {
		t.Fatalf("original system prompt should be replaced: %s", raw)
	}
	if out["model"] != "m-1" {
		t.Fatalf("model = %v, want m-1", out["model"])
	}
}
