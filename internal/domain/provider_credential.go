package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

type CredentialKind string

const (
	CredentialAPIKey        CredentialKind = "api_key"
	CredentialOAuthAccount  CredentialKind = "oauth_account"
	CredentialRefreshToken  CredentialKind = "refresh_token"
	CredentialImportedToken CredentialKind = "imported_token"
	CredentialSession       CredentialKind = "session"
	CredentialPublic        CredentialKind = "public"
)

func (kind CredentialKind) Valid() bool {
	switch kind {
	case CredentialAPIKey, CredentialOAuthAccount, CredentialRefreshToken,
		CredentialImportedToken, CredentialSession, CredentialPublic:
		return true
	default:
		return false
	}
}

// ProviderCredential is a redacted global view over legacy account,
// connection, API-key and session stores. It never contains secret material.
type ProviderCredential struct {
	ID               CredentialID         `json:"id"`
	ProviderIDs      []ProviderInstanceID `json:"providerIds,omitempty"`
	Kind             CredentialKind       `json:"kind"`
	AuthFlow         string               `json:"authFlow,omitempty"`
	Subject          string               `json:"subject,omitempty"`
	Status           string               `json:"status"`
	ExpiresAt        time.Time            `json:"expiresAt,omitempty"`
	CooldownUntil    time.Time            `json:"cooldownUntil,omitempty"`
	ModelCooldowns   map[string]time.Time `json:"modelCooldowns,omitempty"`
	Refreshable      bool                 `json:"refreshable,omitempty"`
	SecretConfigured bool                 `json:"secretConfigured"`
	UsageCount       int64                `json:"usageCount"`
	PromptTokens     int64                `json:"promptTokens"`
	CompletionTokens int64                `json:"completionTokens"`
	TotalTokens      int64                `json:"totalTokens"`
	CachedTokens     int64                `json:"cachedTokens"`
}

// ClineCredentialID is the canonical global identity for a Cline account. Old
// account records without AccountID use a redacted email fingerprint.
func ClineCredentialID(accountID, email string) CredentialID {
	if accountID != "" {
		return CredentialID("cline:" + accountID)
	}
	hash := sha256.Sum256([]byte(email))
	return CredentialID("cline:legacy:" + hex.EncodeToString(hash[:8]))
}

func (credential ProviderCredential) Valid() bool {
	if !credential.ID.Valid() || !credential.Kind.Valid() || credential.Status == "" {
		return false
	}
	seen := map[ProviderInstanceID]bool{}
	for _, providerID := range credential.ProviderIDs {
		if !providerID.Valid() || seen[providerID] {
			return false
		}
		seen[providerID] = true
	}
	return true
}
