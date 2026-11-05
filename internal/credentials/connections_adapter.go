package credentials

import (
	"sort"
	"time"

	"routenest/internal/connections"
	"routenest/internal/domain"
)

type ProviderBinding struct {
	ProviderID       domain.ProviderInstanceID
	ConnectionIDs    []string
	APIKeyConfigured bool
}

type BindingsProvider func() []ProviderBinding

type ConnectionsSource struct{ Bindings BindingsProvider }

func (ConnectionsSource) Name() string { return "provider-connections" }

func projectConnections(snapshots []connections.CredentialSnapshot, bindings []ProviderBinding) []domain.ProviderCredential {
	providersByConnection := map[string][]domain.ProviderInstanceID{}
	for _, binding := range bindings {
		for _, connectionID := range binding.ConnectionIDs {
			providersByConnection[connectionID] = append(providersByConnection[connectionID], binding.ProviderID)
		}
	}
	out := make([]domain.ProviderCredential, 0, len(snapshots))
	for _, snapshot := range snapshots {
		connection := snapshot.Connection
		providerIDs := append([]domain.ProviderInstanceID(nil), providersByConnection[connection.ID]...)
		sort.Slice(providerIDs, func(i, j int) bool { return providerIDs[i] < providerIDs[j] })
		kind := domain.CredentialImportedToken
		if connection.AuthType == "device_code" {
			kind = domain.CredentialOAuthAccount
		}
		var expiresAt time.Time
		if connection.ExpiresAt > 0 {
			expiresAt = time.UnixMilli(connection.ExpiresAt)
		}
		status := connection.Status
		if status == "" {
			status = "active"
		}
		out = append(out, domain.ProviderCredential{
			ID:          domain.CredentialID("connection:" + connection.ID),
			ProviderIDs: providerIDs, Kind: kind, AuthFlow: connection.Provider,
			Subject: connection.Email, Status: status, ExpiresAt: expiresAt,
			CooldownUntil: snapshot.CooldownUntil, Refreshable: snapshot.Refreshable,
			SecretConfigured: true, UsageCount: connection.UsageCount,
			PromptTokens: connection.PromptTokens, CompletionTokens: connection.CompletionTokens,
			TotalTokens: connection.TotalTokens, CachedTokens: connection.CachedTokens,
		})
	}
	return out
}

func (source ConnectionsSource) Snapshot() []domain.ProviderCredential {
	var bindings []ProviderBinding
	if source.Bindings != nil {
		bindings = source.Bindings()
	}
	return projectConnections(connections.ListCredentialSnapshots(), bindings)
}
