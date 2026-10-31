package models

import (
	"routenest/internal/accounts"
	"routenest/internal/domain"
)

// Find returns a copy of the first record for which match reports true. match
// runs under the pool lock: it must be a pure predicate and must not call into
// this package or take the pool lock.
func Find(match func(m domain.Model) bool) (domain.Model, bool) {
	accounts.LoadPool()
	accounts.PoolMu.Lock()
	defer accounts.PoolMu.Unlock()
	for _, m := range accounts.Pool.Models {
		if match(m) {
			return cloneModel(m), true
		}
	}
	return domain.Model{}, false
}

// Where returns copies of every record for which match reports true, in pool
// order. The same rules as Find apply to match.
func Where(match func(m domain.Model) bool) []domain.Model {
	accounts.LoadPool()
	accounts.PoolMu.Lock()
	defer accounts.PoolMu.Unlock()
	var out []domain.Model
	for _, m := range accounts.Pool.Models {
		if match(m) {
			out = append(out, cloneModel(m))
		}
	}
	return out
}

// Read runs fn with the live list under the pool lock. fn must not keep the
// slice and must not call into this package. Prefer Snapshot, Find or Where
// unless a loop needs the whole list without copying it.
func Read(fn func(list []domain.Model)) {
	accounts.LoadPool()
	accounts.PoolMu.Lock()
	defer accounts.PoolMu.Unlock()
	fn(accounts.Pool.Models)
}
