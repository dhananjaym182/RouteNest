package accounts

import (
	"sync"
	"time"

	"cline-go-proxy/internal/config"
)

// Cline account session affinity: binds a conversation (first user message
// hash, same key as provider sticky pins) to one pool account inside the TTL
// window, so multi-turn requests keep hitting the same upstream account
// (warm per-account context, steadier quota burn). Orthogonal to provider
// pinning: a provider-pinned model call never consults this. Disabled unless
// SessionAffinity.Enabled.

type accountAffinityStore struct {
	mu    sync.Mutex
	binds map[string]accountAffinityBind
}

type accountAffinityBind struct {
	accountID string
	expires   time.Time
}

var accountAffinity = &accountAffinityStore{binds: map[string]accountAffinityBind{}}

var (
	cfgMu        sync.RWMutex
	cfgProvider  func() *config.Proxy
	affinityClsf func(err error) (statusCode int, unavailable bool)
)

// SetProxyConfigProvider injects the live proxy config accessor. The root
// package owns the config store in this phase; accounts only reads the
// strategy and session-affinity policy from it.
func SetProxyConfigProvider(f func() *config.Proxy) {
	cfgMu.Lock()
	cfgProvider = f
	cfgMu.Unlock()
}

// SetAffinityErrorClassifier injects Cline error classification. The concrete
// error types still live in the root package, so accounts asks for the two
// facts it needs instead of importing them.
func SetAffinityErrorClassifier(f func(err error) (statusCode int, unavailable bool)) {
	cfgMu.Lock()
	affinityClsf = f
	cfgMu.Unlock()
}

func currentConfig() *config.Proxy {
	cfgMu.RLock()
	f := cfgProvider
	cfgMu.RUnlock()
	if f == nil {
		return config.DefaultProxy()
	}
	return f()
}

func classifyAffinityError(err error) (int, bool) {
	cfgMu.RLock()
	f := affinityClsf
	cfgMu.RUnlock()
	if f == nil {
		return 0, false
	}
	return f(err)
}

// enabled reports whether affinity applies to this strategy config.
func (s *accountAffinityStore) enabled(cfg *config.Proxy) bool {
	return cfg != nil && cfg.SessionAffinity.Enabled
}

// get returns the bound, still-eligible account for a session, or nil.
// Eligibility mirrors the pick functions: account active and the model not
// in that account's cooldown. A stale/invalidated binding is dropped.
func (s *accountAffinityStore) get(sk, model string, now time.Time) *Account {
	if sk == "" {
		return nil
	}
	cfg := currentConfig()
	if !s.enabled(cfg) {
		return nil
	}
	s.mu.Lock()
	bind, ok := s.binds[sk]
	s.mu.Unlock()
	if !ok || now.After(bind.expires) {
		return nil
	}
	acc := GetAccountByID(bind.accountID)
	if acc == nil || acc.Status != "active" {
		s.clear(sk)
		return nil
	}
	if model != "" {
		if until, cool := acc.ModelCooldowns[model]; cool && !now.After(until) {
			return nil // bound account cooling: fall back to normal pick
		}
	}
	return acc
}

// bind pins sk→account for the configured TTL. Re-binding refreshes the TTL.
func (s *accountAffinityStore) bind(sk string, acc *Account) {
	if sk == "" || acc == nil || acc.AccountID == "" {
		return
	}
	cfg := currentConfig()
	if !s.enabled(cfg) {
		return
	}
	s.mu.Lock()
	s.binds[sk] = accountAffinityBind{accountID: acc.AccountID, expires: time.Now().Add(cfg.SessionAffinity.TTL())}
	s.mu.Unlock()
}

func (s *accountAffinityStore) clear(sk string) {
	if sk == "" {
		return
	}
	s.mu.Lock()
	delete(s.binds, sk)
	s.mu.Unlock()
}

// rebindStatuses reports the account-level failures that invalidate a binding:
// auth/entitlement problems and model rate limits on the bound account.
// Transient 5xx keep the binding (same account is still the right one).
func accountAffinityRebindStatus(status int) bool {
	switch status {
	case 401, 402, 403, 404, 429:
		return true
	}
	return false
}

// pickAffinityAccount returns the session-bound account when eligible.
// ok=false means affinity does not apply (caller falls back to its pick).
func PickAffinityAccount(sk, model string) (*Account, bool) {
	if acc := accountAffinity.get(sk, model, time.Now()); acc != nil {
		return acc, true
	}
	return nil, false
}

// observeAffinityOutcome updates the binding after an attempt on acc:
// success refreshes it, a rebind-class API error or account-unavailable
// clears it, anything else leaves it untouched.
func ObserveAffinityOutcome(sk string, acc *Account, err error) {
	if sk == "" || acc == nil {
		return
	}
	if err == nil {
		accountAffinity.bind(sk, acc)
		return
	}
	if code, unavailable := classifyAffinityError(err); unavailable {
		accountAffinity.clear(sk)
	} else if code != 0 && accountAffinityRebindStatus(code) {
		accountAffinity.clear(sk)
	}
}
