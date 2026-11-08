package accounts

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"routenest/internal/domain"
	"routenest/internal/httpapi"
	poolstore "routenest/internal/store/accounts"
)

func TestFreshAccountStorageRequiresExplicitInitialization(t *testing.T) {
	path := filepath.Join(t.TempDir(), poolstore.FileName)
	t.Setenv(InitializeEmptyAccountStoreEnv, "")
	if _, state, err := LoadPoolFromDisk(path); err == nil || state != PoolStorageMissing {
		t.Fatalf("without opt-in = (%q, %v), want missing failure", state, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unapproved initialization created pool: %v", err)
	}

	t.Setenv(InitializeEmptyAccountStoreEnv, "true")
	pool, state, err := LoadPoolFromDisk(path)
	if err != nil || state != PoolStorageFirstRun || len(pool.Keys) != 0 {
		t.Fatalf("approved first run = (%#v, %q, %v)", pool, state, err)
	}
	if _, err := os.Stat(path + ".initialized"); err != nil {
		t.Fatalf("initialization witness: %v", err)
	}

	t.Setenv(InitializeEmptyAccountStoreEnv, "")
	pool, state, err = LoadPoolFromDisk(path)
	if err != nil || state != PoolStorageReady || len(pool.Keys) != 0 {
		t.Fatalf("restart = (%#v, %q, %v)", pool, state, err)
	}
}

func TestEstablishedEvidenceBlocksEmptyStoreInitialization(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, poolstore.FileName)
	if err := os.WriteFile(filepath.Join(dir, ".routenest-config.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(InitializeEmptyAccountStoreEnv, "true")
	if _, state, err := LoadPoolFromDisk(path); err == nil || state != PoolStorageMissing || !strings.Contains(err.Error(), "established data directory") {
		t.Fatalf("established missing storage = (%q, %v)", state, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("established state was silently initialized: %v", err)
	}
}

func TestValidPoolWithoutWitnessBackfillsAfterValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), poolstore.FileName)
	want := &domain.AccountPool{Accounts: []*domain.Account{}, Keys: []string{}, Models: []domain.Model{}}
	if err := poolstore.SaveDurable(path, want); err != nil {
		t.Fatal(err)
	}
	t.Setenv(InitializeEmptyAccountStoreEnv, "")
	got, state, err := LoadPoolFromDisk(path)
	if err != nil || state != PoolStorageReady || len(got.Keys) != 0 {
		t.Fatalf("load = (%#v, %q, %v)", got, state, err)
	}
	if _, err := os.Stat(path + ".initialized"); err != nil {
		t.Fatalf("backfilled witness: %v", err)
	}
}

func TestWitnessWithoutPoolFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), poolstore.FileName)
	if err := savePoolMarker(path); err != nil {
		t.Fatal(err)
	}
	t.Setenv(InitializeEmptyAccountStoreEnv, "true")
	if _, state, err := LoadPoolFromDisk(path); err == nil || state != PoolStorageMissing {
		t.Fatalf("orphan witness = (%q, %v)", state, err)
	}
}

func TestCorruptOrInvalidWitnessFailsClosed(t *testing.T) {
	for _, marker := range []string{`{`, `{"version":99}`} {
		t.Run(marker, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), poolstore.FileName)
			if err := poolstore.SaveDurable(path, &domain.AccountPool{Accounts: []*domain.Account{}, Keys: []string{}, Models: []domain.Model{}}); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path+".initialized", []byte(marker), 0600); err != nil {
				t.Fatal(err)
			}
			if _, state, err := LoadPoolFromDisk(path); err == nil || state != PoolStorageCorrupt {
				t.Fatalf("marker %q = (%q, %v)", marker, state, err)
			}
		})
	}
}

func TestNonRegularWitnessFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), poolstore.FileName)
	if err := poolstore.SaveDurable(path, &domain.AccountPool{Accounts: []*domain.Account{}, Keys: []string{}, Models: []domain.Model{}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".initialized", 0700); err != nil {
		t.Fatal(err)
	}
	if _, state, err := LoadPoolFromDisk(path); err == nil || state != PoolStorageCorrupt {
		t.Fatalf("non-regular marker = (%q, %v)", state, err)
	}
}

func TestMissingEstablishedStorageNeverOpensAuthenticatedRoutes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, poolstore.FileName)
	if err := os.WriteFile(filepath.Join(dir, ".routenest-providers.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	oldPool, oldPath, oldErr, oldErrPath := Pool, PoolPath, poolLoadErr, poolLoadErrPath
	t.Cleanup(func() { Pool, PoolPath, poolLoadErr, poolLoadErrPath = oldPool, oldPath, oldErr, oldErrPath })
	Pool, PoolPath, poolLoadErr, poolLoadErrPath = nil, path, nil, ""
	t.Setenv(InitializeEmptyAccountStoreEnv, "true")

	auth := &httpapi.APIKeyAuth{
		Snapshot:     ClientAuthSnapshot,
		Unauthorized: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) },
	}
	called := false
	recorder := httptest.NewRecorder()
	auth.Wrap(func(http.ResponseWriter, *http.Request) { called = true })(recorder, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if called || recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("called=%v status=%d, want fail-closed 503", called, recorder.Code)
	}
}
