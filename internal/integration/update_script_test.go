//go:build linux

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestUpdateScriptPreservesControllingTerminalForBootstrap(t *testing.T) {
	scriptCommand, err := exec.LookPath("script")
	if err != nil {
		t.Skip("util-linux script command is required for controlling-terminal coverage")
	}

	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	source, err := os.ReadFile(filepath.Join(repoRoot, "scripts", "update.sh"))
	if err != nil {
		t.Fatalf("read update script: %v", err)
	}

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatalf("create fixture scripts directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "scripts", "update.sh"), source, 0o755); err != nil {
		t.Fatalf("write update script fixture: %v", err)
	}

	binDir := filepath.Join(root, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatalf("create fixture bin directory: %v", err)
	}
	writeUpdateFixtureExecutable(t, binDir, "go", `#!/bin/sh
exit 0
`)
	writeUpdateFixtureExecutable(t, binDir, "git", `#!/bin/sh
case "$1" in
  describe) printf '%s\n' test-version ;;
  rev-parse) printf '%s\n' test-commit ;;
esac
`)
	writeUpdateFixtureExecutable(t, binDir, "pkill", `#!/bin/sh
exit 1
`)
	writeUpdateFixtureExecutable(t, binDir, "pgrep", `#!/bin/sh
if [ -s "$UPDATE_TEST_PID_FILE" ]; then
  pid=$(sed -n '1p' "$UPDATE_TEST_PID_FILE")
  if kill -0 "$pid" 2>/dev/null; then
    printf '%s\n' "$pid"
    exit 0
  fi
fi
exit 1
`)
	writeUpdateFixtureExecutable(t, binDir, "curl", `#!/bin/sh
printf '%s' 200
`)

	pidFile := filepath.Join(root, "routenest.pid")
	writeUpdateFixtureExecutable(t, root, "routenest", `#!/bin/sh
if ! exec 3>/dev/tty; then
  printf '%s\n' 'no controlling terminal' >&2
  exit 42
fi
printf '%s\n' 'bootstrap terminal available' >&3
exec 3>&-
printf '%s\n' "$$" > "$UPDATE_TEST_PID_FILE"
trap 'exit 0' TERM INT
while :; do
  sleep 1
done
`)

	command := exec.Command(scriptCommand, "-q", "-e", "-c", "sh scripts/update.sh --no-pull --no-test", "/dev/null")
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"UPDATE_TEST_PID_FILE="+pidFile,
		"HEALTH_URL=http://127.0.0.1:3457/admin/",
	)
	output, err := command.CombinedOutput()
	t.Cleanup(func() { stopUpdateFixtureProcess(t, pidFile) })
	if err != nil {
		t.Fatalf("update script lost the controlling terminal needed for bootstrap: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "bootstrap terminal available") {
		t.Fatalf("bootstrap delivery did not reach the controlling terminal:\n%s", output)
	}
	assertUpdateFixtureProcessRunning(t, pidFile)
}

func writeUpdateFixtureExecutable(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertUpdateFixtureProcessRunning(t *testing.T, pidFile string) {
	t.Helper()
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read fixture pid after updater exit: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("parse fixture pid after updater exit: %v", err)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("find fixture process after updater exit: %v", err)
	}
	if err := process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("nohup service did not survive updater exit: %v", err)
	}
}

func stopUpdateFixtureProcess(t *testing.T, pidFile string) {
	t.Helper()
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		if !os.IsNotExist(err) {
			t.Errorf("read fixture pid: %v", err)
		}
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Errorf("parse fixture pid: %v", err)
		return
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Errorf("find fixture process: %v", err)
		return
	}
	if err := process.Signal(syscall.SIGTERM); err != nil && !strings.Contains(err.Error(), "process already finished") {
		t.Errorf("stop fixture process: %v", err)
		return
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := process.Signal(syscall.Signal(0)); err != nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Errorf("fixture process %d did not stop", pid)
}
