package credentials

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"routenest/internal/domain"
	"routenest/internal/zen"
)

type ZenSessionsSource struct{}

func (ZenSessionsSource) Name() string { return "opencode-sessions" }

func projectZenCredentials(key string, sessions []string) []domain.ProviderCredential {
	out := make([]domain.ProviderCredential, 0, len(sessions)+1)
	if key != "" {
		kind := domain.CredentialAPIKey
		authFlow := "api-key"
		if key == "public" {
			kind = domain.CredentialPublic
			authFlow = "opencode-public"
		}
		out = append(out, domain.ProviderCredential{
			ID: "opencode:public", ProviderIDs: []domain.ProviderInstanceID{"opencode"},
			Kind: kind, AuthFlow: authFlow, Subject: "OpenCode Zen", Status: "active", SecretConfigured: true,
		})
	}
	seen := map[domain.CredentialID]bool{}
	for index, session := range sessions {
		if session == "" {
			continue
		}
		hash := sha256.Sum256([]byte(session))
		credentialID := domain.CredentialID("opencode-session:" + hex.EncodeToString(hash[:8]))
		if seen[credentialID] {
			continue
		}
		seen[credentialID] = true
		out = append(out, domain.ProviderCredential{
			ID:          credentialID,
			ProviderIDs: []domain.ProviderInstanceID{"opencode"}, Kind: domain.CredentialSession,
			AuthFlow: "opencode-session", Subject: fmt.Sprintf("OpenCode session %d", index+1),
			Status: "active", SecretConfigured: true,
		})
	}
	return out
}

func (ZenSessionsSource) Snapshot() []domain.ProviderCredential {
	config := zen.GetConfig()
	return projectZenCredentials(config.Key, config.SessionIDs)
}
