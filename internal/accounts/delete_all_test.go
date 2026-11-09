package accounts

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"routenest/internal/domain"
	"routenest/internal/httpapi"
	"routenest/internal/platform"
	poolstore "routenest/internal/store/accounts"
)

func TestDeleteAllAccountsStrictPreservesIndependentPoolStateAcrossRestart(t *testing.T) {
	oldPool, oldPath, oldErr, oldErrPath := Pool, PoolPath, poolLoadErr, poolLoadErrPath
	t.Cleanup(func() { Pool, PoolPath, poolLoadErr, poolLoadErrPath = oldPool, oldPath, oldErr, oldErrPath })

	created := time.Date(2026, 11, 8, 4, 5, 6, 0, time.UTC)
	seed := &domain.AccountPool{
		Accounts: []*domain.Account{{
			AccountID: "a1", Email: "a@example.com", RefreshToken: "secret", Status: "cooldown",
			CooldownUntil: created.Add(time.Hour), UsageCount: 4,
			ModelStats:     map[string]*domain.ModelStat{"m1": {ModelID: "m1", UsageCount: 3}},
			ModelCooldowns: map[string]time.Time{"m1": created.Add(time.Hour)},
		}},
		CurrentIdx:              1,
		Keys:                    []string{"client-key-1", "client-key-2"},
		Models:                  []domain.Model{{ID: "m1", Provider: "cline", Status: "active", Context: 128000}},
		DefaultModel:            "m1",
		ModelCatalogInitialized: true,
		RemovedModelOfferings:   []domain.ModelRemovalRef{{ID: "removed", Source: "remote", ProviderID: "cline"}},
		ListenHost:              "127.0.0.2",
		AdminPasswordHash:       "sentinel-hash",
		AdminPasswordSalt:       "sentinel-salt",
	}
	Pool = seed
	PoolPath = filepath.Join(t.TempDir(), poolstore.FileName)
	poolLoadErr, poolLoadErrPath = nil, ""
	if err := poolstore.SaveDurable(PoolPath, seed); err != nil {
		t.Fatal(err)
	}

	if err := DeleteAllAccountsStrict(); err != nil {
		t.Fatalf("delete all: %v", err)
	}
	if Pool != seed {
		t.Fatal("successful deletion must preserve the shared pool identity")
	}
	assertAccountOnlyDeletion(t, Pool, seed)

	Pool = nil
	reloaded := LoadPool()
	if err := PoolStorageError(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	assertAccountOnlyDeletion(t, reloaded, seed)

	if err := DeleteAllAccountsStrict(); err != nil {
		t.Fatalf("idempotent deletion: %v", err)
	}
	assertAccountOnlyDeletion(t, Pool, seed)
}

func TestSavePoolCandidateStrictLockedReconcilesUncertainReplacement(t *testing.T) {
	oldPool, oldPath, oldErr, oldErrPath := Pool, PoolPath, poolLoadErr, poolLoadErrPath
	t.Cleanup(func() { Pool, PoolPath, poolLoadErr, poolLoadErrPath = oldPool, oldPath, oldErr, oldErrPath })

	PoolPath = filepath.Join(t.TempDir(), poolstore.FileName)
	Pool = &domain.AccountPool{Keys: []string{"client-key"}, DefaultModel: "old"}
	poolLoadErr, poolLoadErrPath = nil, ""
	if err := poolstore.SaveDurable(PoolPath, Pool); err != nil {
		t.Fatal(err)
	}
	next := clonePool(Pool)
	next.DefaultModel = "new"

	PoolMu.Lock()
	err := savePoolCandidateStrictLocked(next, func(path string, candidate *AccountPool) error {
		return poolstore.SaveDurableWithOptions(path, candidate, platform.AtomicWriteOptions{Fault: func(stage platform.AtomicWriteStage) error {
			if stage == platform.AtomicWriteDirectorySync {
				return errors.New("directory sync failed")
			}
			return nil
		}})
	})
	PoolMu.Unlock()
	if err == nil || !platform.AtomicWriteMayHaveReplaced(err) {
		t.Fatalf("error = %v, want uncertain replacement", err)
	}
	if Pool.DefaultModel != "new" {
		t.Fatalf("memory was not reconciled to the replaced file: %+v", Pool)
	}
	persisted, loadErr := poolstore.Load(PoolPath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if persisted.DefaultModel != "new" || !reflect.DeepEqual(persisted.Keys, []string{"client-key"}) {
		t.Fatalf("unexpected persisted candidate: %+v", persisted)
	}
}

func TestDeleteAllAccountsStrictPreservesClientAuthentication(t *testing.T) {
	oldPool, oldPath, oldErr, oldErrPath := Pool, PoolPath, poolLoadErr, poolLoadErrPath
	t.Cleanup(func() { Pool, PoolPath, poolLoadErr, poolLoadErrPath = oldPool, oldPath, oldErr, oldErrPath })

	Pool = &domain.AccountPool{
		Accounts: []*domain.Account{{AccountID: "a1", RefreshToken: "secret"}},
		Keys:     []string{"surviving-client-key"},
		Models:   []domain.Model{},
	}
	PoolPath = filepath.Join(t.TempDir(), poolstore.FileName)
	poolLoadErr, poolLoadErrPath = nil, ""
	if err := poolstore.SaveDurable(PoolPath, Pool); err != nil {
		t.Fatal(err)
	}
	if err := DeleteAllAccountsStrict(); err != nil {
		t.Fatal(err)
	}

	auth := &httpapi.APIKeyAuth{
		Snapshot:     ClientAuthSnapshot,
		Unauthorized: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) },
	}
	handler := auth.Wrap(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, tc := range []struct {
		name string
		key  string
		want int
	}{
		{name: "missing", want: http.StatusUnauthorized},
		{name: "wrong", key: "wrong", want: http.StatusUnauthorized},
		{name: "surviving", key: "surviving-client-key", want: http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			if tc.key != "" {
				req.Header.Set("x-api-key", tc.key)
			}
			recorder := httptest.NewRecorder()
			handler(recorder, req)
			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.want)
			}
		})
	}
}

