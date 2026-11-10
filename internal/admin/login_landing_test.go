package admin

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"routenest/internal/adminauth"
)

func loginLandingRuntime(t *testing.T, canonical string) *adminSecurityRuntime {
	t.Helper()
	canonicalURL, err := url.Parse(canonical)
	if err != nil {
		t.Fatal(err)
	}
	return &adminSecurityRuntime{
		requestOptions: adminauth.RequestSecurityOptions{CanonicalURL: canonicalURL},
	}
}

func loginLandingMux(runtime *adminSecurityRuntime) *http.ServeMux {
	mux := http.NewServeMux()
	registry := newAdminRouteRegistry()
	registerSessionRoutes(registry)
	mux.Handle("/admin/api/", runtime.handler(registry))
	mux.Handle("/admin/", runtime.staticHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	})))
	return mux
}

func loginLandingRequest(method, target, remote string) *http.Request {
	request := httptest.NewRequest(method, target, nil)
	request.RemoteAddr = remote
	return request
}

func TestLocalAdminLandingRedirectsLoopbackAliasesToCanonical(t *testing.T) {
	tests := []struct {
		name      string
		canonical string
		alias     string
		remote    string
	}{
		{name: "default IPv4 through localhost", canonical: "http://127.0.0.1:3457", alias: "http://localhost:3457", remote: "127.0.0.1:41000"},
		{name: "default IPv4 through IPv6", canonical: "http://127.0.0.1:3457", alias: "http://[::1]:3457", remote: "[::1]:41000"},
		{name: "IPv6 through localhost", canonical: "http://[::1]:41909", alias: "http://localhost:41909", remote: "[::1]:41000"},
		{name: "IPv6 through IPv4", canonical: "http://[::1]:41909", alias: "http://127.0.0.1:41909", remote: "127.0.0.1:41000"},
		{name: "localhost through IPv4", canonical: "http://localhost:3458", alias: "http://127.0.0.1:3458", remote: "127.0.0.1:41000"},
		{name: "localhost through IPv6", canonical: "http://localhost:3458", alias: "http://[::1]:3458", remote: "[::1]:41000"},
		{name: "default HTTP port", canonical: "http://127.0.0.1", alias: "http://localhost:80", remote: "127.0.0.1:41000"},
	}
	for _, test := range tests {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			t.Run(test.name+" "+method, func(t *testing.T) {
				runtime := loginLandingRuntime(t, test.canonical)
				recorder := httptest.NewRecorder()
				loginLandingMux(runtime).ServeHTTP(recorder, loginLandingRequest(method, test.alias+"/admin/?handoff=secret", test.remote))
				if recorder.Code != http.StatusTemporaryRedirect {
					t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusTemporaryRedirect, recorder.Body.String())
				}
				if location := recorder.Header().Get("Location"); location != test.canonical+"/admin/" {
					t.Fatalf("Location = %q, want %q", location, test.canonical+"/admin/")
				}
				if len(recorder.Result().Cookies()) != 0 {
					t.Fatalf("redirect issued cookies: %+v", recorder.Result().Cookies())
				}
				if method == http.MethodHead && recorder.Body.Len() != 0 {
					t.Fatalf("HEAD redirect body = %q", recorder.Body.String())
				}
			})
		}
	}
}

func TestLocalAdminLandingRejectsForwardingHeaders(t *testing.T) {
	for name, headers := range map[string]http.Header{
		"Forwarded": {
			"Forwarded": {`for=198.51.100.7;proto=https;host=attacker.example`},
		},
		"X-Forwarded": {
			"X-Forwarded-For":   {"198.51.100.7"},
			"X-Forwarded-Host":  {"attacker.example"},
			"X-Forwarded-Proto": {"https"},
		},
		"other X-Forwarded header": {
			"X-Forwarded-Port": {"3457"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			runtime := loginLandingRuntime(t, "http://127.0.0.1:3457")
			request := loginLandingRequest(http.MethodGet, "http://localhost:3457/admin/", "127.0.0.1:41000")
			request.Header = headers
			recorder := httptest.NewRecorder()
			loginLandingMux(runtime).ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest || recorder.Header().Get("Location") != "" || len(recorder.Result().Cookies()) != 0 {
				t.Fatalf("forwarded request = %d Location %q cookies %+v", recorder.Code, recorder.Header().Get("Location"), recorder.Result().Cookies())
			}
		})
	}
}

