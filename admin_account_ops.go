package main

import (
	"cline-go-proxy/internal/routing"
	"log"
	"time"

	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/adminapi"
	"cline-go-proxy/internal/domain"
	"cline-go-proxy/internal/i18n"
)

func snapshotAccounts() []*domain.Account {
	p := accounts.LoadPool()
	accounts.PoolMu.Lock()
	out := make([]*domain.Account, len(p.Accounts))
	copy(out, p.Accounts)
	accounts.PoolMu.Unlock()
	return out
}

func refreshAllAccounts() {
	p := accounts.LoadPool()
	accounts.PoolMu.Lock()
	for _, a := range p.Accounts {
		if err := accounts.RefreshAccountToken(a); err != nil {
			log.Printf("Refresh failed for %s: %v", a.Email, err)
		}
	}
	accounts.PoolMu.Unlock()
}

func clearAccountPool() {
	accounts.PoolMu.Lock()
	accounts.Pool = &AccountPool{Accounts: []*Account{}, Keys: []string{}}
	accounts.PoolMu.Unlock()
	accounts.SavePool()
}

var adminAccountOps = &adminapi.AccountOps{
	All:          snapshotAccounts,
	Get:          accounts.GetAccountByID,
	DoRefreshAll: refreshAllAccounts,
	Refresh:      accounts.RefreshAccountToken,
	ClearAll:     clearAccountPool,
	Probe:        func(acc *domain.Account) any { return routing.TestAccount(acc) },
	OpenBrowser:  accounts.OpenBrowser,
	Now:          time.Now,
	Translate:    i18n.TAPI,
}