func TestDeleteAllAccountsStrictReportsPostReplacementUncertaintyTruthfully(t *testing.T) {
	oldPool, oldPath, oldErr, oldErrPath := Pool, PoolPath, poolLoadErr, poolLoadErrPath
	t.Cleanup(func() { Pool, PoolPath, poolLoadErr, poolLoadErrPath = oldPool, oldPath, oldErr, oldErrPath })

	seed := &domain.AccountPool{
		Accounts: []*domain.Account{{AccountID: "a1", RefreshToken: "secret"}},
		Keys:     []string{"client-key"},
		Models:   []domain.Model{},
	}
	Pool = seed
	PoolPath = filepath.Join(t.TempDir(), poolstore.FileName)
	poolLoadErr, poolLoadErrPath = nil, ""
	if err := poolstore.SaveDurable(PoolPath, seed); err != nil {
		t.Fatal(err)
	}

	err := deleteAllAccountsStrict(func(path string, next *AccountPool) error {
		return poolstore.SaveDurableWithOptions(path, next, platform.AtomicWriteOptions{Fault: func(stage platform.AtomicWriteStage) error {
			if stage == platform.AtomicWriteDirectorySync {
				return errors.New("injected directory sync failure")
			}
			return nil
		}})
	})
	if err == nil || !platform.AtomicWriteMayHaveReplaced(err) {
		t.Fatalf("error = %v, want post-replacement uncertainty", err)
	}
	persisted, loadErr := poolstore.Load(PoolPath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(persisted.Accounts) != 0 || !reflect.DeepEqual(persisted.Keys, seed.Keys) {
		t.Fatalf("truthful persisted state = %#v", persisted)
	}
	if len(Pool.Accounts) != 0 || !reflect.DeepEqual(Pool.Keys, seed.Keys) {
		t.Fatalf("memory was not reconciled to replacement: %#v", Pool)
	}
}

func TestDeleteAllAccountsStrictDoesNotPublishBeforeDurableReplacement(t *testing.T) {
	oldPool, oldPath, oldErr, oldErrPath := Pool, PoolPath, poolLoadErr, poolLoadErrPath
	t.Cleanup(func() { Pool, PoolPath, poolLoadErr, poolLoadErrPath = oldPool, oldPath, oldErr, oldErrPath })

	seed := &domain.AccountPool{
		Accounts: []*domain.Account{{AccountID: "a1", RefreshToken: "secret"}},
		Keys:     []string{"client-key"},
		Models:   []domain.Model{},
	}
	Pool = seed
	PoolPath = filepath.Join(t.TempDir(), "storage-directory")
	poolLoadErr, poolLoadErrPath = nil, ""
	if err := os.Mkdir(PoolPath, 0700); err != nil {
		t.Fatal(err)
	}

	if err := DeleteAllAccountsStrict(); err == nil {
		t.Fatal("expected persistence error")
	}
	if Pool != seed || len(Pool.Accounts) != 1 || !reflect.DeepEqual(Pool.Keys, seed.Keys) {
		t.Fatalf("failed persistence published mutation: %#v", Pool)
	}
}

func TestDeleteAllAccountsStrictKeepsExistingPoolReferencesCurrent(t *testing.T) {
	oldPool, oldPath, oldErr, oldErrPath := Pool, PoolPath, poolLoadErr, poolLoadErrPath
	t.Cleanup(func() { Pool, PoolPath, poolLoadErr, poolLoadErrPath = oldPool, oldPath, oldErr, oldErrPath })

	Pool = &domain.AccountPool{
		Accounts: []*domain.Account{{AccountID: "a1", RefreshToken: "secret"}},
		Keys:     []string{"client-key"},
		Models:   []domain.Model{},
	}
	shared := Pool
	PoolPath = filepath.Join(t.TempDir(), poolstore.FileName)
	poolLoadErr, poolLoadErrPath = nil, ""
	if err := poolstore.SaveDurable(PoolPath, Pool); err != nil {
		t.Fatal(err)
	}
	if err := DeleteAllAccountsStrict(); err != nil {
		t.Fatal(err)
	}
	if Pool != shared || len(shared.Accounts) != 0 || !reflect.DeepEqual(shared.Keys, []string{"client-key"}) {
		t.Fatalf("shared pool reference became stale: pool=%p shared=%p value=%#v", Pool, shared, shared)
	}
}

func TestDeleteAllAccountsStrictSerializesConcurrentAccountMutation(t *testing.T) {
	oldPool, oldPath, oldErr, oldErrPath := Pool, PoolPath, poolLoadErr, poolLoadErrPath
	t.Cleanup(func() { Pool, PoolPath, poolLoadErr, poolLoadErrPath = oldPool, oldPath, oldErr, oldErrPath })

	Pool = &domain.AccountPool{
		Accounts: []*domain.Account{{AccountID: "before", Email: "before@example.com"}},
		Keys:     []string{"client-key"},
		Models:   []domain.Model{},
	}
	PoolPath = filepath.Join(t.TempDir(), poolstore.FileName)
	poolLoadErr, poolLoadErrPath = nil, ""
	if err := poolstore.SaveDurable(PoolPath, Pool); err != nil {
		t.Fatal(err)
	}

	var start sync.WaitGroup
	start.Add(1)
	done := make(chan struct{})
	go func() {
		start.Done()
		AddAccount(&domain.Account{AccountID: "after", Email: "after@example.com", RefreshToken: "new", Status: "active"})
		close(done)
	}()
	start.Wait()
	if err := DeleteAllAccountsStrict(); err != nil {
		t.Fatalf("delete all: %v", err)
	}
	<-done

	persisted, err := poolstore.Load(PoolPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(Pool.Accounts, persisted.Accounts) || !reflect.DeepEqual(Pool.Keys, persisted.Keys) {
		t.Fatalf("memory/disk diverged: memory=%#v disk=%#v", Pool, persisted)
	}
	if !reflect.DeepEqual(Pool.Keys, []string{"client-key"}) {
		t.Fatalf("key changed: %v", Pool.Keys)
	}
}

func TestDurableAccountSaveFaultStages(t *testing.T) {
	stages := []platform.AtomicWriteStage{
		platform.AtomicWritePermission,
		platform.AtomicWriteData,
		platform.AtomicWriteFileSync,
		platform.AtomicWriteRename,
		platform.AtomicWriteDirectorySync,
	}
	for _, stage := range stages {
		t.Run(string(stage), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), poolstore.FileName)
			before := &domain.AccountPool{Accounts: []*domain.Account{{AccountID: "a1"}}, Keys: []string{"key"}, Models: []domain.Model{}}
			if err := poolstore.SaveDurable(path, before); err != nil {
				t.Fatal(err)
			}
			after := clonePool(before)
			after.Accounts = []*domain.Account{}
			err := poolstore.SaveDurableWithOptions(path, after, platform.AtomicWriteOptions{Fault: func(point platform.AtomicWriteStage) error {
				if point == stage {
					return errors.New("injected")
				}
				return nil
			}})
			if err == nil {
				t.Fatal("expected injected error")
			}
			persisted, loadErr := poolstore.Load(path)
			if loadErr != nil {
				t.Fatalf("reload: %v", loadErr)
			}
			if stage == platform.AtomicWriteDirectorySync {
				if !platform.AtomicWriteMayHaveReplaced(err) || len(persisted.Accounts) != 0 {
					t.Fatalf("post-replacement uncertainty not represented: err=%v pool=%#v", err, persisted)
				}
			} else if platform.AtomicWriteMayHaveReplaced(err) || len(persisted.Accounts) != 1 {
				t.Fatalf("pre-replacement failure changed authority: err=%v pool=%#v", err, persisted)
			}
		})
	}
}

