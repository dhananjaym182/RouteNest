package adminauth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestBootstrapWithRecoveryPersistsOnlyVerifier(t *testing.T) {
	service, repo := testService(t)
	credentials, code, err := service.CompleteBootstrapWithRecovery(t.Context(), "one time bootstrap secret", "AdminRecovery8!")
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 43 {
		t.Fatalf("recovery code length = %d", len(code))
	}
	available, err := service.RecoveryCodeStatus(t.Context())
	if err != nil || !available {
		t.Fatalf("recovery status = %v, %v", available, err)
	}
	persisted, err := json.Marshal(repo.state)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{code, credentials.Token, credentials.CSRFToken, "AdminRecovery8!"} {
		if strings.Contains(string(persisted), secret) {
			t.Fatalf("plaintext secret persisted: %q", secret)
		}
	}
}

func TestRecoveryCodeIsSingleUseRotatesAndRevokesEverySession(t *testing.T) {
	service, _ := testService(t)
	first, code, err := service.CompleteBootstrapWithRecovery(t.Context(), "one time bootstrap secret", "AdminRecovery8!")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Login(t.Context(), "admin", "AdminRecovery8!", "127.0.0.2")
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := service.RecoverWithCode(t.Context(), code, "NewRecovery8!", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if replacement == "" || replacement == code {
		t.Fatal("recovery code was not rotated")
	}
	for _, token := range []string{first.Token, second.Token} {
		if _, err := service.Authenticate(t.Context(), token); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("session survived recovery: %v", err)
		}
	}
	if _, err := service.Login(t.Context(), "admin", "AdminRecovery8!", "127.0.0.1"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old password accepted: %v", err)
	}
	if _, err := service.Login(t.Context(), "admin", "NewRecovery8!", "127.0.0.1"); err != nil {
		t.Fatalf("new password rejected: %v", err)
	}
	if _, err := service.RecoverWithCode(t.Context(), code, "ReplayRecovery8!", "127.0.0.1"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("used recovery code accepted: %v", err)
	}
	if _, err := service.RecoverWithCode(t.Context(), replacement, "ThirdRecovery8!", "127.0.0.1"); err != nil {
		t.Fatalf("replacement recovery code rejected: %v", err)
	}
}

func TestConcurrentRecoveryAllowsOneWinner(t *testing.T) {
	service, _ := testService(t)
	_, code, err := service.CompleteBootstrapWithRecovery(t.Context(), "one time bootstrap secret", "AdminRecovery8!")
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, password := range []string{"WinnerRecovery8!", "OtherRecovery8!"} {
		wait.Add(1)
		go func(password string) {
			defer wait.Done()
			<-start
			_, err := service.RecoverWithCode(context.Background(), code, password, "127.0.0.1")
			results <- err
		}(password)
	}
	close(start)
	wait.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("unexpected concurrent result: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful recoveries = %d, want 1", successes)
	}
}

func TestIssueRecoveryCodeRequiresBreakGlassPassword(t *testing.T) {
	service, _ := testService(t)
	credentials := bootstrap(t, service)
	if _, err := service.IssueRecoveryCode(t.Context(), credentials.Principal, "wrong password", "127.0.0.1"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong password issue = %v", err)
	}
	denials := service.Denials()
	if len(denials) != 1 || denials[0].Kind != AuditRecoveryCodeDenied || denials[0].Source != "127.0.0.1" {
		t.Fatalf("recovery-code issue denial was not buffered: %+v", denials)
	}
	code, err := service.IssueRecoveryCode(t.Context(), credentials.Principal, "permanent admin password", "127.0.0.1")
	if err != nil || code == "" {
		t.Fatalf("issue recovery code = %q, %v", code, err)
	}
	state, err := service.State(t.Context())
	if err != nil || state.RecoveryCodePHC == "" {
		t.Fatalf("recovery verifier missing: %v", err)
	}
}
