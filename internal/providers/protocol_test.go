package providers

import (
	"strings"
	"testing"
)

// TestProviderProtocolByBaseURL pins the baseURL → protocol mapping. Providers
// carry no protocol field, so a drift here would silently send signed Qoder
// accounts down the plain OpenAI-compatible path.
func TestProviderProtocolByBaseURL(t *testing.T) {
	cases := []struct {
		name string
		base string
		want string
	}{
		{"qoder exact", "https://api3.qoder.sh", "qoder"},
		{"qoder trailing slash", "https://api3.qoder.sh/", "qoder"},
		{"qoder chat suffix", "https://api3.qoder.sh/chat/completions", "qoder"},
		{"qoder cn", "https://api3.qoder.com.cn", "qoder"},
		{"qoder cn uppercase", "https://API3.Qoder.com.CN/", "qoder"},
		{"openai compatible", "https://api.openai.com/v1", ""},
		{"unknown host", "https://example.invalid/v1", ""},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &CustomProvider{BaseURL: c.base}
			if got := Protocol(p); got != c.want {
				t.Fatalf("Protocol(%q) = %q, want %q", c.base, got, c.want)
			}
		})
	}
	if got := Protocol(nil); got != "" {
		t.Fatalf("Protocol(nil) = %q, want empty", got)
	}
}

// TestQoderPresetsAreRoutable guards the wiring that makes a logged-in Qoder
// account usable: the preset must declare the qoder protocol, stay a login-flow
// provider, and ship a catalog so models exist before any manual import.
func TestQoderPresetsAreRoutable(t *testing.T) {
	for _, id := range []string{"qoder", "qoder-cn"} {
		p, ok := providerPresets[id]
		if !ok {
			t.Fatalf("root preset map missing %q", id)
		}
		if p.Protocol != "qoder" {
			t.Errorf("%s: protocol = %q, want qoder", id, p.Protocol)
		}
		if !p.LoginFlow {
			t.Errorf("%s: loginFlow must be true (device-code auth)", id)
		}
		if len(p.ModelIDs) == 0 {
			t.Errorf("%s: no modelIds, models would be unreachable", id)
		}
		// Custom-provider model ids are global, so every qoder model must be
		// namespaced to avoid colliding with other providers' bare ids ("auto").
		for _, mid := range p.ModelIDs {
			if !strings.HasPrefix(mid, "qoder/") {
				t.Errorf("%s: model id %q is not namespaced with qoder/", id, mid)
			}
		}
		if p.ModelsFetcher == nil || p.ModelsFetcher.Type != "qoder" {
			t.Errorf("%s: modelsFetcher must use the qoder catalog type", id)
		}
		// The dispatch path resolves the protocol by baseURL, so the preset's
		// own baseURL has to round-trip.
		if got := Protocol(&CustomProvider{BaseURL: p.BaseURL}); got != "qoder" {
			t.Errorf("%s: Protocol(%q) = %q", id, p.BaseURL, got)
		}
	}
}
