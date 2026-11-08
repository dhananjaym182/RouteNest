// Package accounts persists the Cline account pool. It performs no upstream
// calls and no dispatch decisions.
package accounts

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"routenest/internal/domain"
	"routenest/internal/platform"
)

// FileName is the data file holding the account pool.
const FileName = ".routenest-accounts.json"

var ErrInvalidDocument = errors.New("accounts: invalid account-pool document")

// Empty returns a pool with initialized slices so callers never see nils.
func Empty() *domain.AccountPool {
	return &domain.AccountPool{Accounts: []*domain.Account{}, Keys: []string{}, Models: []domain.Model{}}
}

// Load reads path. Only a missing file yields an empty pool and no error.
// Unreadable and invalid files return an error so security-sensitive callers
// cannot mistake unavailable client-key authority for intentional anonymous mode.
func Load(path string) (*domain.AccountPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Empty(), nil
		}
		return nil, err
	}
	var p *domain.AccountPool
	if err := json.Unmarshal(data, &p); err != nil {
		return Empty(), err
	}
	if p == nil {
		return Empty(), fmt.Errorf("%w: top-level value must be an object", ErrInvalidDocument)
	}
	normalize(p)
	return p, nil
}

// Save writes the pool atomically.
func Save(path string, p *domain.AccountPool) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return platform.WriteFileAtomic(path, data, 0600)
}

func SaveDurable(path string, p *domain.AccountPool) error {
	return SaveDurableWithOptions(path, p, platform.AtomicWriteOptions{})
}

func SaveDurableWithOptions(path string, p *domain.AccountPool, options platform.AtomicWriteOptions) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return platform.WriteFileAtomicDurableWithOptions(path, data, 0600, options)
}

func normalize(p *domain.AccountPool) {
	if p.Accounts == nil {
		p.Accounts = []*domain.Account{}
	}
	if p.Keys == nil {
		p.Keys = []string{}
	}
	if p.Models == nil {
		p.Models = []domain.Model{}
	}
}
