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
	"strconv"
	"strings"
	"testing"

	"routenest/internal/providerreg"
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

	entries, err := Discover(Request{Client: srv.Client(), BaseURL: srv.URL})
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

	entries, err := Discover(Request{Client: srv.Client(), BaseURL: srv.URL})
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

	_, err := Discover(Request{Client: srv.Client(), BaseURL: srv.URL})
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

	_, err := Discover(Request{Client: srv.Client(), BaseURL: srv.URL})
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

	entries, err := Discover(Request{Client: http.DefaultClient, BaseURL: deadURL, FetcherBase: catalog.URL})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "from-catalog" {
		t.Fatalf("entries = %+v", entries)
	}
}

// Phase 3 decision: when the primary and the catalogue both fail, the primary
// error is returned. The admin probe already reported the configured endpoint's
// status; import and recheck now report the same thing.
func TestDiscoverBothFailReturnsPrimaryError(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "primary down", http.StatusInternalServerError)
	}))
	defer primary.Close()
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "catalog down", http.StatusBadGateway)
	}))
	defer catalog.Close()

	_, err := Discover(Request{Client: primary.Client(), BaseURL: primary.URL, FetcherBase: catalog.URL})
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") || strings.Contains(err.Error(), "502") {
		t.Fatalf("err = %v, want the primary's HTTP 500", err)
	}
}

// Phase 3: error text never echoes the API key, even when the upstream body does.
func TestDiscoverRedactsAPIKeyInErrors(t *testing.T) {
	const key = "sk-live-secret-123"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad key "+key, http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := Discover(Request{Client: srv.Client(), BaseURL: srv.URL, APIKey: key})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), key) {
		t.Fatalf("error leaks the API key: %v", err)
	}
	if !strings.Contains(err.Error(), "HTTP 401") || !strings.Contains(err.Error(), redactedMask) {
		t.Fatalf("err = %v, want status and mask", err)
	}
}

// Phase 3: a long custom header value is redacted from upstream error text too.
func TestDiscoverRedactsLongHeaderValues(t *testing.T) {
	const token = "org-token-abcdefgh"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "echo "+token, http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := Discover(Request{Client: srv.Client(), BaseURL: srv.URL, Headers: map[string]string{"X-Org-Token": token}})
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("err = %v, want the header value masked", err)
	}
}

// Phase 3: an empty header value deletes the header the request would carry,
// including Authorization (the admin probe's existing semantics).
func TestDiscoverEmptyHeaderDeletesDefault(t *testing.T) {
	var sawAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawAuth = r.Header["Authorization"]
		w.Write([]byte(`{"data":[{"id":"x"}]}`))
	}))
	defer srv.Close()

	if _, err := Discover(Request{Client: srv.Client(), BaseURL: srv.URL, APIKey: "sk-k1", Headers: map[string]string{"Authorization": ""}}); err != nil {
		t.Fatalf("discover: %v", err)
	}
	if sawAuth {
		t.Fatal("Authorization was sent despite the empty header override")
	}
}

// Phase 3: the unified cap admits catalogues larger than the admin's former
// 64 KiB limit. The body is 200 KiB of ids.
func TestDiscoverLargeCatalogueWithinCap(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"data":[`)
	for i := 0; i < 4000; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"id":"provider/model-with-a-long-name-`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`"}`)
	}
	b.WriteString(`]}`)
	if b.Len() <= 64<<10 || b.Len() >= MaxCatalogBody {
		t.Fatalf("fixture size %d must sit between 64KiB and the cap", b.Len())
	}
	srv := serveModels(t, b.String())
	defer srv.Close()
	entries, err := Discover(Request{Client: srv.Client(), BaseURL: srv.URL})
	if err != nil || len(entries) != 4000 {
		t.Fatalf("entries = %d, err = %v", len(entries), err)
	}
}