func TestPoolStorageErrorClearsAfterExplicitRepair(t *testing.T) {
	oldPool, oldPath, oldErr, oldErrPath := Pool, PoolPath, poolLoadErr, poolLoadErrPath
	t.Cleanup(func() { Pool, PoolPath, poolLoadErr, poolLoadErrPath = oldPool, oldPath, oldErr, oldErrPath })

	path := filepath.Join(t.TempDir(), poolstore.FileName)
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	Pool, PoolPath, poolLoadErr, poolLoadErrPath = nil, path, nil, ""
	LoadPool()
	if PoolStorageError() == nil {
		t.Fatal("corrupt storage did not fail closed")
	}
	if err := poolstore.SaveDurable(path, &domain.AccountPool{Accounts: []*domain.Account{}, Keys: []string{"restored"}, Models: []domain.Model{}}); err != nil {
		t.Fatal(err)
	}
	Pool = nil
	LoadPool()
	if err := PoolStorageError(); err != nil {
		t.Fatalf("storage error persisted after repair: %v", err)
	}
}

func TestLoadPoolFromDiskDistinguishesSecurityStates(t *testing.T) {
	t.Setenv(InitializeEmptyAccountStoreEnv, "true")
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.json")
	pool, state, err := LoadPoolFromDisk(missing)
	if err != nil || state != PoolStorageFirstRun || pool == nil || len(pool.Keys) != 0 {
		t.Fatalf("first run = (%#v, %q, %v)", pool, state, err)
	}
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	if _, state, err = LoadPoolFromDisk(missing); err == nil || state != PoolStorageMissing {
		t.Fatalf("missing initialized storage = (%q, %v)", state, err)
	}

	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte(`{"accounts":[],"keys":[],"models":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	pool, state, err = LoadPoolFromDisk(empty)
	if err != nil || state != PoolStorageReady || len(pool.Keys) != 0 {
		t.Fatalf("intentional empty = (%#v, %q, %v)", pool, state, err)
	}

	corrupt := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(corrupt, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, state, err = LoadPoolFromDisk(corrupt); err == nil || state != PoolStorageCorrupt {
		t.Fatalf("corrupt = (%q, %v)", state, err)
	}
	if _, state, err = LoadPoolFromDisk(corrupt); err == nil || state != PoolStorageCorrupt {
		t.Fatalf("missing after quarantine = (%q, %v)", state, err)
	}

	unreadable := filepath.Join(dir, "directory")
	if err := os.Mkdir(unreadable, 0700); err != nil {
		t.Fatal(err)
	}
	if _, state, err = LoadPoolFromDisk(unreadable); err == nil || state != PoolStorageUnreadable {
		t.Fatalf("unreadable = (%q, %v)", state, err)
	}
}

func assertAccountOnlyDeletion(t *testing.T, got, seed *domain.AccountPool) {
	t.Helper()
	if got.Accounts == nil || len(got.Accounts) != 0 || got.CurrentIdx != 0 {
		t.Fatalf("account state = %#v", got)
	}
	if !reflect.DeepEqual(got.Keys, seed.Keys) || !reflect.DeepEqual(got.Models, seed.Models) ||
		got.DefaultModel != seed.DefaultModel || got.ModelCatalogInitialized != seed.ModelCatalogInitialized ||
		!reflect.DeepEqual(got.RemovedModelOfferings, seed.RemovedModelOfferings) ||
		got.ListenHost != seed.ListenHost || got.AdminPasswordHash != seed.AdminPasswordHash ||
		got.AdminPasswordSalt != seed.AdminPasswordSalt {
		t.Fatalf("independent state changed: got=%#v seed=%#v", got, seed)
	}
}
