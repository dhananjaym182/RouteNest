package admin

import (
	"log"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/adminapi"
	"routenest/internal/domain"
	"routenest/internal/i18n"
)

// rootAccountStore adapts the package-level account pool to adminapi.AccountStore.
type rootAccountStore struct{}

func (rootAccountStore) List() []*domain.Account { return accounts.ListAccounts() }

func (rootAccountStore) PoolIndex() int { return accounts.LoadPool().CurrentIdx }

func (rootAccountStore) FindByRefreshToken(token string) *domain.Account {
	return accounts.FindAccountByRefreshToken(token)
}

func (rootAccountStore) Add(acc *domain.Account) { accounts.AddAccount(acc) }

func (rootAccountStore) Remove(accountID string) bool { return accounts.RemoveAccount(accountID) }

func validateNewAccountRefreshToken(token string) (adminapi.RefreshedAccount, error) {
	resp, err := accounts.RefreshClineToken(token)
	if err != nil {
		return adminapi.RefreshedAccount{}, err
	}
	return adminapi.RefreshedAccount{
		AccessToken:  resp.Data.AccessToken,
		RefreshToken: resp.Data.RefreshToken,
		ExpiresAt:    accounts.ParseExpiry(resp.Data.ExpiresAt),
	}, nil
}

var adminAccounts = &adminapi.Accounts{
	Store:     rootAccountStore{},
	Refresh:   validateNewAccountRefreshToken,
	Now:       time.Now,
	Translate: i18n.TAPI,
	Logf:      log.Printf,
}