func TestLocalAdminLandingServesCanonicalAuthority(t *testing.T) {
	for _, canonical := range []string{
		"http://127.0.0.1:3457",
		"http://localhost:3458",
		"http://[::1]:41909",
	} {
		t.Run(canonical, func(t *testing.T) {
			runtime := loginLandingRuntime(t, canonical)
			recorder := httptest.NewRecorder()
			loginLandingMux(runtime).ServeHTTP(recorder, loginLandingRequest(http.MethodGet, canonical+"/admin/", "127.0.0.1:41000"))
			if recorder.Code != http.StatusOK || recorder.Header().Get("Location") != "" {
				t.Fatalf("canonical landing = %d Location %q", recorder.Code, recorder.Header().Get("Location"))
			}
		})
	}
}

func TestLocalAdminLandingRejectsUnsafeAliasContexts(t *testing.T) {
	_, trustedLoopback, err := net.ParseCIDR("127.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	_, unrelatedProxy, err := net.ParseCIDR("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		canonical string
		method    string
		target    string
		remote    string
		headers   http.Header
		configure func(*adminSecurityRuntime)
	}{
		{name: "arbitrary host", canonical: "http://127.0.0.1:3457", method: http.MethodGet, target: "http://attacker.example:3457/admin/", remote: "127.0.0.1:41000"},
		{name: "DNS rebinding shape", canonical: "http://127.0.0.1:3457", method: http.MethodGet, target: "http://127.0.0.1.attacker.example:3457/admin/", remote: "127.0.0.1:41000"},
		{name: "wrong port", canonical: "http://127.0.0.1:3457", method: http.MethodGet, target: "http://localhost:3458/admin/", remote: "127.0.0.1:41000"},
		{name: "remote source", canonical: "http://127.0.0.1:3457", method: http.MethodGet, target: "http://localhost:3457/admin/", remote: "198.51.100.7:41000"},
		{name: "spoofed remote source", canonical: "http://127.0.0.1:3457", method: http.MethodGet, target: "http://localhost:3457/admin/", remote: "198.51.100.7:41000", headers: http.Header{"X-Forwarded-For": {"127.0.0.1"}}},
		{name: "spoofed alias host", canonical: "http://127.0.0.1:3457", method: http.MethodGet, target: "http://attacker.example:3457/admin/", remote: "127.0.0.1:41000", headers: http.Header{"X-Forwarded-Host": {"localhost:3457"}, "X-Forwarded-Proto": {"http"}}},
		{name: "cross-site navigation", canonical: "http://127.0.0.1:3457", method: http.MethodGet, target: "http://localhost:3457/admin/", remote: "127.0.0.1:41000", headers: http.Header{"Sec-Fetch-Site": {"cross-site"}}},
		{name: "foreign origin", canonical: "http://127.0.0.1:3457", method: http.MethodGet, target: "http://localhost:3457/admin/", remote: "127.0.0.1:41000", headers: http.Header{"Origin": {"http://attacker.example"}}},
		{name: "null origin", canonical: "http://127.0.0.1:3457", method: http.MethodGet, target: "http://localhost:3457/admin/", remote: "127.0.0.1:41000", headers: http.Header{"Origin": {"null"}}},
		{name: "repeated origin", canonical: "http://127.0.0.1:3457", method: http.MethodGet, target: "http://localhost:3457/admin/", remote: "127.0.0.1:41000", headers: http.Header{"Origin": {"http://localhost:3457", "http://attacker.example"}}},
		{name: "wrong scheme", canonical: "http://127.0.0.1:3457", method: http.MethodGet, target: "https://localhost:3457/admin/", remote: "127.0.0.1:41000"},
		{name: "wrong path", canonical: "http://127.0.0.1:3457", method: http.MethodGet, target: "http://localhost:3457/admin/index.html", remote: "127.0.0.1:41000"},
		{name: "non-loopback canonical", canonical: "http://admin.example.test:3457", method: http.MethodGet, target: "http://localhost:3457/admin/", remote: "127.0.0.1:41000"},
		{name: "proxy mode", canonical: "http://127.0.0.1:3457", method: http.MethodGet, target: "http://localhost:3457/admin/", remote: "127.0.0.1:41000", configure: func(runtime *adminSecurityRuntime) {
			runtime.requestOptions.ProxyMode = true
		}},
		{name: "trusted proxy", canonical: "http://127.0.0.1:3457", method: http.MethodGet, target: "http://localhost:3457/admin/", remote: "127.0.0.1:41000", configure: func(runtime *adminSecurityRuntime) {
			runtime.requestOptions.TrustedProxyCIDRs = []*net.IPNet{trustedLoopback}
		}},
		{name: "unrelated trusted proxy CIDR", canonical: "http://127.0.0.1:3457", method: http.MethodGet, target: "http://localhost:3457/admin/", remote: "127.0.0.1:41000", configure: func(runtime *adminSecurityRuntime) {
			runtime.requestOptions.TrustedProxyCIDRs = []*net.IPNet{unrelatedProxy}
		}},
		{name: "trusted proxy mode", canonical: "http://127.0.0.1:3457", method: http.MethodGet, target: "http://localhost:3457/admin/", remote: "127.0.0.1:41000", configure: func(runtime *adminSecurityRuntime) {
			runtime.requestOptions.ProxyMode = true
			runtime.requestOptions.TrustedProxyCIDRs = []*net.IPNet{trustedLoopback}
		}},
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		tests = append(tests, struct {
			name      string
			canonical string
			method    string
			target    string
			remote    string
			headers   http.Header
			configure func(*adminSecurityRuntime)
		}{name: "mutation " + method, canonical: "http://127.0.0.1:3457", method: method, target: "http://localhost:3457/admin/", remote: "127.0.0.1:41000"})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := loginLandingRuntime(t, test.canonical)
			if test.configure != nil {
				test.configure(runtime)
			}
			request := loginLandingRequest(test.method, test.target, test.remote)
			request.Header = test.headers.Clone()
			recorder := httptest.NewRecorder()
			loginLandingMux(runtime).ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}
			if recorder.Header().Get("Location") != "" || len(recorder.Result().Cookies()) != 0 {
				t.Fatalf("denial Location %q cookies %+v", recorder.Header().Get("Location"), recorder.Result().Cookies())
			}
		})
	}
}

