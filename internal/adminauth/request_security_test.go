package adminauth

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	value, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustCIDR(t *testing.T, raw string) *net.IPNet {
	t.Helper()
	_, network, err := net.ParseCIDR(raw)
	if err != nil {
		t.Fatal(err)
	}
	return network
}

func TestResolveRequestSecurityIgnoresUntrustedForwardedHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3457/admin/api/auth/status", nil)
	req.RemoteAddr = "198.51.100.4:5000"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "attacker.example")
	got, err := ResolveRequestSecurity(req, RequestSecurityOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.ExternalScheme != "http" || got.ExternalHost != "127.0.0.1:3457" || got.ClientIP.String() != "198.51.100.4" || got.TrustedProxy {
		t.Fatalf("security = %+v", got)
	}
}

func TestResolveRequestSecurityUsesTrustedProxyChain(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/admin/api/auth/status", nil)
	req.RemoteAddr = "10.0.0.5:5000"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "admin.example.test:443")
	req.Header.Set("X-Forwarded-For", "203.0.113.8, 10.0.0.4")
	got, err := ResolveRequestSecurity(req, RequestSecurityOptions{
		CanonicalURL:      mustURL(t, "https://admin.example.test"),
		TrustedProxyCIDRs: []*net.IPNet{mustCIDR(t, "10.0.0.0/8")},
		ProxyMode:         true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.TrustedProxy || !got.Secure || got.ExternalHost != "admin.example.test" || got.ClientIP.String() != "203.0.113.8" {
		t.Fatalf("security = %+v", got)
	}
}

func TestResolveRequestSecurityRejectsAmbiguousForwarding(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/admin/api/auth/status", nil)
	req.RemoteAddr = "10.0.0.5:5000"
	req.Header.Set("Forwarded", `for=203.0.113.8;proto=https;host=admin.example.test`)
	req.Header.Set("X-Forwarded-Proto", "https")
	if _, err := ResolveRequestSecurity(req, RequestSecurityOptions{TrustedProxyCIDRs: []*net.IPNet{mustCIDR(t, "10.0.0.0/8")}}); err == nil {
		t.Fatal("ambiguous forwarded headers succeeded")
	}
}

func TestResolveRequestSecurityRejectsCanonicalMismatchAndUntrustedProxyMode(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3457/admin/api/auth/status", nil)
	req.RemoteAddr = "198.51.100.4:5000"
	if _, err := ResolveRequestSecurity(req, RequestSecurityOptions{CanonicalURL: mustURL(t, "http://localhost:3457")}); err == nil {
		t.Fatal("canonical host mismatch succeeded")
	}
	if _, err := ResolveRequestSecurity(req, RequestSecurityOptions{ProxyMode: true}); err == nil {
		t.Fatal("untrusted direct proxy-mode request succeeded")
	}
}

func TestValidateSameOrigin(t *testing.T) {
	security := RequestSecurity{
		PeerIP:         net.ParseIP("127.0.0.1"),
		ClientIP:       net.ParseIP("127.0.0.1"),
		ExternalScheme: "http",
		ExternalHost:   "127.0.0.1:3457",
	}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:3457/admin/api/login", nil)
	req.Header.Set("Origin", "http://127.0.0.1:3457")
	if err := ValidateSameOrigin(req, security, true, true); err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://evil.example")
	if err := ValidateSameOrigin(req, security, true, true); err == nil {
		t.Fatal("cross-origin mutation succeeded")
	}
	req.Header.Del("Origin")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if err := ValidateSameOrigin(req, security, true, true); err != nil {
		t.Fatalf("direct loopback desktop exception rejected: %v", err)
	}
	security.TrustedProxy = true
	if err := ValidateSameOrigin(req, security, true, true); err == nil {
		t.Fatal("proxied missing-Origin mutation succeeded")
	}
}

func TestNormalizeAuthorityRejectsRebindingShapes(t *testing.T) {
	for _, value := range []string{"user@localhost", "localhost.", "localhost/path", "[::1", "bad host"} {
		if _, err := NormalizeAuthority(value, "http"); err == nil {
			t.Fatalf("NormalizeAuthority(%q) succeeded", value)
		}
	}
	if got, err := NormalizeAuthority("[::1]:80", "http"); err != nil || got != "[::1]" {
		t.Fatalf("normalized IPv6 = %q, %v", got, err)
	}
}
