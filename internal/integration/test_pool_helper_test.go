package integration

import "routenest/internal/accounts"

// getTestPool reads the live pool under PoolMu. Background goroutines started by
// the protocol test server read the pool under the same lock.
func getTestPool() *AccountPool {
	accounts.PoolMu.Lock()
	defer accounts.PoolMu.Unlock()
	return accounts.Pool
}

// setTestPool replaces the live pool under PoolMu and returns the previous one.
func setTestPool(p *AccountPool) *AccountPool {
	accounts.PoolMu.Lock()
	defer accounts.PoolMu.Unlock()
	old := accounts.Pool
	accounts.Pool = p
	return old
}
