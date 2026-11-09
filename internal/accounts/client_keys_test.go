package accounts

import (
	"bytes"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"routenest/internal/domain"
	"routenest/internal/httpapi"
	"routenest/internal/platform"
	poolstore "routenest/internal/store/accounts"
)

func seedClientKeyPool(t *testing.T, seed *domain.AccountPool) {
	t.Helper()
	oldPool, oldPath, oldErr, oldErrPath := Pool, PoolPath, poolLoadErr, poolLoadErrPath
	t.Cleanup(func() { Pool, PoolPath, poolLoadErr, poolLoadErrPath = oldPool, oldPath, oldErr, oldErrPath })
	Pool = seed
	PoolPath = filepath.Join(t.TempDir(), poolstore.FileName)
	poolLoadErr, poolLoadErrPath = nil, ""
	if err := poolstore.SaveDurable(PoolPath, seed); err != nil {
		t.Fatal(err)
	}
	if err := savePoolMarker(PoolPath); err != nil {
		t.Fatal(err)
	}
}

func TestClientAuthSnapshotIsDetached(t *testing.T) {
	seedClientKeyPool(t, &domain.AccountPool{Accounts: []*domain.Account{}, Keys: []string{"stable"}, Models: []domain.Model{}})

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

func TestGenerateClientKeyUses256BitsAndPreservesPoolAcrossRestart(t *testing.T) {
	seed := &domain.AccountPool{
		Accounts:     []*domain.Account{{AccountID: "a1", RefreshToken: "secret"}},
		Keys:         []string{"existing"},
		Models:       []domain.Model{{ID: "m1"}},
		DefaultModel: "m1",
		ListenHost:   "127.0.0.2",
	}
	seedClientKeyPool(t, seed)
	randomBytes := bytes.Repeat([]byte{0xa5}, clientKeyRandomBytes)

	key, err := generateClientKey(bytes.NewReader(randomBytes), poolstore.SaveDurable)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) <= len("cline_") || key[:len("cline_")] != "cline_" {
		t.Fatalf("generated key format = %q", key)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(key[len("cline_"):])
	if err != nil || len(decoded) != clientKeyRandomBytes || !bytes.Equal(decoded, randomBytes) {
		t.Fatalf("generated key entropy bytes = %x, err = %v", decoded, err)
	}
	if !reflect.DeepEqual(Pool.Accounts, seed.Accounts) || !reflect.DeepEqual(Pool.Models, seed.Models) || Pool.DefaultModel != seed.DefaultModel || Pool.ListenHost != seed.ListenHost {
		t.Fatalf("unrelated pool state changed: %#v", Pool)
	}

	Pool = nil
	reloaded := LoadPool()
	if err := PoolStorageError(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reloaded.Keys, []string{"existing", key}) {
		t.Fatalf("reloaded keys = %v", reloaded.Keys)
	}
}

func TestGenerateClientKeyRetriesCollision(t *testing.T) {
	first := bytes.Repeat([]byte{0x11}, clientKeyRandomBytes)
	second := bytes.Repeat([]byte{0x22}, clientKeyRandomBytes)
	collision := "cline_" + base64.RawURLEncoding.EncodeToString(first)
	seedClientKeyPool(t, &domain.AccountPool{Accounts: []*domain.Account{}, Keys: []string{collision}, Models: []domain.Model{}})

	key, err := generateClientKey(bytes.NewReader(append(first, second...)), poolstore.SaveDurable)
	if err != nil {
		t.Fatal(err)
	}
	want := "cline_" + base64.RawURLEncoding.EncodeToString(second)
	if key != want || !reflect.DeepEqual(Pool.Keys, []string{collision, want}) {
		t.Fatalf("key = %q, pool = %v", key, Pool.Keys)
	}
}

func TestGenerateClientKeyDoesNotPublishBeforePersistence(t *testing.T) {
	seed := &domain.AccountPool{Accounts: []*domain.Account{}, Keys: []string{"existing"}, Models: []domain.Model{}}
	seedClientKeyPool(t, seed)
	wantDisk, err := poolstore.Load(PoolPath)
	if err != nil {
		t.Fatal(err)
	}

	_, err = generateClientKey(bytes.NewReader(bytes.Repeat([]byte{0x33}, clientKeyRandomBytes)), func(string, *AccountPool) error {
		return errors.New("injected pre-replacement failure")
	})
	if err == nil {
		t.Fatal("expected persistence failure")
	}
	if !reflect.DeepEqual(Pool.Keys, []string{"existing"}) {
		t.Fatalf("memory changed after failed persistence: %v", Pool.Keys)
	}
	gotDisk, loadErr := poolstore.Load(PoolPath)
	if loadErr != nil || !reflect.DeepEqual(gotDisk, wantDisk) {
		t.Fatalf("disk changed after failed persistence: %#v, err = %v", gotDisk, loadErr)
	}
}

func TestGenerateClientKeyReconcilesPostReplacementUncertainty(t *testing.T) {
	seedClientKeyPool(t, &domain.AccountPool{Accounts: []*domain.Account{}, Keys: []string{"existing"}, Models: []domain.Model{}})
	randomBytes := bytes.Repeat([]byte{0x44}, clientKeyRandomBytes)
	want := "cline_" + base64.RawURLEncoding.EncodeToString(randomBytes)

	_, err := generateClientKey(bytes.NewReader(randomBytes), func(path string, next *AccountPool) error {
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
	if !reflect.DeepEqual(Pool.Keys, []string{"existing", want}) {
		t.Fatalf("memory was not reconciled: %v", Pool.Keys)
	}
	persisted, loadErr := poolstore.Load(PoolPath)
	if loadErr != nil || !reflect.DeepEqual(persisted.Keys, Pool.Keys) {
		t.Fatalf("persisted keys = %v, err = %v", persisted.Keys, loadErr)
	}
}

func TestDeleteClientKeyProtectsFinalKey(t *testing.T) {
	seedClientKeyPool(t, &domain.AccountPool{Accounts: []*domain.Account{}, Keys: []string{"last"}, Models: []domain.Model{}})

	if _, err := DeleteClientKey("last", false, true); !errors.Is(err, ErrFinalClientKeyConfirmationRequired) {
		t.Fatalf("without confirmation error = %v", err)
	}
	if _, err := DeleteClientKey("last", true, false); !errors.Is(err, ErrFinalClientKeyAuthorizationRequired) {
		t.Fatalf("without elevation error = %v", err)
	}
	if !reflect.DeepEqual(Pool.Keys, []string{"last"}) {
		t.Fatalf("guarded deletion changed keys: %v", Pool.Keys)
	}
	result, err := DeleteClientKey("last", true, true)
	if err != nil || !result.DeletedFinalKey || len(Pool.Keys) != 0 {
		t.Fatalf("confirmed final deletion result = %+v, err = %v, keys = %v", result, err, Pool.Keys)
	}
}

func TestDeleteClientKeyRevokesLegacyDuplicatesOnly(t *testing.T) {
	seed := &domain.AccountPool{
		Accounts:     []*domain.Account{{AccountID: "a1"}},
		Keys:         []string{"duplicate", "survivor", "duplicate"},
		Models:       []domain.Model{{ID: "m1"}},
		DefaultModel: "m1",
	}
	seedClientKeyPool(t, seed)

	result, err := DeleteClientKey("duplicate", false, false)
	if err != nil || result.DeletedFinalKey {
		t.Fatalf("delete result = %+v, err = %v", result, err)
	}
	if !reflect.DeepEqual(Pool.Keys, []string{"survivor"}) || !reflect.DeepEqual(Pool.Accounts, seed.Accounts) || !reflect.DeepEqual(Pool.Models, seed.Models) || Pool.DefaultModel != seed.DefaultModel {
		t.Fatalf("pool after duplicate revocation = %#v", Pool)
	}
}

func TestDeleteClientKeyDoesNotPublishBeforePersistence(t *testing.T) {
	seedClientKeyPool(t, &domain.AccountPool{Accounts: []*domain.Account{}, Keys: []string{"one", "two"}, Models: []domain.Model{}})
	_, err := deleteClientKey("one", false, false, func(string, *AccountPool) error {
		return errors.New("injected pre-replacement failure")
	})
	if err == nil {
		t.Fatal("expected persistence failure")
	}
	if !reflect.DeepEqual(Pool.Keys, []string{"one", "two"}) {
		t.Fatalf("failed deletion changed memory: %v", Pool.Keys)
	}
	persisted, loadErr := poolstore.Load(PoolPath)
	if loadErr != nil || !reflect.DeepEqual(persisted.Keys, []string{"one", "two"}) {
		t.Fatalf("failed deletion changed disk: %v, err = %v", persisted.Keys, loadErr)
	}
}

func TestConcurrentClientKeyWritesDoNotLoseUpdates(t *testing.T) {
	seedClientKeyPool(t, &domain.AccountPool{Accounts: []*domain.Account{}, Keys: []string{"stable"}, Models: []domain.Model{}})
	const writers = 8
	generated := make(chan string, writers)
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key, err := GenerateClientKey()
			if err != nil {
				errs <- err
				return
			}
			generated <- key
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	close(generated)
	seen := map[string]bool{"stable": true}
	for key := range generated {
		if seen[key] {
			t.Fatalf("duplicate generated key %q", key)
		}
		seen[key] = true
	}
	keys, err := ClientAuthSnapshot()
	if err != nil || len(keys) != writers+1 {
		t.Fatalf("keys = %v, err = %v", keys, err)
	}
	for _, key := range keys {
		if !seen[key] {
			t.Fatalf("unexpected or lost key %q in %v", key, keys)
		}
	}
}

func TestConcurrentAuthenticationKeyMutationAndAccountDeletion(t *testing.T) {
	seedClientKeyPool(t, &domain.AccountPool{
		Accounts: []*domain.Account{{AccountID: "a1", RefreshToken: "secret"}},
		Keys:     []string{"stable"},
		Models:   []domain.Model{},
	})

	auth := (&httpapi.APIKeyAuth{
		Snapshot:     ClientAuthSnapshot,
		Unauthorized: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) },
	}).Wrap(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	start := make(chan struct{})
	errs := make(chan error, 4)
	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 100; i++ {
			req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			req.Header.Set("x-api-key", "stable")
			recorder := httptest.NewRecorder()
			auth(recorder, req)
			if recorder.Code != http.StatusNoContent {
				errs <- errors.New(recorder.Result().Status)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 20; i++ {
			key, err := GenerateClientKey()
			if err != nil {
				errs <- err
				return
			}
			if _, err := DeleteClientKey(key, false, false); err != nil {
				errs <- err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 100; i++ {
			if _, err := ClientKeySnapshot(); err != nil {
				errs <- err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		if err := DeleteAllAccountsStrict(); err != nil {
			errs <- err
		}
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent operation: %v", err)
	}

	keys, err := ClientAuthSnapshot()
	if err != nil || !reflect.DeepEqual(keys, []string{"stable"}) {
		t.Fatalf("stable key state = %v, err = %v", keys, err)
	}
}
