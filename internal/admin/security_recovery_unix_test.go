//go:build unix

package admin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"routenest/internal/platform"
)

func TestOfflineRecoveryRequiresControllingTTY(t *testing.T) {
	const helperEnv = "ROUTENEST_RECOVERY_TTY_TEST_HELPER"
	const helperDirEnv = "ROUTENEST_RECOVERY_TTY_TEST_DATA_DIR"
	if os.Getenv(helperEnv) == "1" {
		// The package TestMain calls app.Configure; set the recovery directory
		// only after that unrelated test harness has finished its startup.
		t.Setenv(platform.DataDirEnv, os.Getenv(helperDirEnv))
		if err := RecoverSecurityOffline(context.Background()); err == nil {
			t.Fatal("detached process accepted offline recovery")
		}
		return
	}
	dir := filepath.Join(t.TempDir(), "missing-data")
	cmd := exec.Command(os.Args[0], "-test.run=^TestOfflineRecoveryRequiresControllingTTY$")
	cmd.Env = append(os.Environ(), helperEnv+"=1", helperDirEnv+"="+dir)
	// A new Unix session has no controlling terminal, even when the test runner
	// itself was launched interactively. Pipes must never become secret delivery.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("headless recovery guard failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("headless recovery touched the data directory: %v", err)
	}
}
