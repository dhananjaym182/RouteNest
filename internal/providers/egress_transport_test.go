package providers

import (
	"net/http"
	"testing"

	"routenest/internal/egress"
	"routenest/internal/providerstate"
)

func TestProviderTransportUsesEgressResolverWhenInstalled(t *testing.T) {
	if egress.HasDefault() {
		t.Skip("egress resolver already published by another test")
	}
	registry, err := egress.NewRegistry("global", staticEgressSource{})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := egress.NewResolver(registry, providerstate.NewStore(nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := egress.Publish(resolver); err != nil {
		t.Fatal(err)
	}

	tr, err := providerTransportFor("p", "direct")
	if err != nil {
		t.Fatal(err)
	}
	direct, ok := tr.(*http.Transport)
	if !ok || direct.Proxy != nil {
		t.Fatalf("resolver direct transport = %T", tr)
	}

	if _, err := providerTransportFor("p", "ftp://bad:1"); err == nil {
		t.Fatal("resolver must reject unsupported schemes")
	}
}

type staticEgressSource struct{}

func (staticEgressSource) ProfileID() string { return "global" }
func (staticEgressSource) Snapshot() egress.Profile {
	return egress.Profile{ID: "global"}
}
