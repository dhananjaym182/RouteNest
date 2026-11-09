package admin

import (
	"log"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/adminapi"
	"routenest/internal/domain"
	"routenest/internal/i18n"
	"routenest/internal/routing"
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
	all := append([]*domain.Account(nil), p.Accounts...)
	accounts.PoolMu.Unlock()
	for _, a := range all {
		if err := accounts.RefreshAccountToken(a); err != nil {
			log.Printf("Refresh failed for %s: %v", a.Email, err)
		}
	}
}

func clearAccountPool() error {
	return accounts.DeleteAllAccountsStrict()
}

var adminAccountOps = &adminapi.AccountOps{
	All:          snapshotAccounts,
	Get:          accounts.GetAccountByID,
	DoRefreshAll: refreshAllAccounts,
	Refresh:      accounts.RefreshAccountToken,
	ClearAll:     clearAccountPool,
	Probe: func(acc *domain.Account) any {
		result := routing.TestAccount(acc)
		if result.OK {
			markOnboardingProviderVerified(onboardingClineVerificationKey(acc.AccountID))
		}
		return result
	},
	OpenBrowser: accounts.OpenBrowser,
	Now:         time.Now,
	Translate:   i18n.TAPI,
}
