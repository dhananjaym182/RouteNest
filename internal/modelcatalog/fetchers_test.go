package modelcatalog

// Offline characterization of catalogue fetchers (plan Phase 3).
//
// Each special fetcher type is exercised with a response body it would return.
// Where a verified fixture exists, the test asserts the parsed result. Where it
// does not, the test pins the generic outcome, so a change in behaviour is a
// visible, deliberate edit.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/fetchers/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

// serveModels answers /v1/models with body and everything else with 404, like a
// gateway that exposes the OpenAI list only under /v1.
func serveModels(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
}

// Verified: the captured bynara payload parses through the generic OpenAI path
// with the 404 /models -> /v1/models retry. Context and capability fields are
// not read by RouteNest, so only ids are compared.
func TestFetcherFixtureBynaraCapturedPayload(t *testing.T) {
	srv := serveModels(t, readFixture(t, "bynara-v1-models.json"))
	defer srv.Close()

	entries, err := Discover(srv.Client(), srv.URL, "", nil, "")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(entries) != 17 {
		t.Fatalf("entries = %d, want 17", len(entries))
	}
	if entries[0].ID != "agnes-2.0-flash" {
		t.Errorf("first id = %q (entries are sorted by id)", entries[0].ID)
	}
}

// Unverified: inxora's catalog reports accessibility and chat flags. The generic
// parser ignores them, so inaccessible entries are listed. Behaviour is kept
// until a verified inxora fixture justifies a type-specific strategy.
func TestFetcherFixtureInxoraGenericIgnoresAccessFlags(t *testing.T) {
	body := `{"models":[
		{"id":"ok-model","accessible":true,"chat":true},
		{"id":"locked-model","accessible":false,"chat":true},
		{"id":"image-only","chat":false}]}`
	srv := serveModels(t, body)
	defer srv.Close()

	entries, err := Discover(srv.Client(), srv.URL, "", nil, "")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	var ids []string
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	if got := strings.Join(ids, ","); got != "image-only,locked-model,ok-model" {
		t.Errorf("ids = %s; generic path must keep every id", got)
	}
}

// Unsupported: agentrouter's catalogue is /api/pricing with model_name fields,
// which the generic parser cannot read. Its fetcher URL is also ignored because it
// does not end in /models, so the probe reports no models.
func TestFetcherFixtureAgentrouterPricingUnsupported(t *testing.T) {
	body := `{"data":[{"model_name":"gpt-x","quota_type":1}]}`
	srv := serveModels(t, body)
	defer srv.Close()

	_, err := Discover(srv.Client(), srv.URL, "", nil, "")
	if err == nil || err.Error() != "upstream returned no models" {
		t.Fatalf("err = %v, want \"upstream returned no models\"", err)
	}
}

// Unsupported: mimo-free's source is the models.dev catalogue, keyed by
// provider. The generic parser finds no data or models array and reports none.
func TestFetcherFixtureMimoCatalogUnsupported(t *testing.T) {
	body := `{"xiaomi":{"id":"xiaomi","models":{"mimo-v2":{"id":"mimo-v2"}}}}`
	srv := serveModels(t, body)
	defer srv.Close()

	_, err := Discover(srv.Client(), srv.URL, "", nil, "")
	if err == nil || err.Error() != "upstream returned no models" {
		t.Fatalf("err = %v, want \"upstream returned no models\"", err)
	}
}

// Current behaviour: a transport failure on the primary base still tries the
// catalogue fallback. The admin probe did not do this; the unified path keeps it.
func TestDiscoverTransportErrorTriesFetcherBase(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	catalog := serveModels(t, `{"data":[{"id":"from-catalog"}]}`)
	defer catalog.Close()

	entries, err := Discover(http.DefaultClient, deadURL, "", nil, catalog.URL)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "from-catalog" {
		t.Fatalf("entries = %+v", entries)
	}
}

// Current behaviour: when both the primary and the catalogue fail, the error
// returned is the catalogue's. The admin probe returned the primary's status
// instead. The unified path returns the primary error (see Phase 3 decision).
func TestDiscoverBothFailReturnsFetcherErrorCurrently(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "primary down", http.StatusInternalServerError)
	}))
	defer primary.Close()
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "catalog down", http.StatusBadGateway)
	}))
	defer catalog.Close()

	_, err := Discover(primary.Client(), primary.URL, "", nil, catalog.URL)
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("err = %v, want the catalogue's HTTP 502 currently", err)
	}
}
