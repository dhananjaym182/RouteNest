// Package models owns the live model catalogue: the Models list persisted in
// .routenest-accounts.json. Every reader and writer of that list goes through
// this package, so locking, persistence, copies and default-model cleanup have
// one implementation.
//
// The storage schema is unchanged. The list is still domain.AccountPool.Models
// in the accounts file, and the JSON tags of domain.Model are unchanged.
package models

import (
	"routenest/internal/accounts"
	"routenest/internal/domain"
)

// Source values are persisted on each record. They must not change.
const (
	// SourceRemote marks records synced from Cline's recommended-model list.
	SourceRemote = "remote"
	// SourceZen marks records synced from the OpenCode zen catalogue.
	SourceZen = "zen"
	// SourceSeed is the legacy zen seed value, still treated as zen-owned.
	SourceSeed = "seed"
	// SourceProvider marks records projected from a custom provider's ModelIDs.
	SourceProvider = "provider"
	// SourceCustom is the empty source: records added by an administrator or
	// built in.
	SourceCustom = ""
)

// Cost values. Records carry "free" or "pass".
const (
	CostFree = "free"
	CostPass = "pass"
)

// Snapshot returns a deep copy of the live model list. Callers may modify the
// result freely; it never aliases the pool.
func Snapshot() []domain.Model {
	accounts.LoadPool()
	accounts.PoolMu.Lock()
	defer accounts.PoolMu.Unlock()
	return copyModels(accounts.Pool.Models)
}

// Edit runs fn on the live pool while holding the pool lock, then persists the
// pool when fn reports a change. It is the single write convention: fn must not
// call other functions in this package or the accounts lock deadlocks.
func Edit(fn func(p *domain.AccountPool) bool) bool {
	accounts.LoadPool()
	accounts.PoolMu.Lock()
	changed := fn(accounts.Pool)
	if changed {
		accounts.SavePoolLocked()
	}
	accounts.PoolMu.Unlock()
	return changed
}

// copyModels deep-copies each record, including its slices.
func copyModels(in []domain.Model) []domain.Model {
	out := make([]domain.Model, len(in))
	for i, m := range in {
		out[i] = cloneModel(m)
	}
	return out
}

func cloneModel(m domain.Model) domain.Model {
	m.ThinkingLevels = append([]string(nil), m.ThinkingLevels...)
	m.InputModalities = append([]string(nil), m.InputModalities...)
	m.OutputModalities = append([]string(nil), m.OutputModalities...)
	return m
}
