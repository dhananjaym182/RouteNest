package accounts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestCredentialsFileRoundTrip characterizes the legacy .routenest-credentials.json
// helper: filename, the refreshToken JSON tag, and load/save/reload, all inside
// a temporary directory.
func TestCredentialsFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	old := credentialsPath
	credentialsPath = filepath.Join(dir, ".routenest-credentials.json")
	t.Cleanup(func() { credentialsPath = old })

	if got := filepath.Base(findCredentialsFile()); got != ".routenest-credentials.json" {
		t.Fatalf("credentials filename = %q", got)
	}

	saveCredentials("rt-fixture")
	raw, err := os.ReadFile(credentialsPath)
	if err != nil {
		t.Fatalf("read credentials: %v", err)
	}
	var keys map[string]any
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("decode credentials: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("top-level keys = %v, want only refreshToken", keys)
	}
	if _, ok := keys["refreshToken"]; !ok {
		t.Fatalf("missing refreshToken tag in %s", raw)
	}

	c := loadCredentials()
	if c == nil || c.RefreshToken != "rt-fixture" {
		t.Fatalf("loadCredentials = %+v, want rt-fixture", c)
	}

	saveCredentials(c.RefreshToken)
	again, _ := os.ReadFile(credentialsPath)
	if string(again) != string(raw) {
		t.Errorf("credentials not stable across save/reload:\n%s\nvs\n%s", raw, again)
	}
}
