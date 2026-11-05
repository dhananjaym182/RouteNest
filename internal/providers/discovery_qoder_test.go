package providers

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"routenest/internal/connections"
)

// fakeTransport answers every outbound request in-process and records it.
type fakeTransport struct {
	mu       sync.Mutex
	requests []*http.Request
	respond  func(r *http.Request) (int, string)
}

func (f *fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.requests = append(f.requests, r)
	f.mu.Unlock()
	status, body := f.respond(r)
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    r,
	}, nil
}

func (f *fakeTransport) seen() []*http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*http.Request(nil), f.requests...)
}

// installFakeHTTP routes the connection OAuth client through transport.
func installFakeHTTP(t *testing.T, transport http.RoundTripper) {
	t.Helper()
	old := connections.HTTPClient
	connections.HTTPClient = &http.Client{Transport: transport}
	t.Cleanup(func() { connections.HTTPClient = old })
}

// qoderConnection creates a usable qoder login connection with a unique user id,
// because the Qoder catalogue cache is keyed by base URL and user id.
func qoderConnection(t *testing.T, userID string) *connections.Connection {
	t.Helper()
	conn := connections.UpsertConnection(&connections.Connection{
		Provider:    "qoder",
		AuthType:    "device",
		Email:       userID + "@example.invalid",
		AccessToken: "token-" + userID,
		Status:      "active",
		ProviderSpecificData: map[string]string{
			"userId":    userID,
			"machineId": "machine-" + userID,
		},
	})
	t.Cleanup(func() { connections.RemoveConnection(conn.ID) })
	return conn
}

// Characterization of the signed Qoder catalogue path: a qoder-protocol provider
// reads /algo/api/v2/model/list with COSY headers and never probes an OpenAI
// /models endpoint.
func TestQoderSignedDiscoveryCharacterization(t *testing.T) {
	conn := qoderConnection(t, "uid-phase3-characterize")
	ft := &fakeTransport{respond: func(r *http.Request) (int, string) {
		return http.StatusOK, `{"chat":[{"key":"auto"},{"key":"performance"}]}`
	}}
	installFakeHTTP(t, ft)

	p := &CustomProvider{ID: "qoder-char", Name: "Qoder", BaseURL: "https://api3.qoder.sh", ConnectionID: conn.ID, Enabled: true}
	if got := Protocol(p); got != "qoder" {
		t.Fatalf("Protocol = %q, want qoder", got)
	}

	entries, err := DiscoverModels(*p, DiscoveryOverrides{})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	var ids []string
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	if strings.Join(ids, ",") != "qoder/auto,qoder/performance" {
		t.Fatalf("ids = %v", ids)
	}

	reqs := ft.seen()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want exactly one catalogue read", len(reqs))
	}
	r := reqs[0]
	if r.URL.Path != "/algo/api/v2/model/list" {
		t.Errorf("path = %q", r.URL.Path)
	}
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer COSY.") {
		t.Errorf("Authorization = %q, want a COSY-signed bearer", r.Header.Get("Authorization"))
	}
	if got := r.Header.Get("Cosy-User"); got != "uid-phase3-characterize" {
		t.Errorf("Cosy-User = %q", got)
	}
	if r.Header.Get("Cosy-Sigpath") == "" || r.Header.Get("Cosy-Bodyhash") == "" {
		t.Error("signature headers missing")
	}
}
