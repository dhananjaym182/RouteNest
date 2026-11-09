package accounts

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"routenest/internal/platform"
	poolstore "routenest/internal/store/accounts"
)

const clientKeyRandomBytes = 32

var (
	ErrClientKeyRequired                   = errors.New("client API key is required")
	ErrClientKeyNotFound                   = errors.New("client API key not found")
	ErrClientKeyGeneration                 = errors.New("client API key generation failed")
	ErrFinalClientKeyConfirmationRequired  = errors.New("final client API key deletion requires explicit confirmation")
	ErrFinalClientKeyAuthorizationRequired = errors.New("final client API key deletion requires elevated authorization")
)

type ClientKeyDeleteResult struct {
	DeletedFinalKey bool
}

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

// ClientKeySnapshot returns a detached key list for administrator display.
// Storage failures are returned instead of being mistaken for an intentional
// empty-key configuration.
func ClientKeySnapshot() ([]string, error) {
	return ClientAuthSnapshot()
}

// GenerateClientKey creates a key with 256 bits of operating-system entropy,
// checks it against the authoritative persisted snapshot, and publishes it only
// after durable persistence succeeds.
func GenerateClientKey() (string, error) {
	return generateClientKey(rand.Reader, poolstore.SaveDurable)
}

func generateClientKey(random io.Reader, save func(string, *AccountPool) error) (string, error) {
	if random == nil {
		return "", fmt.Errorf("%w: random source unavailable", ErrClientKeyGeneration)
	}
	PoolMu.Lock()
	defer PoolMu.Unlock()

	p, err := loadClientKeyMutationPoolLocked()
	if err != nil {
		return "", err
	}
	existing := make(map[string]struct{}, len(p.Keys))
	for _, key := range p.Keys {
		existing[key] = struct{}{}
	}
	var key string
	for attempts := 0; attempts < 128; attempts++ {
		raw := make([]byte, clientKeyRandomBytes)
		if _, err := io.ReadFull(random, raw); err != nil {
			return "", fmt.Errorf("%w: %v", ErrClientKeyGeneration, err)
		}
		candidate := "cline_" + base64.RawURLEncoding.EncodeToString(raw)
		if _, collision := existing[candidate]; !collision {
			key = candidate
			break
		}
	}
	if key == "" {
		return "", fmt.Errorf("%w: collision limit exceeded", ErrClientKeyGeneration)
	}

	next := clonePool(p)
	next.Keys = append(next.Keys, key)
	if err := persistClientKeyMutationLocked(next, save); err != nil {
		return "", err
	}
	return key, nil
}

// DeleteClientKey revokes every occurrence of key. Legacy duplicate entries
// must not leave the same credential valid after a successful revocation.
// Deleting the final effective key requires both an explicit confirmation and
// elevated authorization, while preserving the intentional empty-key policy.
func DeleteClientKey(key string, confirmDisableAuthentication, elevated bool) (ClientKeyDeleteResult, error) {
	return deleteClientKey(key, confirmDisableAuthentication, elevated, poolstore.SaveDurable)
}

func deleteClientKey(key string, confirmDisableAuthentication, elevated bool, save func(string, *AccountPool) error) (ClientKeyDeleteResult, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return ClientKeyDeleteResult{}, ErrClientKeyRequired
	}
	PoolMu.Lock()
	defer PoolMu.Unlock()

	p, err := loadClientKeyMutationPoolLocked()
	if err != nil {
		return ClientKeyDeleteResult{}, err
	}
	nextKeys := make([]string, 0, len(p.Keys))
	found := false
	for _, candidate := range p.Keys {
		if candidate == key {
			found = true
			continue
		}
		nextKeys = append(nextKeys, candidate)
	}
	if !found {
		return ClientKeyDeleteResult{}, ErrClientKeyNotFound
	}
	deletingFinal := len(nextKeys) == 0
	if deletingFinal && !confirmDisableAuthentication {
		return ClientKeyDeleteResult{}, ErrFinalClientKeyConfirmationRequired
	}
	if deletingFinal && !elevated {
		return ClientKeyDeleteResult{}, ErrFinalClientKeyAuthorizationRequired
	}

	next := clonePool(p)
	next.Keys = nextKeys
	if err := persistClientKeyMutationLocked(next, save); err != nil {
		return ClientKeyDeleteResult{DeletedFinalKey: deletingFinal}, err
	}
	return ClientKeyDeleteResult{DeletedFinalKey: deletingFinal}, nil
}

func loadClientKeyMutationPoolLocked() (*AccountPool, error) {
	ensurePoolPath()
	p, _, err := loadPoolFromDisk(PoolPath)
	if err != nil {
		poolLoadErr = err
		poolLoadErrPath = PoolPath
		return nil, err
	}
	return p, nil
}

func persistClientKeyMutationLocked(next *AccountPool, save func(string, *AccountPool) error) error {
	if save == nil {
		return fmt.Errorf("persist client API keys: storage unavailable")
	}
	if err := save(PoolPath, next); err != nil {
		if platform.AtomicWriteMayHaveReplaced(err) {
			persisted, reloadErr := poolstore.Load(PoolPath)
			if reloadErr != nil {
				poolLoadErr = errors.Join(err, reloadErr)
				poolLoadErrPath = PoolPath
				Pool = poolstore.Empty()
				return poolLoadErr
			}
			publishPoolLocked(persisted)
			poolLoadErr = nil
			poolLoadErrPath = ""
		}
		return err
	}
	publishPoolLocked(next)
	poolLoadErr = nil
	poolLoadErrPath = ""
	return nil
}
