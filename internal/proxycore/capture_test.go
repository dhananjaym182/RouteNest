package proxycore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"routenest/internal/platform"
)

func TestCaptureDefaultDirectoryUsesDataDir(t *testing.T) {
	want := filepath.Join(platform.CurrentDataDir(), "capture-logs")
	if got := captureDir(); got != want {
		t.Fatalf("captureDir() = %q, want %q", got, want)
	}
}

func TestSaveCaptureEntryUsesPrivatePermissions(t *testing.T) {
	oldDir, oldEntries := captureLogDir, allEntries
	captureLogDir = filepath.Join(t.TempDir(), "capture-logs")
	allEntries = nil
	t.Cleanup(func() {
		captureLogDir, allEntries = oldDir, oldEntries
	})

	entry := &CaptureEntry{Step: 1, Name: "OAuth Token", ReqHeaders: map[string]string{"Authorization": "secret"}}
	saveCaptureEntry(entry)

	stepPath := filepath.Join(captureLogDir, "step-01-OAuth-Token.json")
	fullPath := filepath.Join(captureLogDir, "full-capture.json")
	for _, path := range []string{stepPath, fullPath} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !json.Valid(data) {
			t.Fatalf("%s does not contain valid JSON", path)
		}
	}
	if runtime.GOOS == "windows" {
		return
	}
	dirInfo, _ := os.Stat(captureLogDir)
	stepInfo, _ := os.Stat(stepPath)
	fullInfo, _ := os.Stat(fullPath)
	if got := dirInfo.Mode().Perm(); got != 0700 {
		t.Errorf("capture dir mode = %04o, want 0700", got)
	}
	if got := stepInfo.Mode().Perm(); got != 0600 {
		t.Errorf("step file mode = %04o, want 0600", got)
	}
	if got := fullInfo.Mode().Perm(); got != 0600 {
		t.Errorf("full file mode = %04o, want 0600", got)
	}
}
