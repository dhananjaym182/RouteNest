package providers

import (
	"net/http"
	"testing"

	"routenest/internal/egress"
)

type roundTripperStub struct{ calls int }

func (s *roundTripperStub) RoundTrip(*http.Request) (*http.Response, error) {
	s.calls++
	return nil, nil
}

func TestProviderTransportForInheritsGlobal(t *testing.T) {
	old := TransportProvider
	t.Cleanup(func() { TransportProvider = old })

	probe := &roundTripperStub{}
	TransportProvider = func() http.RoundTripper { return probe }

	tr, err := providerTransportFor("p", "")
	if err != nil {
		t.Fatal(err)
	}
	if tr != http.RoundTripper(probe) {
		t.Fatal("empty spec must inherit the global transport exactly")
	}
}

func TestProviderTransportForDirectAndProxy(t *testing.T) {
	tr, err := providerTransportFor("p", "none")
	if err != nil {
		t.Fatal(err)
	}
	direct, ok := tr.(*http.Transport)
	if !ok {
		t.Fatalf("direct transport expected, got %T", tr)
	}
	if direct.Proxy != nil {
		t.Fatal("direct must bypass environment proxies")
	}

	tr2, err := providerTransportFor("p", "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	proxied, ok := tr2.(*http.Transport)
	if !ok || proxied.Proxy == nil {
		t.Fatalf("proxy transport expected, got %T", tr2)
	}
	req, _ := http.NewRequest(http.MethodGet, "https://example.test/v1", nil)
	u, err := proxied.Proxy(req)
	if err != nil || u == nil || u.Host != "127.0.0.1:8080" {
		t.Fatalf("proxy func = %v, err=%v", u, err)
	}

	// Same spec must reuse the same transport instance (connection pooling).
	tr3, err := providerTransportFor("p", "direct")
	if err != nil {
		t.Fatal(err)
	}
	if tr3 != tr {
		t.Fatal("normalized specs should share a cached transport")
	}
}

func TestProviderTransportForRejectsInvalidSpec(t *testing.T) {
	if _, err := providerTransportFor("p", "ftp://example.test"); err == nil {
		t.Fatal("invalid proxy scheme must be rejected")
	}
}

type emptyEgressSource struct{}

func (emptyEgressSource) ProfileID() string { return "global" }
func (emptyEgressSource) Snapshot() egress.Profile {
	return egress.Profile{ID: "global"}
}

func TestProviderTransportForRoutesOverridesThroughEgressResolver(t *testing.T) {
	registry, err := egress.NewRegistry("global", emptyEgressSource{})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := egress.NewResolver(registry, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := egress.Publish(resolver); err != nil {
		t.Fatal(err)
	}
	providerTransportCache.Range(func(key, _ any) bool {
		providerTransportCache.Delete(key)
		return true
	})

	tr, err := providerTransportFor("p", "http://127.0.0.1:9090")
	if err != nil {
		t.Fatal(err)
	}
	proxied, ok := tr.(*http.Transport)
	if !ok || proxied.Proxy == nil {
		t.Fatalf("proxy transport expected, got %T", tr)
	}
	req, _ := http.NewRequest(http.MethodGet, "https://example.test/v1", nil)
	u, err := proxied.Proxy(req)
	if err != nil || u == nil || u.Host != "127.0.0.1:9090" {
		t.Fatalf("proxy func = %v, err=%v", u, err)
	}

	direct, err := providerTransportFor("p", "direct")
	if err != nil {
		t.Fatal(err)
	}
	if dt, ok := direct.(*http.Transport); !ok || dt.Proxy != nil {
		t.Fatalf("direct transport expected, got %T", direct)
	}
}
