package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"routenest/internal/adminauth"
)

// scripts/docker-smoke.sh starts the container on 0.0.0.0 with a canonical URL, proxy mode and the
// Docker bridge gateway as the single trusted peer. CI failed because the script omitted these
// settings; this pins the exact combination so the smoke test and the server rules stay in step.
func TestContainerListenerRequiresAndAcceptsTrustedProxySettings(t *testing.T) {
	smoke := SecurityOptions{ListenHost: "0.0.0.0", ListenPort: 3457}

	if _, err := requestSecurityOptions(smoke); err == nil || !strings.Contains(err.Error(), "non-loopback administration requires explicit trusted-proxy mode") {
		t.Fatalf("a bare 0.0.0.0 listener must be refused, got %v", err)
	}

	smoke.CanonicalURL = "http://127.0.0.1:39999"
	smoke.ProxyMode = true
	smoke.TrustedProxyCIDRs = []string{"172.17.0.1/32"}
	options, err := requestSecurityOptions(smoke)
	if err != nil {
		t.Fatalf("the smoke-test configuration must be accepted: %v", err)
	}

	// A published Docker port reaches the container from the bridge gateway, with the Host the
	// host's curl used and no forwarding headers. That must be accepted...
	request := httptest.NewRequest(http.MethodPost, "/admin/api/auth/bootstrap", nil)
	request.Host = "127.0.0.1:39999"
	request.RemoteAddr = "172.17.0.1:50000"
	if _, err := adminauth.ResolveRequestSecurity(request, options); err != nil {
		t.Fatalf("a request through the published port was rejected: %v", err)
	}
	// ...while any other peer still cannot reach the admin API directly.
	request.RemoteAddr = "172.17.0.99:50000"
	if _, err := adminauth.ResolveRequestSecurity(request, options); err == nil {
		t.Fatal("an untrusted peer must be rejected in proxy mode")
	}
}
