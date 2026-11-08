package accounts

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"routenest/internal/domain"
	"routenest/internal/httpapi"
	poolstore "routenest/internal/store/accounts"
)

func TestClientAuthSnapshotIsDetached(t *testing.T) {
	oldPool, oldPath, oldErr, oldErrPath := Pool, PoolPath, poolLoadErr, poolLoadErrPath
	t.Cleanup(func() { Pool, PoolPath, poolLoadErr, poolLoadErrPath = oldPool, oldPath, oldErr, oldErrPath })
	Pool = &domain.AccountPool{Accounts: []*domain.Account{}, Keys: []string{"stable"}, Models: []domain.Model{}}
	PoolPath = filepath.Join(t.TempDir(), poolstore.FileName)
	poolLoadErr, poolLoadErrPath = nil, ""

	first, err := ClientAuthSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	first[0] = "changed"
	second, err := ClientAuthSnapshot()
	if err != nil || len(second) != 1 || second[0] != "stable" {
		t.Fatalf("second snapshot = %v, err = %v", second, err)
	}
}

func TestConcurrentAuthenticationKeyMutationAndAccountDeletion(t *testing.T) {
	oldPool, oldPath, oldErr, oldErrPath := Pool, PoolPath, poolLoadErr, poolLoadErrPath
	t.Cleanup(func() { Pool, PoolPath, poolLoadErr, poolLoadErrPath = oldPool, oldPath, oldErr, oldErrPath })
	Pool = &domain.AccountPool{
		Accounts: []*domain.Account{{AccountID: "a1", RefreshToken: "secret"}},
		Keys:     []string{"stable"},
		Models:   []domain.Model{},
	}
	PoolPath = filepath.Join(t.TempDir(), poolstore.FileName)
	poolLoadErr, poolLoadErrPath = nil, ""
	if err := poolstore.SaveDurable(PoolPath, Pool); err != nil {
		t.Fatal(err)
	}
	if err := savePoolMarker(PoolPath); err != nil {
		t.Fatal(err)
	}

	auth := (&httpapi.APIKeyAuth{
		Snapshot:     ClientAuthSnapshot,
		Unauthorized: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) },
	}).Wrap(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	start := make(chan struct{})
	errs := make(chan string, 4)
	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 200; i++ {
			req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			req.Header.Set("x-api-key", "stable")
			recorder := httptest.NewRecorder()
			auth(recorder, req)
			if recorder.Code != http.StatusNoContent {
				errs <- recorder.Result().Status
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 100; i++ {
			AddClientKey("ephemeral")
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 100; i++ {
			DeleteClientKey("ephemeral")
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		if err := DeleteAllAccountsStrict(); err != nil {
			errs <- err.Error()
		}
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent operation: %s", err)
	}

	keys, err := ClientAuthSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, key := range keys {
		found = found || key == "stable"
	}
	if !found {
		t.Fatalf("stable key lost: %v", keys)
	}
}
