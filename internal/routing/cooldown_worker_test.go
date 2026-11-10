package routing

import (
	"context"
	"sync"
	"testing"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/domain"
)

func waitCooldownWorker(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cooldown worker did not stop")
	}
}

func TestCooldownRecoveryWithContextStopsWithoutTick(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := StartCooldownRecoveryWithContext(ctx)
	cancel()
	waitCooldownWorker(t, done)
}

func TestCooldownRecoveryCanceledQueuedTickSkipsPoolRead(t *testing.T) {
	for range 100 {
		ctx, cancel := context.WithCancel(context.Background())
		ticks := make(chan time.Time, 1)
		ticks <- time.Now()
		cancel()
		reads, probes := 0, 0
		runCooldownRecovery(ctx, ticks, func() *domain.AccountPool {
			reads++
			return &domain.AccountPool{Accounts: []*domain.Account{{Status: "cooldown"}}}
		}, func(*domain.Account) AccountTestResult {
			probes++
			return AccountTestResult{}
		})
		if reads != 0 || probes != 0 {
			t.Fatalf("canceled tick accessed pool: reads=%d probes=%d", reads, probes)
		}
	}
}

func TestCooldownRecoveryJoinedWorkerDoesNotReadReplacedPool(t *testing.T) {
	oldPool := accounts.Pool
	accounts.Pool = &domain.AccountPool{Accounts: []*domain.Account{{Email: "cooldown@example.com", Status: "cooldown"}}}
	t.Cleanup(func() { accounts.Pool = oldPool })
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time, 1)
	probed := make(chan struct{})
	done := make(chan struct{})
	reads := 0
	go func() {
		defer close(done)
		runCooldownRecovery(ctx, ticks, func() *domain.AccountPool {
			reads++
			return accounts.Pool
		}, func(*domain.Account) AccountTestResult {
			close(probed)
			return AccountTestResult{OK: true}
		})
	}()
	t.Cleanup(func() {
		cancel()
		waitCooldownWorker(t, done)
	})
	ticks <- time.Now()
	select {
	case <-probed:
	case <-time.After(time.Second):
		t.Fatal("cooldown tick did not inspect the account")
	}
	cancel()
	waitCooldownWorker(t, done)
	accounts.Pool = &domain.AccountPool{}
	ticks <- time.Now()
	close(ticks)
	if reads != 1 || len(ticks) != 1 {
		t.Fatalf("joined worker consumed a later tick: reads=%d pending ticks=%d", reads, len(ticks))
	}
}

func TestCooldownRecoveryCancellationDuringPoolLoadSkipsAccounts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time, 1)
	ticks <- time.Now()
	probes := 0
	runCooldownRecovery(ctx, ticks, func() *domain.AccountPool {
		cancel()
		return &domain.AccountPool{Accounts: []*domain.Account{nil}}
	}, func(*domain.Account) AccountTestResult {
		probes++
		return AccountTestResult{}
	})
	if probes != 0 {
		t.Fatalf("canceled pool load probed %d accounts", probes)
	}
}

func TestCooldownRecoveryCancellationWaitsForProbeAndSkipsNext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time, 1)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	pool := &domain.AccountPool{Accounts: []*domain.Account{
		{Email: "first@example.com", Status: "cooldown"},
		{Email: "second@example.com", Status: "cooldown"},
	}}
	reads, probes := 0, 0
	go func() {
		defer close(done)
		runCooldownRecovery(ctx, ticks, func() *domain.AccountPool {
			reads++
			return pool
		}, func(*domain.Account) AccountTestResult {
			probes++
			if probes == 1 {
				close(entered)
				<-release
			}
			return AccountTestResult{}
		})
	}()
	t.Cleanup(func() {
		cancel()
		unblock()
		waitCooldownWorker(t, done)
	})
	ticks <- time.Now()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first cooldown probe did not start")
	}
	ticks <- time.Now()
	cancel()
	select {
	case <-done:
		t.Fatal("worker finished before its in-flight probe returned")
	default:
	}
	unblock()
	waitCooldownWorker(t, done)
	if reads != 1 || probes != 1 {
		t.Fatalf("canceled worker performed later work: reads=%d probes=%d", reads, probes)
	}
}

func TestCooldownRecoveryPreservesEligibleAccountSelection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time, 1)
	ticks <- time.Now()
	close(ticks)
	pool := &domain.AccountPool{Accounts: []*domain.Account{
		{Email: "active@example.com", Status: "active"},
		{Email: "future@example.com", Status: "cooldown", CooldownUntil: time.Now().Add(time.Hour)},
		{Email: "expired@example.com", Status: "cooldown", CooldownUntil: time.Now().Add(-time.Hour)},
		{Email: "unknown@example.com", Status: "cooldown"},
	}}
	reads := 0
	var probed []string
	runCooldownRecovery(ctx, ticks, func() *domain.AccountPool {
		reads++
		return pool
	}, func(account *domain.Account) AccountTestResult {
		probed = append(probed, account.Email)
		return AccountTestResult{OK: true}
	})
	if reads != 1 || len(probed) != 2 || probed[0] != "expired@example.com" || probed[1] != "unknown@example.com" {
		t.Fatalf("cooldown selection changed: reads=%d probes=%v", reads, probed)
	}
}
