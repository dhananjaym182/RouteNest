package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"routenest/internal/platform"
)

func adminAuthHelpArguments() [][]string {
	return [][]string{{"auth"}, {"auth", "help"}, {"auth", "--help"}, {"auth", "-h"}, {"auth", "status", "--help"}, {"auth", "status", "-h"}, {"auth", "recover", "--help"}, {"auth", "recover", "-h"}}
}

func assertAdminAuthHelpDiscoverability(t *testing.T, output string) {
	t.Helper()
	var missing []string
	for _, expected := range []string{
		"\n  routenest auth status  ",
		"\n  routenest auth recover  ",
		"\n  routenest auth recover --help  ",
		"\n  routenest auth help  ",
		"CLI/server",
		"go build .",
		"http://localhost:3457/admin/",
		"configured host/port",
		"Create password:",
		"one-time bootstrap secret",
		"ROUTENEST_ADMIN_BOOTSTRAP_PASSWORD",
		`go build -tags "desktop production" -o routenest-desktop .`,
		"Native setup needs no terminal bootstrap secret",
		"Administrator Security -> Change password",
		"Administrator Security -> Recovery code",
		"current-password reauthentication",
		`"Forgot password?"`,
		"saved recovery code",
		"Password changes and using a recovery code to reset a forgotten password revoke",
		"generating or rotating only a recovery code does not",
		"Trusted Devices:",
		"Administrator Security -> Desktop Sign-In",
		"revoking, or forgetting",
		"Native/Desktop Sign Out pauses",
		"ordinary browser logout only ends its browser session",
		"does not",
		"pause or revoke native device trust",
		"Ordinary browsers require independent administrator",
		"data-directory owner in a local controlling terminal",
		"intact authority is required",
		"--login (CLI/server only) runs provider OAuth",
		"not administrator login",
		"no plaintext password flags",
	} {
		if !strings.Contains(output, expected) {
			missing = append(missing, expected)
		}
	}
	if len(missing) != 0 {
		t.Errorf("help omitted administrator commands or password guidance %q:\n%s", missing, output)
	}
}

func TestAuthCommandRoutingAndHelp(t *testing.T) {
	for _, args := range [][]string{{"auth", "reset"}, {"auth", "recover", "password"}, {"auth", "recover", "--host", "0.0.0.0"}, {"auth", "status", "extra"}} {
		if handled, err := runAdminAuthCommand(args, &bytes.Buffer{}); !handled || err == nil {
			t.Fatalf("auth command %v = handled %v, err %v", args, handled, err)
		}
	}
	for _, args := range [][]string{nil, {"--port", "3457"}, {"-h"}, {"--help"}} {
		if handled, err := runAdminAuthCommand(args, &bytes.Buffer{}); handled || err != nil {
			t.Fatalf("ordinary command %v = handled %v, err %v", args, handled, err)
		}
	}
	for _, args := range adminAuthHelpArguments() {
		var output bytes.Buffer
		if handled, err := runAdminAuthCommand(args, &output); !handled || err != nil || !strings.Contains(output.String(), "password-reset") {
			t.Fatalf("help command %v = handled %v, err %v, output %q", args, handled, err, output.String())
		}
		if len(args) <= 2 {
			assertAdminAuthHelpDiscoverability(t, output.String())
		}
	}
}

func TestAuthRootHelpDoesNotMutateState(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "missing")
	t.Setenv(platform.DataDirEnv, dataDir)
	for _, argument := range []string{"-h", "--help"} {
		command := flag.NewFlagSet("routenest", flag.ContinueOnError)
		var output bytes.Buffer
		command.SetOutput(&output)
		command.Bool("initialize-empty-account-store", false, "Authorize a confirmed fresh client store")
		configureRootHelp(command)
		if err := command.Parse([]string{argument}); !errors.Is(err, flag.ErrHelp) {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), "auth <status|recover|help>") || !strings.Contains(output.String(), "not a password-reset mechanism") {
			t.Fatalf("root help omitted authentication guidance: %q", output.String())
		}
		assertAdminAuthHelpDiscoverability(t, output.String())
		if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
			t.Fatal("root help created state", err)
		}
	}
}

func TestAuthHelpEntryPointsDoNotMutateState(t *testing.T) {
	const helperEnv = "ROUTENEST_AUTH_HELP_TEST_PROCESS"
	if arguments := os.Getenv(helperEnv); arguments != "" {
		flag.CommandLine = flag.NewFlagSet("routenest", flag.ExitOnError)
		os.Args = append([]string{"routenest"}, strings.Fields(arguments)...)
		main()
		return
	}
	arguments := append([][]string{{"-h"}, {"--help"}}, adminAuthHelpArguments()...)
	for _, args := range arguments {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := t.TempDir()
			dataDir := filepath.Join(root, "missing")
			legacyDir := filepath.Join(root, "legacy")
			command := exec.Command(os.Args[0], "-test.run=^TestAuthHelpEntryPointsDoNotMutateState$")
			command.Env = append(os.Environ(), helperEnv+"="+strings.Join(args, " "), platform.DataDirEnv+"="+dataDir, platform.LegacyDataDirEnv+"="+legacyDir)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("help process failed: %v\n%s", err, output)
			}
			if !strings.Contains(string(output), "auth") || !strings.Contains(string(output), "not a password-reset mechanism") {
				t.Fatalf("help process output = %q", output)
			}
			if len(args) <= 2 {
				assertAdminAuthHelpDiscoverability(t, string(output))
			}
			for _, path := range []string{dataDir, legacyDir} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("help process created %s: %v", path, err)
				}
			}
		})
	}
}

func TestAuthHelpDoesNotOpenExistingAuthority(t *testing.T) {
	state := adminStatusFixture(time.Now().UTC())
	dataDir, _ := adminStatusStore(t, state)
	before := adminStatusTree(t, dataDir)
	for _, args := range adminAuthHelpArguments() {
		var output bytes.Buffer
		if handled, err := runAdminAuthCommand(args, &output); !handled || err != nil {
			t.Fatalf("help %v = %v, %v", args, handled, err)
		}
		assertAdminStatusRedacted(t, output.String(), state)
	}
	assertAdminStatusUnchanged(t, dataDir, before)
}

type adminAuthErrorWriter struct{}

func (adminAuthErrorWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func TestAuthHelpOutputFailureDoesNotOpenState(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "missing")
	t.Setenv(platform.DataDirEnv, dataDir)
	for _, args := range adminAuthHelpArguments() {
		if handled, err := runAdminAuthCommand(args, adminAuthErrorWriter{}); !handled || !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("help output failure %v = %v, %v", args, handled, err)
		}
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatal("failed help output created state", err)
	}
}

func TestAuthHelpDoesNotMutateState(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "missing")
	t.Setenv(platform.DataDirEnv, dataDir)
	for _, args := range adminAuthHelpArguments() {
		if handled, err := runAdminAuthCommand(args, &bytes.Buffer{}); !handled || err != nil {
			t.Fatalf("help %v = %v, %v", args, handled, err)
		}
		if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
			t.Fatalf("help %v created state: %v", args, err)
		}
	}
}
