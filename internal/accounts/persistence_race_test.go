package accounts_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/domain"
	"routenest/internal/routing"
)

func TestConcurrentPoolMutationsPersistUnderSingleLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".routenest-accounts.json")
	usePoolPath(t, path)
	account := &domain.Account{
		AccountID:      "persist-race",
		Email:          "persist-race@example.com",
		RefreshToken:   "refresh-token",
		Status:         "active",
		CreatedAt:      time.Now(),
		ModelStats:     map[string]*domain.ModelStat{},
		ModelCooldowns: map[string]time.Time{},
	}
	accounts.AddAccount(account)

	const iterations = 40
	errCh := make(chan error, iterations*2)
	var group sync.WaitGroup
	group.Add(5)
	go func() {
		defer group.Done()
		for i := 0; i < iterations; i++ {
			accounts.RecordAccountUse(account, time.Unix(int64(i+1), 0))
		}
	}()
	go func() {
		defer group.Done()
		for i := 0; i < iterations; i++ {
			routing.RecordTokenUsage(account, "", routing.TokenUsage{Prompt: 1, Completion: 2, Total: 3, Valid: true})
		}
	}()
	go func() {
		defer group.Done()
		for i := 0; i < iterations; i++ {
			accounts.PoolMu.Lock()
			accounts.Pool.DefaultModel = "model-" + string(rune('a'+i%26))
			if err := accounts.SavePoolStrictLocked(); err != nil {
				errCh <- err
			}
			accounts.PoolMu.Unlock()
		}
	}()
	go func() {
		defer group.Done()
		for i := 0; i < iterations; i++ {
			accounts.PoolMu.Lock()
			if i%2 == 0 {
				accounts.Pool.ListenHost = "127.0.0.1"
			} else {
				accounts.Pool.ListenHost = "localhost"
			}
			if err := accounts.SavePoolStrictLocked(); err != nil {
				errCh <- err
			}
			accounts.PoolMu.Unlock()
		}
	}()
	go func() {
		defer group.Done()
		for i := 0; i < iterations; i++ {
			status := "active"
			if i%2 == 0 {
				status = "cooldown"
			}
			accounts.SetAccountStatus(account, status)
		}
	}()
	group.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("persist concurrent pool mutation: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var persisted domain.AccountPool
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("persisted pool is torn JSON: %v", err)
	}
	if len(persisted.Accounts) != 1 || persisted.Accounts[0].UsageCount != iterations {
		t.Fatalf("persisted account usage = %+v, want %d requests", persisted.Accounts, iterations)
	}
	if persisted.Accounts[0].TotalTokens != iterations*3 {
		t.Fatalf("persisted token usage = %d, want %d", persisted.Accounts[0].TotalTokens, iterations*3)
	}
}
