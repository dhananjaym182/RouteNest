package credentials

import "routenest/internal/domain"

type APIKeysSource struct{ Bindings BindingsProvider }

func (APIKeysSource) Name() string { return "provider-api-keys" }

func projectAPIKeys(bindings []ProviderBinding) []domain.ProviderCredential {
	var out []domain.ProviderCredential
	for _, binding := range bindings {
		if !binding.APIKeyConfigured {
			continue
		}
		out = append(out, domain.ProviderCredential{
			ID:          domain.CredentialID("api-key:" + string(binding.ProviderID)),
			ProviderIDs: []domain.ProviderInstanceID{binding.ProviderID},
			Kind:        domain.CredentialAPIKey, AuthFlow: "api-key", Status: "active", SecretConfigured: true,
		})
	}
	return out
}

func (source APIKeysSource) Snapshot() []domain.ProviderCredential {
	if source.Bindings == nil {
		return nil
	}
	return projectAPIKeys(source.Bindings())
}
