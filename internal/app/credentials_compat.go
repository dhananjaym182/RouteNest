package app

import (
	"routenest/internal/connections"
	"routenest/internal/credentials"
	"routenest/internal/domain"
	"routenest/internal/providers"
	"routenest/internal/providerstate"
)

func providerCredentialBindings() []credentials.ProviderBinding {
	list := providers.List()
	out := make([]credentials.ProviderBinding, 0, len(list))
	for i := range list {
		provider := &list[i]
		out = append(out, credentials.ProviderBinding{
			ProviderID:       domain.ProviderInstanceID(provider.ID),
			ConnectionIDs:    connections.ProviderConnIDs(provider),
			APIKeyConfigured: provider.APIKey != "",
		})
	}
	return out
}

func configureCredentials() error {
	registry, err := credentials.NewRegistry(
		providerstate.Default(),
		credentials.AccountsSource{},
		credentials.ConnectionsSource{Bindings: providerCredentialBindings},
		credentials.ZenSessionsSource{},
		credentials.APIKeysSource{Bindings: providerCredentialBindings},
	)
	if err != nil {
		return err
	}
	return credentials.Publish(registry)
}
