package credentials

import (
	"strings"
	"testing"
	"time"

	"routenest/internal/connections"
	"routenest/internal/domain"
)

func TestProjectAccountsRedactsAndKeepsCooldownMetadata(t *testing.T) {
	until := time.Now().Add(time.Hour)
	got := projectAccounts([]*domain.Account{{
		AccountID: "a1", Email: "a@example.com", RefreshToken: "secret", AccessToken: "secret-access",
		Status: "active", CooldownUntil: until, ModelCooldowns: map[string]time.Time{"m": until}, TotalTokens: 42,
	}})
	if len(got) != 1 || got[0].ID != "cline:a1" || got[0].ProviderIDs[0] != "cline" || !got[0].Refreshable || got[0].TotalTokens != 42 {
		t.Fatalf("projection = %+v", got)
	}
	if got[0].ModelCooldowns["m"] != until {
		t.Fatalf("model cooldown lost: %+v", got[0])
	}
}

func TestProjectConnectionsSupportsSharedProviderBindings(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	got := projectConnections([]connections.CredentialSnapshot{{
		Connection: connections.Connection{
			ID: "c1", Provider: "github", AuthType: "device_code", Email: "a@example.com",
			AccessToken: "", RefreshToken: "", Status: "active", ExpiresAt: expires.UnixMilli(),
		},
		Refreshable: true, CooldownUntil: expires,
	}}, []ProviderBinding{
		{ProviderID: "p2", ConnectionIDs: []string{"c1"}},
		{ProviderID: "p1", ConnectionIDs: []string{"c1"}},
	})
	if len(got) != 1 || got[0].ID != "connection:c1" || got[0].Kind != domain.CredentialOAuthAccount || !got[0].Refreshable {
		t.Fatalf("projection = %+v", got)
	}
	if strings.Join([]string{string(got[0].ProviderIDs[0]), string(got[0].ProviderIDs[1])}, ",") != "p1,p2" {
		t.Fatalf("provider bindings = %v", got[0].ProviderIDs)
	}
}

func TestProjectZenCredentialsHashesSessionSecrets(t *testing.T) {
	const session = "ses_super-secret-session"
	got := projectZenCredentials("public", []string{session})
	if len(got) != 2 || got[0].Kind != domain.CredentialPublic || got[1].Kind != domain.CredentialSession {
		t.Fatalf("projection = %+v", got)
	}
	if strings.Contains(string(got[1].ID), session) || strings.Contains(got[1].Subject, session) {
		t.Fatalf("session secret leaked: %+v", got[1])
	}
}

func TestProjectAPIKeysExposesPresenceNotSecret(t *testing.T) {
	got := projectAPIKeys([]ProviderBinding{{ProviderID: "p", APIKeyConfigured: true}, {ProviderID: "none"}})
	if len(got) != 1 || got[0].ID != "api-key:p" || !got[0].SecretConfigured {
		t.Fatalf("projection = %+v", got)
	}
}
