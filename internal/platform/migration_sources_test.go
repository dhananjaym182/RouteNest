package platform

import (
	"os"
	"path/filepath"
	"testing"
)

// The OS config directory must never be a migration source. Copying from it
// duplicated live credentials into an explicit ROUTENEST_DATA_DIR.
func TestLegacyDataCandidatesExcludeOSConfigDir(t *testing.T) {
	cfg := t.TempDir()
	old := userConfigDir
	userConfigDir = func() (string, error) { return cfg, nil }
	t.Cleanup(func() { userConfigDir = old })

	home := filepath.Join(cfg, "routenest")
	for _, d := range LegacyDataCandidates(nil) {
		abs, _ := filepath.Abs(home)
		if d == abs {
			t.Fatalf("legacy candidates include the OS config dir: %s", d)
		}
	}
}

// A state file in the OS config dir is not copied into an explicit data dir.
func TestMigrationDoesNotCopyFromOSConfigDir(t *testing.T) {
	cfg := t.TempDir()
	old := userConfigDir
	userConfigDir = func() (string, error) { return cfg, nil }
	t.Cleanup(func() { userConfigDir = old })

	secret := []byte(`{"accounts":[{"refreshToken":"do-not-copy"}]}`)
	src := filepath.Join(cfg, "routenest")
	if err := os.MkdirAll(src, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".routenest-accounts.json"), secret, 0o600); err != nil {
		t.Fatal(err)
	}

	dst := t.TempDir()
	if err := MigrateDataFiles(dst, nil); err != nil {
		t.Fatal(err)
	}
	if FileExists(filepath.Join(dst, ".routenest-accounts.json")) {
		t.Fatal("state was copied from the OS config directory")
	}
}
