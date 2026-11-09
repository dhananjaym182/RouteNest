package admin

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"routenest/internal/accounts"
)

func TestOpenAdminSecurityAllowsAuthorizedRetryAfterUnapprovedFirstRun(t *testing.T) {
	if err := CloseSecurity(); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	poolPath := filepath.Join(dataDir, ".routenest-accounts.json")
	oldPool, oldPath := accounts.Pool, accounts.PoolPath
	accounts.Pool, accounts.PoolPath = nil, poolPath
	t.Cleanup(func() {
		_ = CloseSecurity()
		accounts.Pool, accounts.PoolPath = oldPool, oldPath
	})
	t.Setenv(accounts.InitializeEmptyAccountStoreEnv, "")

	options := SecurityOptions{
		DataDir:           dataDir,
		ListenHost:        "127.0.0.1",
		ListenPort:        3457,
		BootstrapPassword: "authorized retry bootstrap secret",
	}
	_, err := openAdminSecurity(options)
	if err == nil || !strings.Contains(err.Error(), "client authentication storage unavailable") {
		t.Fatalf("unapproved first run error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dataDir, "admin-auth")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("unapproved first run created administrator authority: %v", statErr)
	}

	accounts.Pool = nil
	t.Setenv(accounts.InitializeEmptyAccountStoreEnv, "true")
	runtime, err := openAdminSecurity(options)
	if err != nil {
		t.Fatalf("authorized retry: %v", err)
	}
	if err := runtime.close(); err != nil {
		t.Fatalf("close authorized retry: %v", err)
	}
	if _, err := os.Stat(poolPath); err != nil {
		t.Fatalf("initialized account pool: %v", err)
	}
	if _, err := os.Stat(poolPath + ".initialized"); err != nil {
		t.Fatalf("initialized account witness: %v", err)
	}
}
