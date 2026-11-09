package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"routenest/internal/accounts"
)

func TestAuthorizeEmptyAccountStoreForProcess(t *testing.T) {
	t.Setenv(accounts.InitializeEmptyAccountStoreEnv, "")
	if err := authorizeEmptyAccountStoreForProcess(false); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(accounts.InitializeEmptyAccountStoreEnv); got != "" {
		t.Fatalf("environment changed without authorization: %q", got)
	}
	if err := authorizeEmptyAccountStoreForProcess(true); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(accounts.InitializeEmptyAccountStoreEnv); got != "true" {
		t.Fatalf("environment = %q, want true", got)
	}
}

func TestPrepareClientAuthenticationStorageAllowsAuthorizedRetry(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, ".routenest-accounts.json")
	oldPool, oldPath := accounts.Pool, accounts.PoolPath
	accounts.Pool, accounts.PoolPath = nil, path
	t.Cleanup(func() {
		accounts.Pool, accounts.PoolPath = oldPool, oldPath
	})
	t.Setenv(accounts.InitializeEmptyAccountStoreEnv, "")

	if err := prepareClientAuthenticationStorage(false); !errors.Is(err, accounts.ErrPoolStorageUnavailable) {
		t.Fatalf("unapproved first run error = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unapproved first run created account storage: %v", err)
	}

	accounts.Pool = nil
	if err := prepareClientAuthenticationStorage(true); err != nil {
		t.Fatalf("authorized retry: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("initialized account pool: %v", err)
	}
	if _, err := os.Stat(path + ".initialized"); err != nil {
		t.Fatalf("initialized account witness: %v", err)
	}
}
