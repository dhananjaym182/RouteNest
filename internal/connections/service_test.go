package connections

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The owner list must not return any credential. The refresh token and the
// exchanged job token used to leak through the list response.
func TestListConnectionsRedactsAllTokens(t *testing.T) {
	withTestConnections(t, func() {
		c := UpsertConnection(&Connection{
			Provider: "kilocode", Email: "redact@x.io", AccessToken: "acc-secret-1",
			RefreshToken: "ref-secret-2", Status: "active",
			ProviderSpecificData: map[string]string{exchangedTokenKey: "job-secret-3", "orgId": "org-1"},
		})
		defer RemoveConnection(c.ID)
		out := ListConnections()
		if len(out) != 1 {
			t.Fatalf("want 1 connection, got %d", len(out))
		}
		got := out[0]
		if got.AccessToken != "" || got.RefreshToken != "" {
			t.Errorf("tokens returned: access=%q refresh=%q", got.AccessToken, got.RefreshToken)
		}
		if _, ok := got.ProviderSpecificData[exchangedTokenKey]; ok {
			t.Error("exchanged job token returned")
		}
		if got.ProviderSpecificData["orgId"] != "org-1" {
			t.Error("non-secret providerSpecificData must stay visible")
		}
	})
}

// A probe answer that echoes the credential is masked before it is returned.
func TestTestConnectionMasksEchoedCredential(t *testing.T) {
	const secret = "probe-secret-token-42"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("upstream saw " + r.Header.Get("Authorization")))
	}))
	defer srv.Close()
	restore := setTestFlow("t-probe", deviceFlowSpec{ModelsTestURL: srv.URL})
	defer restore()

	withTestConnections(t, func() {
		c := UpsertConnection(&Connection{Provider: "t-probe", Email: "p@x.io", AccessToken: secret, Status: "active"})
		defer RemoveConnection(c.ID)
		res, found := TestConnection(c.ID)
		if !found {
			t.Fatal("connection not found")
		}
		if res.Status != http.StatusInternalServerError || res.OK {
			t.Fatalf("status = %d ok = %v", res.Status, res.OK)
		}
		if strings.Contains(res.Body, secret) {
			t.Fatalf("probe body leaks the credential: %q", res.Body)
		}
		if !strings.Contains(res.Body, "***") {
			t.Errorf("probe body should show the mask: %q", res.Body)
		}
	})
}

// A repeated import of the same account replaces the stored token: the last one
// wins.
func TestImportConnectionTokenLastWins(t *testing.T) {
	withTestConnections(t, func() {
		first, err := ImportConnectionToken("kilocode", "dup-t1", "same@x.io", "")
		if err != nil {
			t.Fatal(err)
		}
		second, err := ImportConnectionToken("kilocode", "dup-t2", "same@x.io", "")
		if err != nil {
			t.Fatal(err)
		}
		if first.ID != second.ID {
			t.Fatalf("same account must reuse one connection: %s vs %s", first.ID, second.ID)
		}
		ConnectionsMu.Lock()
		defer ConnectionsMu.Unlock()
		if len(connections) != 1 || connections[0].AccessToken != "dup-t2" {
			t.Fatalf("stored token = %+v, want dup-t2 in one record", connections)
		}
	})
}

// Status of an unknown login session is reported as not found, without a
// connection payload.
func TestProviderLoginStatusUnknownSession(t *testing.T) {
	if _, ok := ProviderLoginStatus("no-such-session"); ok {
		t.Fatal("unknown session must not be found")
	}
}
