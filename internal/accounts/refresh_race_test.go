package accounts_test

import (
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/egress"
)

func TestRefreshAccountTokenSynchronizesSharedAccountMutation(t *testing.T) {
	oldPool, oldPoolPath, oldTransport := accounts.Pool, accounts.PoolPath, egress.Client.Transport
	account := &Account{
		AccountID:    "refresh-race",
		Email:        "refresh-race@example.com",
		RefreshToken: "old-refresh",
		Status:       "active",
	}
	accounts.PoolMu.Lock()
	accounts.Pool = &AccountPool{Accounts: []*Account{account}}
	accounts.PoolPath = filepath.Join(t.TempDir(), ".routenest-accounts.json")
	accounts.PoolMu.Unlock()
	t.Cleanup(func() {
		accounts.PoolMu.Lock()
		accounts.Pool, accounts.PoolPath = oldPool, oldPoolPath
		accounts.PoolMu.Unlock()
		egress.Client.Transport = oldTransport
	})

	requestStarted := make(chan struct{})
	allowResponse := make(chan struct{})
	egress.Client.Transport = freeModelRoundTripper(func(req *http.Request) (*http.Response, error) {
		close(requestStarted)
		<-allowResponse
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(
				`{"data":{"accessToken":"new-access","refreshToken":"new-refresh","expiresAt":4102444800000}}`,
			)),
			Header:  make(http.Header),
			Request: req,
		}, nil
	})

	refreshDone := make(chan error, 1)
	go func() { refreshDone <- accounts.RefreshAccountToken(account) }()
	<-requestStarted

	// The pool mutex is the account-state synchronization boundary. Holding it
	// while the refresh response arrives makes an unlocked refresh mutation a
	// deterministic race, while a correct implementation waits for the lock.
	accounts.PoolMu.Lock()
	close(allowResponse)
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		_, _, _, _ = account.AccessToken, account.RefreshToken, account.ExpiresAt, account.Status
		runtime.Gosched()
	}
	accounts.PoolMu.Unlock()

	select {
	case err := <-refreshDone:
		if err != nil {
			t.Fatalf("refresh account token: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("refresh did not complete after the account-state lock was released")
	}
}