func TestLocalAdminLandingKeepsAPILoginCanonicalStrict(t *testing.T) {
	tests := []struct {
		canonical string
		alias     string
		remote    string
	}{
		{canonical: "http://127.0.0.1:3457", alias: "http://localhost:3457", remote: "127.0.0.1:41000"},
		{canonical: "http://127.0.0.1:3457", alias: "http://[::1]:3457", remote: "[::1]:41000"},
		{canonical: "http://[::1]:41909", alias: "http://localhost:41909", remote: "[::1]:41000"},
		{canonical: "http://[::1]:41909", alias: "http://127.0.0.1:41909", remote: "127.0.0.1:41000"},
	}
	for _, test := range tests {
		t.Run(test.canonical+" from "+test.alias, func(t *testing.T) {
			runtime := loginLandingRuntime(t, test.canonical)
			request := httptest.NewRequest(http.MethodPost, test.alias+"/admin/api/login", strings.NewReader(`{"username":"admin","password":"secret"}`))
			request.RemoteAddr = test.remote
			request.Header.Set("Origin", test.alias)
			recorder := httptest.NewRecorder()
			loginLandingMux(runtime).ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest || recorder.Header().Get("Location") != "" || len(recorder.Result().Cookies()) != 0 {
				t.Fatalf("alias login = %d Location %q cookies %+v: %s", recorder.Code, recorder.Header().Get("Location"), recorder.Result().Cookies(), recorder.Body.String())
			}
		})
	}
}

func TestLocalAdminLandingPreservesServeMuxSlashRedirect(t *testing.T) {
	runtime := loginLandingRuntime(t, "http://127.0.0.1:3457")
	recorder := httptest.NewRecorder()
	loginLandingMux(runtime).ServeHTTP(recorder, loginLandingRequest(http.MethodGet, "http://localhost:3457/admin", "127.0.0.1:41000"))
	if recorder.Code != http.StatusTemporaryRedirect || recorder.Header().Get("Location") != "/admin/" {
		t.Fatalf("slash redirect = %d Location %q", recorder.Code, recorder.Header().Get("Location"))
	}
}
