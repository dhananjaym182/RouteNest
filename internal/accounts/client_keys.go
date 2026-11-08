package accounts

// ClientAuthSnapshot returns a detached snapshot of the configured client API
// keys and the storage state that authorized it. The copy is made while
// holding PoolMu so request authentication never observes a live mutable slice.
func ClientAuthSnapshot() ([]string, error) {
	p := LoadPool()
	PoolMu.Lock()
	defer PoolMu.Unlock()
	if err := PoolStorageErrorLocked(); err != nil {
		return nil, err
	}
	return append([]string(nil), p.Keys...), nil
}

// ClientKeySnapshot returns a detached key list for administrator display. A
// storage failure is represented as an empty list here; public request
// authentication uses ClientAuthSnapshot and fails closed on the error.
func ClientKeySnapshot() []string {
	keys, _ := ClientAuthSnapshot()
	return keys
}

// AddClientKey preserves the existing best-effort persistence policy while
// keeping key mutation and serialization under the account-pool lock.
func AddClientKey(key string) {
	p := LoadPool()
	PoolMu.Lock()
	defer PoolMu.Unlock()
	p.Keys = append(p.Keys, key)
	SavePoolLocked()
}

// DeleteClientKey preserves the existing best-effort persistence policy while
// keeping key mutation and serialization under the account-pool lock.
func DeleteClientKey(key string) {
	p := LoadPool()
	PoolMu.Lock()
	defer PoolMu.Unlock()
	for i, candidate := range p.Keys {
		if candidate == key {
			p.Keys = append(p.Keys[:i], p.Keys[i+1:]...)
			break
		}
	}
	SavePoolLocked()
}
