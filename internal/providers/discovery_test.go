package providers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"routenest/internal/connections"
)

// bearerCatalogServer serves /models and records the auth-related headers it sees.
func bearerCatalogServer(t *testing.T, status int, body string, seen *http.Header) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = r.Header.Clone()
		}
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
}

// Import preview and recheck previously ignored bound connections. The unified
// entry point sends the connection's token instead of the stale stored key.
func TestDiscoverUsesBoundConnectionCredentials(t *testing.T) {
	conn := connections.UpsertConnection(&connections.Connection{
		Provider: "kilocode", AuthType: "device", Email: "bound-disc@example.invalid",
		AccessToken: "conn-token-xyz", Status: "active",
		ProviderSpecificData: map[string]string{"orgId": "org-42"},
	})
	t.Cleanup(func() { connections.RemoveConnection(conn.ID) })

	var seen http.Header
	srv := bearerCatalogServer(t, http.StatusOK, `{"data":[{"id":"m1"}]}`, &seen)
	defer srv.Close()

	p := CustomProvider{ID: "bound-disc", Name: "Bound", BaseURL: srv.URL, APIKey: "stale-key", ConnectionID: conn.ID, Enabled: true}
	entries, err := DiscoverModels(p, DiscoveryOverrides{})
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %v, err = %v", entries, err)
	}
	if got := seen.Get("Authorization"); got != "Bearer conn-token-xyz" {
		t.Errorf("Authorization = %q, want the connection token", got)
	}
	if spec := connections.ProviderDeviceFlows["kilocode"]; spec.OrgHeader != "" {
		if got := seen.Get(spec.OrgHeader); got != "org-42" {
			t.Errorf("%s = %q, want the connection's organization id", spec.OrgHeader, got)
		}
	} else {
		t.Fatal("kilocode flow must define an organization header for this test")
	}
}

func TestDiscoverConnectionUnavailableIsNamed(t *testing.T) {
	p := CustomProvider{ID: "gone", Name: "Gone", BaseURL: "http://127.0.0.1:1", ConnectionID: "no-such-connection", Enabled: true}
	_, err := DiscoverModels(p, DiscoveryOverrides{})
	if err == nil || err.Error() != errConnectionUnavailable {
		t.Fatalf("err = %v, want %q", err, errConnectionUnavailable)
	}
}

// Upstream error bodies can echo credentials. The returned error never contains
// the stored key, and it keeps the status so the operator can act on it.
func TestDiscoverRedactsStoredKeyFromUpstreamError(t *testing.T) {
	const key = "sk-stored-secret-987"
	srv := bearerCatalogServer(t, http.StatusUnauthorized, "rejected key "+key, nil)
	defer srv.Close()

	_, err := DiscoverModels(CustomProvider{BaseURL: srv.URL, APIKey: key}, DiscoveryOverrides{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), key) {
		t.Fatalf("error leaks the stored key: %v", err)
	}
	if !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("status lost: %v", err)
	}
}

// Request overrides win over stored values, so the admin probe tests what the
// operator typed.
func TestDiscoverOverridesReplaceStoredValues(t *testing.T) {
	var seen http.Header
	srv := bearerCatalogServer(t, http.StatusOK, `{"data":[{"id":"ok"}]}`, &seen)
	defer srv.Close()

	stored := CustomProvider{BaseURL: "http://127.0.0.1:1", APIKey: "stored-key", Headers: map[string]string{"X-Stored": "1"}}
	_, err := DiscoverModels(stored, DiscoveryOverrides{BaseURL: srv.URL, APIKey: "typed-key", Headers: map[string]string{"X-Typed": "2"}})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if seen.Get("Authorization") != "Bearer typed-key" || seen.Get("X-Typed") != "2" || seen.Get("X-Stored") != "" {
		t.Fatalf("headers = %v, want the overrides only", seen)
	}
}

// The Qoder adapter's error text can echo the bound token. It must be masked.
func TestQoderDiscoveryErrorRedactsBoundToken(t *testing.T) {
	const token = "qoder-token-leak-check"
	conn := qoderConnection(t, "uid-phase3-redact")
	conn.AccessToken = token
	connections.UpsertConnection(conn)
	ft := &fakeTransport{respond: func(r *http.Request) (int, string) {
		return http.StatusInternalServerError, "server echoed " + token
	}}
	installFakeHTTP(t, ft)

	_, err := DiscoverModels(CustomProvider{ID: "qr", Name: "Qoder", BaseURL: "https://api3.qoder.sh", ConnectionID: conn.ID}, DiscoveryOverrides{})
	if err == nil {
		t.Fatal("expected the forced upstream error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("qoder error leaks the token: %v", err)
	}
}