// Entries with a non-string id are skipped rather than failing the whole list.
func TestParseModelsSkipsNonStringIDs(t *testing.T) {
	entries, err := ParseModels([]byte(`{"data":[{"id":42},{"id":"ok"}]}`))
	if err != nil || len(entries) != 1 || entries[0].ID != "ok" {
		t.Fatalf("entries = %+v, err = %v", entries, err)
	}
}

// Every catalogue fetcher type used by a built-in preset has an executable strategy.
func TestEveryPresetFetcherTypeHasStrategy(t *testing.T) {
	for _, p := range providerreg.Builtin().All() {
		if p.ModelsFetcher == nil {
			continue
		}
		if _, ok := StrategyFor(p.ModelsFetcher.Type); !ok {
			t.Errorf("preset %s: fetcher type %q has no strategy", p.ID, p.ModelsFetcher.Type)
		}
	}
	if s, _ := StrategyFor("bynara"); s.Status != StatusVerified {
		t.Errorf("bynara status = %s, want verified (captured fixture)", s.Status)
	}
	if s, _ := StrategyFor("agentrouter"); s.Status != StatusUnsupported {
		t.Errorf("agentrouter status = %s, want unsupported", s.Status)
	}
}

// An unsupported fetcher type must not be read through the generic path: the
// preset catalogue base is deliberately ignored, even when it looks like a
// /models endpoint.
func TestDiscoverUnsupportedFetcherIgnoresCatalogBase(t *testing.T) {
	var catalogHits int
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		catalogHits++
		w.Write([]byte(`{"data":[{"id":"from-catalog"}]}`))
	}))
	defer catalog.Close()
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "primary down", http.StatusInternalServerError)
	}))
	defer primary.Close()

	_, err := Discover(Request{
		Client:      primary.Client(),
		BaseURL:     primary.URL,
		FetcherBase: catalog.URL,
		FetcherType: "agentrouter",
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("err = %v, want the primary error", err)
	}
	if catalogHits != 0 {
		t.Fatalf("unsupported fetcher requested the catalogue base %d times, want 0", catalogHits)
	}
}

// A signed catalogue is owned by its protocol adapter and must fail here rather
// than be guessed at with a generic GET.
func TestDiscoverSignedFetcherRequiresAdapter(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(`{"data":[{"id":"x"}]}`))
	}))
	defer srv.Close()

	_, err := Discover(Request{Client: srv.Client(), BaseURL: srv.URL, FetcherType: "qoder"})
	if err == nil || !strings.Contains(err.Error(), "protocol adapter") {
		t.Fatalf("err = %v, want a protocol-adapter error", err)
	}
	if hits != 0 {
		t.Fatalf("signed fetcher made %d requests, want 0", hits)
	}
}

// An unknown non-empty type is a visible failure, not a silent generic read.
func TestDiscoverUnknownFetcherTypeFails(t *testing.T) {
	_, err := Discover(Request{Client: http.DefaultClient, BaseURL: "http://127.0.0.1:1", FetcherType: "bogus"})
	if err == nil || !strings.Contains(err.Error(), `unknown catalogue fetcher type "bogus"`) {
		t.Fatalf("err = %v", err)
	}
}

// The preset lookup reports the fetcher type even when the catalogue has no
// separate fallback base, so the strategy table is always consulted.
func TestCatalogueSourceFor(t *testing.T) {
	base, typ := CatalogueSourceFor("https://router.bynara.id/v1")
	if base != "" || typ != "bynara" {
		t.Fatalf("bynara: base=%q type=%q, want empty base and bynara type", base, typ)
	}
	if base, typ := CatalogueSourceFor("https://example.invalid/v1"); base != "" || typ != "" {
		t.Fatalf("unknown URL: base=%q type=%q, want empty", base, typ)
	}
	if base, _ := CatalogueSourceFor("https://opencode.ai"); base != "https://opencode.ai/zen/v1" {
		t.Fatalf("zen base = %q", base)
	}
}
