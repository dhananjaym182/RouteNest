package credentials

import (
	"crypto/sha256"
	"encoding/hex"

	"routenest/internal/accounts"
	"routenest/internal/domain"
)

type AccountsSource struct{}

func (AccountsSource) Name() string { return "cline-accounts" }

func clineCredentialID(accountID, email string) domain.CredentialID {
	if accountID != "" {
		return domain.CredentialID("cline:" + accountID)
	}
	hash := sha256.Sum256([]byte(email))
	return domain.CredentialID("cline:legacy:" + hex.EncodeToString(hash[:8]))
}

func projectAccounts(list []*domain.Account) []domain.ProviderCredential {
	out := make([]domain.ProviderCredential, 0, len(list))
	for _, account := range list {
		if account == nil {
			continue
		}
		status := account.Status
		if status == "" {
			status = "active"
		}
		out = append(out, domain.ProviderCredential{
			ID:          clineCredentialID(account.AccountID, account.Email),
			ProviderIDs: []domain.ProviderInstanceID{"cline"},
			Kind:        domain.CredentialOAuthAccount, AuthFlow: "cline-workos",
			Subject: account.Email, Status: status,
			CooldownUntil:  account.CooldownUntil,
			ModelCooldowns: account.ModelCooldowns,
			Refreshable:    true, SecretConfigured: true,
			UsageCount: account.UsageCount, PromptTokens: account.PromptTokens,
			CompletionTokens: account.CompletionTokens, TotalTokens: account.TotalTokens,
			CachedTokens: account.CachedTokens,
		})
	}
	return out
}

func (AccountsSource) Snapshot() []domain.ProviderCredential {
	return projectAccounts(accounts.ListAccounts())
}
