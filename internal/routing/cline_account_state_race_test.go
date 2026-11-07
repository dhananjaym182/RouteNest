package routing

import (
	"context"
	"errors"
	"net/http"
	"runtime"
	"testing"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/domain"
	"routenest/internal/provideradapters"
)

type blockingClineInvoker struct {
	started  chan struct{}
	release  chan struct{}
	response *http.Response
	err      error
}

func (invoker *blockingClineInvoker) Invoke(_ context.Context, _ provideradapters.Invocation) (*http.Response, error) {
	close(invoker.started)
	<-invoker.release
	return invoker.response, invoker.err
}

func TestCallClineAccountSynchronizesFailureState(t *testing.T) {
	invoker := &blockingClineInvoker{
		started: make(chan struct{}),
		release: make(chan struct{}),
		err:     errors.New("upstream unavailable"),
	}
	publishClineTestAdapter(t, provideradapters.Bundle{ID: "cline", Invoker: invoker})
	account := &domain.Account{
		AccountID: "failure-race", Email: "failure-race@example.com", AccessToken: "token",
		ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), Status: "active",
	}
	installClineAccountPool(t, account)

	done := make(chan error, 1)
	go func() {
		_, _, err := CallClineAPIWithAccount(account, map[string]any{"model": "model"}, false)
		done <- err
	}()
	<-invoker.started

	accounts.PoolMu.Lock()
	close(invoker.release)
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		_, _ = account.Status, account.CooldownUntil
		runtime.Gosched()
	}
	accounts.PoolMu.Unlock()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("transport failure unexpectedly succeeded")
		}
		status, until := accounts.AccountAvailability(account)
		if status != "cooldown" || until.IsZero() {
			t.Fatalf("failure state = (%q, %s), want persisted cooldown", status, until)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("failure state update did not finish after the account-state lock was released")
	}
}

func TestCallClineAccountSynchronizesSuccessState(t *testing.T) {
	invoker := &blockingClineInvoker{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		response: clineAdapterResponse(http.StatusOK, `{}`),
	}
	publishClineTestAdapter(t, provideradapters.Bundle{ID: "cline", Invoker: invoker})
	account := &domain.Account{
		AccountID: "success-race", Email: "success-race@example.com", AccessToken: "token",
		ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), Status: "active",
	}
	installClineAccountPool(t, account)

	type callResult struct {
		response *http.Response
		err      error
	}
	done := make(chan callResult, 1)
	go func() {
		response, _, err := CallClineAPIWithAccount(account, map[string]any{"model": "model"}, false)
		done <- callResult{response: response, err: err}
	}()
	<-invoker.started

	accounts.PoolMu.Lock()
	close(invoker.release)
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		_, _ = account.LastUsed, account.UsageCount
		runtime.Gosched()
	}
	accounts.PoolMu.Unlock()

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("successful upstream call: %v", result.err)
		}
		result.response.Body.Close()
		accounts.PoolMu.Lock()
		lastUsed, usageCount := account.LastUsed, account.UsageCount
		accounts.PoolMu.Unlock()
		if lastUsed.IsZero() || usageCount != 1 {
			t.Fatalf("success usage = (%s, %d), want timestamp and one request", lastUsed, usageCount)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("success state update did not finish after the account-state lock was released")
	}
}
