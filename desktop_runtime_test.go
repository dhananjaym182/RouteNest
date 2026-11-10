//go:build desktop

package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"routenest/internal/accounts"
	"routenest/internal/admin"
	"routenest/internal/adminauth"
	"routenest/internal/desktopauth"
	"routenest/internal/platform"
	accountstore "routenest/internal/store/accounts"
)

func TestDesktopBootstrapCompletionRequiresRestartForUncertainMutation(t *testing.T) {
	for _, err := range []error{adminauth.ErrMutationPrepared, admin.ErrDesktopHandoffUnavailable} {
		result, completionErr := desktopBootstrapCompletion("recovery", err)
		if completionErr != nil || !result.RestartRequired || result.RecoveryCode != "recovery" || result.HandoffToken != "" || result.AdminURL != "/admin/" {
			t.Fatalf("completion for %v = %+v, %v", err, result, completionErr)
		}
	}
	if _, err := desktopBootstrapCompletion("", errors.New("pre-bootstrap failure")); err == nil {
		t.Fatal("ordinary setup failure was reported as restart completion")
	}
}

func TestDesktopBootstrapCompletionDoesNotCreateEarlyHandoff(t *testing.T) {
	result, err := desktopBootstrapCompletion("recovery", nil)
	if err != nil || result.RestartRequired || result.HandoffToken != "" || result.RecoveryCode != "recovery" || result.AdminURL != "/admin/" {
		t.Fatalf("normal completion = %+v, %v", result, err)
	}
}

func TestDesktopRuntimeSeparatesEmbeddedConsoleFromGatewayURL(t *testing.T) {
	runtime := &desktopRuntime{port: 3457}
	for host, want := range map[string]string{
		"127.0.0.1": "http://127.0.0.1:3457/v1",
		"localhost": "http://localhost:3457/v1",
		"::1":       "http://[::1]:3457/v1",
		"0.0.0.0":   "http://127.0.0.1:3457/v1",
		"::":        "http://[::1]:3457/v1",
	} {
		runtime.setHost(host)
		if runtime.adminURL != "/admin/" || runtime.gatewayURL != want {
			t.Fatalf("URLs for %q = %q, %q; want /admin/, %q", host, runtime.adminURL, runtime.gatewayURL, want)
		}
	}
}

func desktopWindowTestContext() context.Context {
	return context.WithValue(context.Background(), "buildtype", "production")
}

func TestDesktopPrivilegedOperationsRequireEmbeddedWindowStartup(t *testing.T) {
	for _, buildType := range []string{"", "dev", "production", "debug"} {
		t.Run(buildType, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), "buildtype", buildType)
			want := buildType == "production" || buildType == "debug"
			if desktopTrustedWindowContext(ctx) != want {
				t.Fatalf("trusted window for %q differs from %t", buildType, want)
			}
			for _, key := range []string{"assetdir", "devserver", "frontenddevserverurl"} {
				for _, value := range []any{"", "http://127.0.0.1:3000", true} {
					if desktopTrustedWindowContext(context.WithValue(ctx, key, value)) {
						t.Fatalf("accepted %s context with %s=%v", buildType, key, value)
					}
				}
			}
		})
	}
	if desktopTrustedWindowContext(nil) || desktopTrustedWindowContext(context.Background()) {
		t.Fatal("accepted context without packaged Wails startup")
	}
	store := &desktopCredentialKeyring{}
	runtime := &desktopRuntime{credentialStore: desktopauth.NewManagerWithKeyring(store)}
	runtime.bridge = runtime.newBridge(desktopSetupState{Mode: "login"}, nil)
	if _, err := runtime.bridge.DesktopStatus(); err == nil {
		t.Fatal("native operation succeeded before window startup")
	}
	runtime.onWindowStartup(context.WithValue(desktopWindowTestContext(), "assetdir", ""))
	if _, err := runtime.bridge.DesktopStatus(); err == nil {
		t.Fatal("native operation accepted external asset context")
	}
	if state := runtime.bridge.State(); state.Mode != "error" {
		t.Fatalf("untrusted window state = %q", state.Mode)
	}
	runtime.onWindowStartup(desktopWindowTestContext())
	if _, err := runtime.bridge.DesktopStatus(); err == nil {
		t.Fatal("later startup call upgraded rejected context")
	}
}

func isolateDesktopAccounts(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	t.Setenv(platform.DataDirEnv, dataDir)
	t.Setenv(platform.LegacyDataDirEnv, "")
	t.Setenv(accounts.InitializeEmptyAccountStoreEnv, "")
	t.Setenv("CLINE_PROXY_HOST", "")
	accounts.PoolMu.Lock()
	oldPool, oldPath := accounts.Pool, accounts.PoolPath
	accounts.Pool, accounts.PoolPath = nil, filepath.Join(dataDir, accountstore.FileName)
	accounts.PoolMu.Unlock()
	t.Cleanup(func() {
		accounts.PoolMu.Lock()
		accounts.Pool, accounts.PoolPath = oldPool, oldPath
		accounts.PoolMu.Unlock()
	})
	return dataDir
}

func TestDesktopOccupiedPortCannotBootstrapOrCaptureHandoff(t *testing.T) {
	for _, initialize := range []bool{false, true} {
		t.Run(map[bool]string{false: "native-setup", true: "explicit-initialize"}[initialize], func(t *testing.T) {
			dataDir := isolateDesktopAccounts(t)
			attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			defer attacker.Close()
			port := attacker.Listener.Addr().(*net.TCPAddr).Port
			runtime, err := prepareDesktopRuntime("127.0.0.1", port, initialize, false)
			if initialize {
				if err == nil {
					_ = runtime.close()
					t.Fatal("explicit initialization accepted an occupied listener")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				defer runtime.close()
				runtime.credentialStore = desktopauth.NewManagerWithKeyring(&desktopCredentialKeyring{})
				runtime.onWindowStartup(desktopWindowTestContext())
				result, err := runtime.bridge.Complete("PortOwnership8!", "PortOwnership8!", false)
				if err == nil || result.RecoveryCode != "" || result.HandoffToken != "" {
					t.Fatalf("occupied listener completed setup: %+v, %v", result, err)
				}
			}
			for _, path := range []string{filepath.Join(dataDir, accountstore.FileName), filepath.Join(dataDir, "admin-auth")} {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("occupied listener mutated %s: %v", path, err)
				}
			}
			if listener, err := net.Listen("tcp", attacker.Listener.Addr().String()); err == nil {
				_ = listener.Close()
				t.Fatal("existing listener lost port ownership")
			}
		})
	}
}
