package accounts

// Cline 账号会话亲和测试：绑定/命中/过期/冷却回退/失效解绑，以及
// ObserveAffinityOutcome 的成功刷新与 401/429 清绑、5xx 保留语义。

import (
	"errors"
	"testing"
	"time"

	"cline-go-proxy/internal/config"
)

// affinityTestError is the stand-in for the root package's Cline API error;
// the classifier injected below is what ObserveAffinityOutcome consults.
type affinityTestError struct {
	status int
}

func (e *affinityTestError) Error() string { return "test upstream error" }

type affinityUnavailableError struct{}

func (e *affinityUnavailableError) Error() string { return "account unavailable" }

func installAffinityConfig(t *testing.T, enabled bool, ttl time.Duration) {
	t.Helper()
	cfg := config.DefaultProxy()
	cfg.SessionAffinity = config.SessionAffinityPolicy{Enabled: enabled, TTLSeconds: int(ttl.Seconds())}
	cfg.Normalize()
	SetProxyConfigProvider(func() *config.Proxy { return cfg })
	SetAffinityErrorClassifier(func(err error) (int, bool) {
		var apiErr *affinityTestError
		if errors.As(err, &apiErr) {
			return apiErr.status, false
		}
		var accErr *affinityUnavailableError
		if errors.As(err, &accErr) {
			return 0, true
		}
		return 0, false
	})
	t.Cleanup(func() {
		SetProxyConfigProvider(nil)
		SetAffinityErrorClassifier(nil)
	})
	accountAffinity.mu.Lock()
	accountAffinity.binds = map[string]accountAffinityBind{}
	accountAffinity.mu.Unlock()
}

func affinityAccount(id, email, status string) *Account {
	return &Account{AccountID: id, Email: email, Status: status, ModelStats: map[string]*ModelStat{}, ModelCooldowns: map[string]time.Time{}}
}

func installAffinityPool(t *testing.T, models []Model, accs ...*Account) {
	t.Helper()
	PoolMu.Lock()
	old := Pool
	Pool = &AccountPool{Accounts: accs, Models: models}
	PoolMu.Unlock()
	t.Cleanup(func() {
		PoolMu.Lock()
		Pool = old
		PoolMu.Unlock()
	})
}

func TestAccountAffinityBindGetExpiry(t *testing.T) {
	installAffinityConfig(t, true, time.Hour)
	acc1 := affinityAccount("a1", "one@x.io", "active")
	acc2 := affinityAccount("a2", "two@x.io", "active")
	installAffinityPool(t, []Model{{ID: "m1"}}, acc1, acc2)

	accountAffinity.bind("sk-1", acc1)
	if got := accountAffinity.get("sk-1", "m1", time.Now()); got == nil || got.AccountID != "a1" {
		t.Fatalf("bound account lookup = %+v, want a1", got)
	}
	// 冷却中的绑定账号 → 未命中（融入常规轮转）
	acc1.ModelCooldowns["m1"] = time.Now().Add(time.Minute)
	if got := accountAffinity.get("sk-1", "m1", time.Now()); got != nil {
		t.Fatal("cooling bound account must not be returned")
	}
	// 冷却到期后恢复命中
	acc1.ModelCooldowns["m1"] = time.Now().Add(-time.Second)
	if got := accountAffinity.get("sk-1", "m1", time.Now()); got == nil || got.AccountID != "a1" {
		t.Fatalf("expired cooldown lookup = %+v, want a1", got)
	}
	// TTL 到期 → 未命中
	if got := accountAffinity.get("sk-1", "m1", time.Now().Add(2*time.Hour)); got != nil {
		t.Fatal("expired binding must not match")
	}
	// 账号消失/非 active → 未命中
	accountAffinity.bind("sk-1", acc1)
	acc1.Status = "expired"
	if got := accountAffinity.get("sk-1", "m1", time.Now()); got != nil {
		t.Fatal("inactive bound account must not be returned")
	}
	acc1.Status = "active"
	if got := accountAffinity.get("sk-2", "m1", time.Now()); got != nil {
		t.Fatal("unknown session key must not match")
	}
	_ = acc2
}

func TestAccountAffinityDisabledNoop(t *testing.T) {
	installAffinityConfig(t, false, time.Hour)
	acc := affinityAccount("a1", "one@x.io", "active")
	installAffinityPool(t, []Model{{ID: "m1"}}, acc)

	accountAffinity.bind("sk-1", acc)
	if got := accountAffinity.get("sk-1", "m1", time.Now()); got != nil {
		t.Fatal("disabled affinity must not bind")
	}
}

func TestObserveAffinityOutcome(t *testing.T) {
	installAffinityConfig(t, true, time.Hour)
	acc := affinityAccount("a1", "one@x.io", "active")
	installAffinityPool(t, []Model{{ID: "m1"}}, acc)

	ObserveAffinityOutcome("sk-1", acc, nil)
	if _, pinned := PickAffinityAccount("sk-1", "m1"); !pinned {
		t.Fatal("success must bind the session")
	}
	// 瞬时 5xx：保留绑定
	ObserveAffinityOutcome("sk-1", acc, &affinityTestError{status: 500})
	if _, pinned := PickAffinityAccount("sk-1", "m1"); !pinned {
		t.Fatal("transient error must keep binding")
	}
	// 401：解绑
	ObserveAffinityOutcome("sk-1", acc, &affinityTestError{status: 401})
	if _, pinned := PickAffinityAccount("sk-1", "m1"); pinned {
		t.Fatal("401 must clear binding")
	}
	// 重新绑定后 429：解绑
	ObserveAffinityOutcome("sk-1", acc, nil)
	ObserveAffinityOutcome("sk-1", acc, &affinityTestError{status: 429})
	if _, pinned := PickAffinityAccount("sk-1", "m1"); pinned {
		t.Fatal("429 must clear binding")
	}
	// account-unavailable：解绑
	ObserveAffinityOutcome("sk-1", acc, nil)
	ObserveAffinityOutcome("sk-1", acc, &affinityUnavailableError{})
	if _, pinned := PickAffinityAccount("sk-1", "m1"); pinned {
		t.Fatal("account-unavailable must clear binding")
	}
	// 空 session key / nil 账号：安全无操作
	ObserveAffinityOutcome("", acc, nil)
	ObserveAffinityOutcome("sk-1", nil, nil)
}
