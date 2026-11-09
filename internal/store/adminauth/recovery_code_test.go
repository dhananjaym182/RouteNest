package adminauth

import (
	"context"
	"errors"
	"strings"
	"testing"

	auth "routenest/internal/adminauth"
)

func TestRecoveryCodeSurvivesRestartAndFaultDoesNotPublish(t *testing.T) {
	dir := privateDir(t)
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	options := auth.Options{PasswordParams: auth.PasswordParams{Memory: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32}, BootstrapPassword: "one time bootstrap secret"}
	service, err := auth.NewService(store, options)
	if err != nil {
		t.Fatal(err)
	}
	_, code, err := service.CompleteBootstrapWithRecovery(context.Background(), "one time bootstrap secret", "AdminRecovery8!")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := auth.NewService(reopened, options)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := restarted.RecoverWithCode(context.Background(), code, "NewRecovery8!", "127.0.0.1")
	if err != nil || replacement == "" {
		t.Fatalf("restart recovery = %q, %v", replacement, err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}

	faulted, err := Open(dir, Options{Fault: func(point FaultPoint) error {
		if point == BeforeSnapshotWrite {
			return errors.New("injected recovery persistence failure")
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	faultedService, err := auth.NewService(faulted, options)
	if err != nil {
		t.Fatal(err)
	}
	preparedCode, err := faultedService.RecoverWithCode(context.Background(), replacement, "FailedRecovery8!", "127.0.0.1")
	if err == nil || !errors.Is(err, auth.ErrMutationPrepared) || !strings.Contains(err.Error(), "injected") || preparedCode == "" {
		t.Fatalf("faulted recovery error = %v", err)
	}
	_ = faulted.Close()

	finalStore, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer finalStore.Close()
	finalService, err := auth.NewService(finalStore, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := finalService.Login(context.Background(), "admin", "FailedRecovery8!", "127.0.0.1"); err != nil {
		t.Fatalf("prepared mutation did not recover deterministically: %v", err)
	}
	if _, err := finalService.RecoverWithCode(context.Background(), preparedCode, "FinalRecovery8!", "127.0.0.1"); err != nil {
		t.Fatalf("prepared replacement code was unusable: %v", err)
	}
}

func TestBootstrapPreparedMutationReturnsDurableRecoveryCode(t *testing.T) {
	dir := privateDir(t)
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	options := auth.Options{PasswordParams: auth.PasswordParams{Memory: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32}, BootstrapPassword: "one time bootstrap secret"}
	service, err := auth.NewService(store, options)
	if err != nil {
		t.Fatal(err)
	}
	store.fault = func(point FaultPoint) error {
		if point == BeforeSnapshotWrite {
			return errors.New("injected bootstrap persistence failure")
		}
		return nil
	}
	_, recoveryCode, err := service.CompleteBootstrapWithRecovery(context.Background(), "one time bootstrap secret", "AdminRecovery8!")
	if err == nil || !errors.Is(err, auth.ErrMutationPrepared) || recoveryCode == "" {
		t.Fatalf("prepared bootstrap = code %q, error %v", recoveryCode, err)
	}
	_ = store.Close()

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restarted, err := auth.NewService(reopened, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Login(context.Background(), "admin", "AdminRecovery8!", "127.0.0.1"); err != nil {
		t.Fatalf("prepared administrator was not recovered: %v", err)
	}
	if _, err := restarted.RecoverWithCode(context.Background(), recoveryCode, "RecoveredAdmin8!", "127.0.0.1"); err != nil {
		t.Fatalf("prepared recovery code was unusable: %v", err)
	}
}

func TestRecoveryCodeIssuePreparedMutationReturnsDurableCode(t *testing.T) {
	dir := privateDir(t)
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	options := auth.Options{PasswordParams: auth.PasswordParams{Memory: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32}, BootstrapPassword: "one time bootstrap secret"}
	service, err := auth.NewService(store, options)
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := service.CompleteBootstrap(context.Background(), "one time bootstrap secret", "AdminRecovery8!")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	faulted, err := Open(dir, Options{Fault: func(point FaultPoint) error {
		if point == BeforeSnapshotWrite {
			return errors.New("injected recovery-code issue failure")
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	faultedService, err := auth.NewService(faulted, options)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := faultedService.Authenticate(context.Background(), credentials.Token)
	if err != nil {
		t.Fatal(err)
	}
	recoveryCode, err := faultedService.IssueRecoveryCode(context.Background(), principal, "AdminRecovery8!", "127.0.0.1")
	if err == nil || !errors.Is(err, auth.ErrMutationPrepared) || recoveryCode == "" {
		t.Fatalf("prepared issue = code %q, error %v", recoveryCode, err)
	}
	_ = faulted.Close()

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restarted, err := auth.NewService(reopened, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.RecoverWithCode(context.Background(), recoveryCode, "RecoveredAdmin8!", "127.0.0.1"); err != nil {
		t.Fatalf("prepared issued code was unusable: %v", err)
	}
}
