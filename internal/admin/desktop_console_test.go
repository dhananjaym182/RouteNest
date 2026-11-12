package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	"routenest/internal/adminapi"
	"routenest/internal/adminauth"
)

const desktopConsoleTestPassword = "Native console password 8!"

func setDesktopSecurityRuntimeForTest(t *testing.T, runtime *adminSecurityRuntime) {
	t.Helper()
	adminSecurityState.Lock()
	previous := adminSecurityState.runtime
	adminSecurityState.runtime = runtime
	adminSecurityState.Unlock()
	t.Cleanup(func() {
		adminSecurityState.Lock()
		adminSecurityState.runtime = previous
		adminSecurityState.Unlock()
	})
}

func desktopConsoleForTest(t *testing.T, runtime *adminSecurityRuntime) *DesktopConsole {
	t.Helper()
	console, err := newDesktopConsole(runtime)
	if err != nil {
		t.Fatal(err)
	}
	return console
}

func initializedDesktopConsoleForTest(t *testing.T) (*DesktopConsole, *http.ServeMux, *adminSecurityRuntime) {
	t.Helper()
	mux, runtime := newAdminAuthTestMux(t)
	credentials, err := runtime.service.CompleteBootstrap(t.Context(), "one time bootstrap secret", desktopConsoleTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.service.Logout(t.Context(), credentials.Principal); err != nil {
		t.Fatal(err)
	}
	return desktopConsoleForTest(t, runtime), mux, runtime
}

func desktopConsoleLoginForTest(t *testing.T, console *DesktopConsole, username, password string) DesktopConsoleResponse {
	t.Helper()
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		t.Fatal(err)
	}
	response := console.Request(t.Context(), DesktopConsoleRequest{Method: http.MethodPost, Path: "/admin/api/login", Body: string(body)})
	if response.Status != http.StatusOK || response.CSRF == "" {
		t.Fatalf("native login status = %d, body = %s", response.Status, response.Body)
	}
	return response
}

func requireDesktopConsoleSignedOut(t *testing.T, console *DesktopConsole) {
	t.Helper()
	credentials, err := console.Credentials(t.Context())
	if !errors.Is(err, adminauth.ErrUnauthorized) || credentials.Token != "" || credentials.CSRFToken != "" {
		t.Fatalf("signed-out credentials error = %v", err)
	}
	response := console.Request(t.Context(), DesktopConsoleRequest{Method: http.MethodGet, Path: "/admin/api/auth/session"})
	if response.Status != http.StatusUnauthorized || response.CSRF != "" {
		t.Fatalf("signed-out request status = %d, body = %s", response.Status, response.Body)
	}
}

func TestNewDesktopConsoleRequiresConfiguredAuthority(t *testing.T) {
	setDesktopSecurityRuntimeForTest(t, nil)
	if console, err := NewDesktopConsole(); err == nil || console != nil {
		t.Fatal("unconfigured constructor accepted a native authority")
	}
	_, runtime := newAdminAuthTestMux(t)
	before, err := runtime.service.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	setDesktopSecurityRuntimeForTest(t, runtime)
	console, err := NewDesktopConsole()
	if err != nil || console.runtime != runtime {
		t.Fatalf("configured constructor did not retain the existing runtime: %v", err)
	}
	after, err := runtime.service.State(t.Context())
	if err != nil || before.Revision != after.Revision {
		t.Fatalf("constructor changed durable authority: %v", err)
	}
	requireDesktopConsoleSignedOut(t, console)
}

func TestDesktopConsolePasswordSessionIsPrivate(t *testing.T) {
	console, browser, runtime := initializedDesktopConsoleForTest(t)
	login := desktopConsoleLoginForTest(t, console, "admin", desktopConsoleTestPassword)
	credentials, err := console.Credentials(t.Context())
	if err != nil || credentials.Token == "" || credentials.CSRFToken != login.CSRF {
		t.Fatalf("native credentials were not retained: %v", err)
	}
	principal, err := runtime.service.Authenticate(t.Context(), credentials.Token)
	if err != nil || principal.SessionHash != credentials.Principal.SessionHash || runtime.service.ValidateCSRF(principal, login.CSRF) != nil {
		t.Fatalf("native session is not an ordinary live session: %v", err)
	}
	encoded, err := json.Marshal(login)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{credentials.Token, credentials.Principal.SessionHash, credentials.Principal.CSRFHash, "Set-Cookie", adminapi.SessionCookieName} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("native response exposed session authority")
		}
	}
	if login.ContentType != "application/json" {
		t.Fatalf("native content type = %q", login.ContentType)
	}
	for _, csrfOnly := range []bool{false, true} {
		request := adminTestRequest(http.MethodGet, "/admin/api/auth/session", "")
		request.Header.Set("Origin", "http://127.0.0.1:3457")
		request.Header.Set("User-Agent", "RouteNest Desktop")
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		if csrfOnly {
			request.AddCookie(&http.Cookie{Name: adminCSRFCookie, Value: login.CSRF})
			request.Header.Set("X-RouteNest-CSRF", login.CSRF)
		}
		recorder := httptest.NewRecorder()
		browser.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized || len(recorder.Result().Cookies()) != 0 {
			t.Fatalf("ordinary browser inherited native session: %d", recorder.Code)
		}
	}
	response := console.Request(t.Context(), DesktopConsoleRequest{Method: http.MethodGet, Path: "/admin/api/auth/session"})
	if response.Status != http.StatusOK || response.CSRF != login.CSRF || !strings.Contains(response.Body, `"username":"admin"`) {
		t.Fatalf("native session read = %d %s", response.Status, response.Body)
	}
	requireDesktopConsoleSignedOut(t, desktopConsoleForTest(t, runtime))
}

func TestDesktopConsoleMutationRequiresSuppliedSessionCSRF(t *testing.T) {
	console, _, runtime := initializedDesktopConsoleForTest(t)
	login := desktopConsoleLoginForTest(t, console, "admin", desktopConsoleTestPassword)
	body := `{"username":"native-viewer","password":"Viewer password 8!","roles":["viewer"]}`
	before, err := runtime.service.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, csrf := range []string{"", strings.Repeat("x", 43), login.CSRF + "," + login.CSRF} {
		response := console.Request(t.Context(), DesktopConsoleRequest{Method: http.MethodPost, Path: "/admin/api/auth/users", Body: body, CSRF: csrf})
		if response.Status != http.StatusForbidden || response.CSRF != "" || !strings.Contains(response.Body, "csrf_invalid") {
			t.Fatalf("invalid CSRF mutation = %d %s", response.Status, response.Body)
		}
	}
	after, err := runtime.service.State(t.Context())
	if err != nil || before.Revision != after.Revision {
		t.Fatalf("denied native mutation changed authority: %v", err)
	}
	response := console.Request(t.Context(), DesktopConsoleRequest{Method: http.MethodPost, Path: "/admin/api/auth/users", Body: body, CSRF: login.CSRF})
	if response.Status != http.StatusCreated || response.CSRF != login.CSRF {
		t.Fatalf("valid CSRF mutation = %d %s", response.Status, response.Body)
	}
	credentials, err := console.Credentials(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	users, err := runtime.service.ListUsers(t.Context(), credentials.Principal)
	if err != nil || len(users) != 2 {
		t.Fatalf("registered handler did not mutate the shared service: %v", err)
	}
	console.Clear()
	viewer := desktopConsoleLoginForTest(t, console, "native-viewer", "Viewer password 8!")
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		response := console.Request(t.Context(), DesktopConsoleRequest{Method: method, Path: "/admin/api/auth/users", Body: body, CSRF: viewer.CSRF})
		if response.Status != http.StatusForbidden || response.CSRF != "" || !strings.Contains(response.Body, "permission_denied") {
			t.Fatalf("native registry bypassed RBAC for %s: %d %s", method, response.Status, response.Body)
		}
	}
}

func TestDesktopConsoleCanonicalRequestOwnsSecurityMetadata(t *testing.T) {
	for _, canonical := range []string{"http://127.0.0.1:3457", "https://admin.example.test"} {
		t.Run(canonical, func(t *testing.T) {
			_, runtime := newAdminAuthTestMux(t)
			var err error
			runtime.requestOptions.CanonicalURL, err = url.Parse(canonical)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(canonical, "https:") {
				_, network, err := net.ParseCIDR("192.0.2.0/24")
				if err != nil {
					t.Fatal(err)
				}
				runtime.requestOptions.ProxyMode = true
				runtime.requestOptions.TrustedProxyCIDRs = []*net.IPNet{network}
			}
			if _, err := runtime.service.CompleteBootstrap(t.Context(), "one time bootstrap secret", desktopConsoleTestPassword); err != nil {
				t.Fatal(err)
			}
			console := desktopConsoleForTest(t, runtime)
			login := desktopConsoleLoginForTest(t, console, "admin", desktopConsoleTestPassword)
			credentials, err := console.Credentials(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			registered := console.handler
			console.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Context().Value(desktopHandoffContextKey{}) != runtime || r.Header.Get("Origin") != canonical || r.Header.Get("Referer") != canonical+"/admin/" {
					t.Error("native request omitted canonical private context")
				}
				if r.Header.Get("Sec-Fetch-Site") != "same-origin" || r.Header.Get("Sec-Fetch-Mode") != "same-origin" || r.Header.Get("Sec-Fetch-Dest") != "empty" || r.UserAgent() != "" {
					t.Error("native request did not own fetch metadata")
				}
				if r.URL.RawQuery != "probe=alpha%2Fbeta&number=1" || r.Header.Get("X-RouteNest-CSRF") != login.CSRF {
					t.Error("native request lost query or supplied CSRF")
				}
				cookies := r.Cookies()
				if len(cookies) != 1 || cookies[0].Name != adminapi.SessionCookieName || cookies[0].Value != credentials.Token {
					t.Error("native request did not use its Go-held cookie")
				}
				for _, header := range []string{"Forwarded", "X-Forwarded-For", "Authorization"} {
					if r.Header.Get(header) != "" {
						t.Errorf("unexpected native request header %s", header)
					}
				}
				security, err := adminauth.ResolveRequestSecurity(r, runtime.requestOptions)
				if err != nil || adminauth.ValidateSameOrigin(r, security, true, false) != nil {
					t.Errorf("canonical request failed the existing boundary: %v", err)
				}
				registered.ServeHTTP(w, r)
			})
			response := console.Request(t.Context(), DesktopConsoleRequest{Method: http.MethodGet, Path: "/admin/api/auth/session?probe=alpha%2Fbeta&number=1", CSRF: login.CSRF})
			if response.Status != http.StatusOK {
				t.Fatalf("canonical native request = %d %s", response.Status, response.Body)
			}
		})
	}
}

func TestDesktopConsoleRejectsUnsafePathsMethodsAndBodies(t *testing.T) {
	_, runtime := newAdminAuthTestMux(t)
	console := desktopConsoleForTest(t, runtime)
	registered := console.handler
	calls := 0
	console.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		registered.ServeHTTP(w, r)
	})
	for _, path := range []string{
		"", "/admin/", "/api/login", "admin/api/login", "//127.0.0.1/admin/api/login",
		"http://127.0.0.1:3457/admin/api/login", "http://user:password@127.0.0.1:3457/admin/api/login",
		"wails://wails/admin/api/login", "/admin/api/../login", "/admin/api/./login", "/admin/api//login",
		"/admin/api/%2e%2e/login", "/admin/api/%252e%252e/login", "/admin/api/auth%2fsession", "/admin/api/%6cogin",
		"/admin/api/..\\login", "/admin/api/%5clogin", "/admin/api/login#fragment", "/admin/api/login?x=1#fragment",
		"/admin/api/login\r\nCookie: bad", "/admin/api/login\x00", "/admin/api/login?x=%zz", "/admin/api/login?x=a\\b",
		"/admin/api/" + strings.Repeat("a", desktopConsoleMaxPathBytes),
	} {
		response := console.Request(t.Context(), DesktopConsoleRequest{Method: http.MethodPost, Path: path, Body: `{}`})
		if response.Status != http.StatusBadRequest || response.CSRF != "" {
			t.Errorf("unsafe path %q = %d", path, response.Status)
		}
	}
	for _, method := range []string{"", "get", " POST", "POST\r\nCookie: bad", http.MethodConnect, http.MethodTrace, http.MethodOptions} {
		response := console.Request(t.Context(), DesktopConsoleRequest{Method: method, Path: "/admin/api/login", Body: `{}`})
		if response.Status != http.StatusMethodNotAllowed {
			t.Errorf("invalid method %q = %d", method, response.Status)
		}
	}
	for _, csrf := range []string{"token\r\nCookie: bad", strings.Repeat("x", 257)} {
		response := console.Request(t.Context(), DesktopConsoleRequest{Method: http.MethodPost, Path: "/admin/api/logout", CSRF: csrf})
		if response.Status != http.StatusBadRequest {
			t.Errorf("invalid CSRF envelope status = %d", response.Status)
		}
	}
	response := console.Request(t.Context(), DesktopConsoleRequest{Method: http.MethodPost, Path: "/admin/api/login", Body: strings.Repeat("x", desktopConsoleMaxBodyBytes+1)})
	if response.Status != http.StatusRequestEntityTooLarge || calls != 0 {
		t.Fatalf("invalid envelope reached registered routes: status = %d calls = %d", response.Status, calls)
	}
	for _, body := range []string{"", "{", `{} {}`, `{"cookie":"forged"}`, `{"origin":"http://127.0.0.1:3457"}`, `{"username":"admin","password":"x","headers":{"Cookie":"forged"}}`} {
		response := console.Request(t.Context(), DesktopConsoleRequest{Method: http.MethodPost, Path: "/admin/api/login", Body: body})
		if response.Status != http.StatusBadRequest || response.CSRF != "" {
			t.Errorf("invalid login body = %d %s", response.Status, response.Body)
		}
	}
	response = console.Request(t.Context(), DesktopConsoleRequest{Method: http.MethodGet, Path: "/admin/api/auth/status?probe=valid%2Fquery", Body: strings.Repeat("x", desktopConsoleMaxBodyBytes)})
	if response.Status != http.StatusOK {
		t.Fatalf("body at native limit = %d %s", response.Status, response.Body)
	}
	requireDesktopConsoleSignedOut(t, console)
}

func TestDesktopConsoleBoundsResponseAndKeepsCookieHeadersPrivate(t *testing.T) {
	console, _, runtime := initializedDesktopConsoleForTest(t)
	credentials, err := runtime.service.Login(t.Context(), "admin", desktopConsoleTestPassword, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte("x"), 64<<10)
	for _, overflow := range []bool{false, true} {
		console.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			setAdminCredentialCookies(w, credentials, false)
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("X-Native-Secret", credentials.Token)
			w.WriteHeader(http.StatusOK)
			for range desktopConsoleMaxResponseBytes / len(chunk) {
				if _, err := w.Write(chunk); err != nil {
					t.Error(err)
				}
			}
			if overflow {
				if _, err := w.Write([]byte("x")); !errors.Is(err, errDesktopConsoleResponseTooLarge) {
					t.Error("oversized response was not bounded")
				}
			}
		})
		response := console.Request(t.Context(), DesktopConsoleRequest{Method: http.MethodGet, Path: "/admin/api/auth/session"})
		if overflow {
			if response.Status != http.StatusInternalServerError || response.CSRF != "" || !strings.Contains(response.Body, "response_too_large") {
				t.Fatalf("oversized response = %d", response.Status)
			}
		} else if response.Status != http.StatusOK || len(response.Body) != desktopConsoleMaxResponseBytes || response.CSRF != credentials.CSRFToken {
			t.Fatalf("bounded response = %d, bytes = %d", response.Status, len(response.Body))
		}
		encoded, err := json.Marshal(response)
		if err != nil || strings.Contains(string(encoded), credentials.Token) || strings.Contains(string(encoded), "Set-Cookie") {
			t.Fatalf("response leaked cookie headers: %v", err)
		}
	}
}

func TestDesktopConsoleCookieCaptureRequiresLiveSessionAndCSRF(t *testing.T) {
	for _, invalid := range []string{"session", "csrf", "revoked"} {
		t.Run(invalid, func(t *testing.T) {
			console, _, runtime := initializedDesktopConsoleForTest(t)
			credentials, err := runtime.service.Login(t.Context(), "admin", desktopConsoleTestPassword, "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			switch invalid {
			case "session":
				credentials.Token = strings.Repeat("x", 43)
			case "csrf":
				credentials.CSRFToken = strings.Repeat("x", 43)
			case "revoked":
				if err := runtime.service.Logout(t.Context(), credentials.Principal); err != nil {
					t.Fatal(err)
				}
			}
			registered := console.handler
			console.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				setAdminCredentialCookies(w, credentials, false)
				w.WriteHeader(http.StatusOK)
			})
			response := console.Request(t.Context(), DesktopConsoleRequest{Method: http.MethodPost, Path: "/admin/api/login", Body: `{}`})
			if response.Status != http.StatusUnauthorized || response.CSRF != "" {
				t.Fatalf("invalid handler credentials were retained: %d", response.Status)
			}
			console.handler = registered
			requireDesktopConsoleSignedOut(t, console)
		})
	}
}

func TestDesktopConsoleLogoutPasswordAndRecoveryClearSession(t *testing.T) {
	for _, action := range []string{"logout", "password", "recovery"} {
		t.Run(action, func(t *testing.T) {
			console, _, runtime := initializedDesktopConsoleForTest(t)
			login := desktopConsoleLoginForTest(t, console, "admin", desktopConsoleTestPassword)
			credentials, err := console.Credentials(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			request := DesktopConsoleRequest{Method: http.MethodPost, Path: "/admin/api/logout", Body: `{}`, CSRF: login.CSRF}
			switch action {
			case "password":
				request.Path = "/admin/api/auth/password"
				request.Body = `{"oldPassword":"` + desktopConsoleTestPassword + `","newPassword":"Changed console password 8!"}`
			case "recovery":
				code, err := runtime.service.IssueRecoveryCode(t.Context(), credentials.Principal, desktopConsoleTestPassword, "127.0.0.1")
				if err != nil {
					t.Fatal(err)
				}
				request.Path = "/admin/api/auth/recovery"
				request.CSRF = ""
				request.Body = `{"recoveryCode":"` + code + `","newPassword":"Recovered console password 8!","confirmPassword":"Recovered console password 8!"}`
			}
			response := console.Request(t.Context(), request)
			if response.Status != http.StatusOK || response.CSRF != "" {
				t.Fatalf("native %s = %d %s", action, response.Status, response.Body)
			}
			requireDesktopConsoleSignedOut(t, console)
			if _, err := runtime.service.Authenticate(t.Context(), credentials.Token); !errors.Is(err, adminauth.ErrUnauthorized) {
				t.Fatalf("native %s retained the service session: %v", action, err)
			}
		})
	}
}

func TestDesktopConsoleUnauthorizedClearsOnlyLocalSession(t *testing.T) {
	console, _, runtime := initializedDesktopConsoleForTest(t)
	desktopConsoleLoginForTest(t, console, "admin", desktopConsoleTestPassword)
	credentials, err := console.Credentials(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	response := console.Request(t.Context(), DesktopConsoleRequest{Method: http.MethodPost, Path: "/admin/api/login", Body: `{"password":"wrong password"}`})
	if response.Status != http.StatusUnauthorized || response.CSRF != "" {
		t.Fatalf("failed login = %d %s", response.Status, response.Body)
	}
	requireDesktopConsoleSignedOut(t, console)
	if _, err := runtime.service.Authenticate(t.Context(), credentials.Token); err != nil {
		t.Fatalf("clearing local session mutated service authority: %v", err)
	}
}

func TestDesktopConsoleCredentialsRefreshAndClear(t *testing.T) {
	console, _, runtime := initializedDesktopConsoleForTest(t)
	setDesktopSecurityRuntimeForTest(t, runtime)
	desktopConsoleLoginForTest(t, console, "admin", desktopConsoleTestPassword)
	credentials, err := console.Credentials(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	device, err := EnrollDesktopDeviceAfterAuthentication(t.Context(), credentials, adminauth.TrustedDeviceMetadata{Name: "Native console"}, "")
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := console.Credentials(t.Context())
	if err != nil || refreshed.Principal.DeviceID != device.ID || credentials.Principal.DeviceID != "" {
		t.Fatalf("credentials did not refresh the live device binding: %v", err)
	}
	if err := ActivateDesktopDevice(t.Context(), credentials, device.ID); err != nil {
		t.Fatalf("activation did not use the refreshed principal: %v", err)
	}
	refreshed.Principal.Roles[0] = adminauth.RoleViewer
	refreshed, err = console.Credentials(t.Context())
	if err != nil || !refreshed.Principal.HasPermission(adminauth.PermissionSecurityWrite) {
		t.Fatalf("returned principal aliased native authority: %v", err)
	}
	console.Clear()
	requireDesktopConsoleSignedOut(t, console)
	if _, err := runtime.service.Authenticate(t.Context(), credentials.Token); err != nil {
		t.Fatalf("Clear revoked the live service session: %v", err)
	}
}

func TestDesktopConsoleCredentialsRejectRevokedExpiredAndInvalidCSRF(t *testing.T) {
	for _, action := range []string{"revoked credentials", "revoked request", "expired credentials", "expired request", "invalid csrf"} {
		t.Run(action, func(t *testing.T) {
			console, _, runtime := initializedDesktopConsoleForTest(t)
			desktopConsoleLoginForTest(t, console, "admin", desktopConsoleTestPassword)
			credentials, err := console.Credentials(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case strings.HasPrefix(action, "revoked"):
				if err := runtime.service.Logout(t.Context(), credentials.Principal); err != nil {
					t.Fatal(err)
				}
			case strings.HasPrefix(action, "expired"):
				runtime.service, err = adminauth.NewService(runtime.store, adminauth.Options{Now: func() time.Time { return credentials.ExpiresAt }})
				if err != nil {
					t.Fatal(err)
				}
			default:
				console.csrfToken = strings.Repeat("x", 43)
			}
			if strings.HasSuffix(action, "request") {
				response := console.Request(t.Context(), DesktopConsoleRequest{Method: http.MethodGet, Path: "/admin/api/auth/session"})
				if response.Status != http.StatusUnauthorized || response.CSRF != "" {
					t.Fatalf("stale native request = %d", response.Status)
				}
			} else if _, err := console.Credentials(t.Context()); !errors.Is(err, adminauth.ErrUnauthorized) && !errors.Is(err, adminauth.ErrForbidden) {
				t.Fatalf("stale native credentials = %v", err)
			}
			requireDesktopConsoleSignedOut(t, console)
		})
	}
}

func TestDesktopConsoleSerializesBoundCallsAndSessionAccess(t *testing.T) {
	console, _, runtime := initializedDesktopConsoleForTest(t)
	desktopConsoleLoginForTest(t, console, "admin", desktopConsoleTestPassword)
	credentials, err := console.Credentials(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var active atomic.Int32
	registered := console.handler
	console.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if active.Add(1) != 1 {
			t.Error("native handlers ran concurrently")
		}
		defer active.Add(-1)
		registered.ServeHTTP(w, r)
	})
	var wait sync.WaitGroup
	for range 12 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range 4 {
				response := console.Request(context.Background(), DesktopConsoleRequest{Method: http.MethodGet, Path: "/admin/api/auth/session"})
				if response.Status != http.StatusOK && response.Status != http.StatusUnauthorized {
					t.Errorf("concurrent request = %d", response.Status)
				}
				_, _ = console.Credentials(context.Background())
			}
		}()
	}
	wait.Add(1)
	go func() { defer wait.Done(); console.Clear() }()
	wait.Wait()
	requireDesktopConsoleSignedOut(t, console)
	if _, err := runtime.service.Authenticate(t.Context(), credentials.Token); err != nil {
		t.Fatal(err)
	}
}

func TestDesktopConsoleStaticHandlerNeverDispatchesAPI(t *testing.T) {
	console, _, runtime := initializedDesktopConsoleForTest(t)
	desktopConsoleLoginForTest(t, console, "admin", desktopConsoleTestPassword)
	credentials, err := console.Credentials(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	token, err := runtime.desktopHandoffs.create(credentials)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/admin/", http.StatusOK},
		{http.MethodHead, "/admin/", http.StatusOK},
		{http.MethodGet, "/admin/assets/providers/openai.png", http.StatusOK},
		{http.MethodHead, "/admin/assets/providers/openai.png", http.StatusOK},
		{http.MethodGet, "/admin/assets/providers/deepinfra.png", http.StatusOK},
		{http.MethodGet, "/admin/assets/providers/electronhub.png", http.StatusOK},
		{http.MethodPost, "/admin/", http.StatusMethodNotAllowed},
		{http.MethodGet, "/admin/api/auth/session", http.StatusNotFound},
		{http.MethodPost, "/admin/api/auth/desktop-handoff", http.StatusNotFound},
		{http.MethodGet, "/admin/../admin/api/auth/session", http.StatusNotFound},
		{http.MethodGet, "/admin/", http.StatusOK},
	} {
		request := httptest.NewRequest(target.method, "http://wails.localhost"+target.path, strings.NewReader(`{"token":"`+token+`"}`))
		request.AddCookie(&http.Cookie{Name: adminapi.SessionCookieName, Value: credentials.Token})
		recorder := httptest.NewRecorder()
		console.ServeHTTP(recorder, request)
		if recorder.Code != target.status || len(recorder.Result().Cookies()) != 0 || strings.Contains(recorder.Body.String(), credentials.Token) {
			t.Fatalf("static request %s %s = %d", target.method, target.path, recorder.Code)
		}
		if target.method == http.MethodHead && recorder.Body.Len() != 0 {
			t.Fatal("static HEAD returned a body")
		}
		if strings.HasSuffix(target.path, ".png") && target.method == http.MethodGet && recorder.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("provider PNG content type = %q", recorder.Header().Get("Content-Type"))
		}
	}
	response := console.Request(t.Context(), DesktopConsoleRequest{Method: http.MethodPost, Path: "/admin/api/auth/desktop-handoff", Body: `{"token":"` + token + `"}`})
	if response.Status != http.StatusOK {
		t.Fatalf("static API denial consumed the native handoff: %d %s", response.Status, response.Body)
	}
}

func TestDesktopNativeBootstrapAndRecoveryWrappersDoNotMintHandoffs(t *testing.T) {
	_, runtime := newAdminAuthTestMux(t)
	setDesktopSecurityRuntimeForTest(t, runtime)
	runtime.desktopHandoffs.random = iotest.ErrReader(errors.New("handoff must not be minted before acknowledgement"))
	code, credentials, err := CompleteDesktopBootstrapCredentials(t.Context(), "one time bootstrap secret", desktopConsoleTestPassword)
	if err != nil || code == "" || credentials.Token == "" || len(runtime.desktopHandoffs.entries) != 0 {
		t.Fatalf("native bootstrap minted a handoff or lost credentials: %v", err)
	}
	console := desktopConsoleForTest(t, runtime)
	requireDesktopConsoleSignedOut(t, console)
	desktopConsoleLoginForTest(t, console, "admin", desktopConsoleTestPassword)
	if _, err := runtime.service.CreateLocalUser(t.Context(), credentials.Principal, "native-operator", "Operator console password 8!", []adminauth.Role{adminauth.RoleViewer}); err != nil {
		t.Fatal(err)
	}
	local, err := AuthenticateDesktopLocal(t.Context(), "native-operator", "Operator console password 8!")
	if err != nil || local.Principal.Username != "native-operator" || local.Principal.HasPermission(adminauth.PermissionSecurityWrite) {
		t.Fatalf("username-aware local authentication = %v", err)
	}
	if _, err := AuthenticateDesktopPassword(t.Context(), desktopConsoleTestPassword); err != nil {
		t.Fatal("legacy password wrapper stopped working", err)
	}
	replacement, restart, err := RecoverDesktopPassword(t.Context(), code, "Recovered native password 8!")
	if err != nil || replacement == "" || replacement == code || restart {
		t.Fatalf("native recovery result = restart %t, error %v", restart, err)
	}
	requireDesktopConsoleSignedOut(t, console)
	state, err := runtime.service.State(t.Context())
	if err != nil || len(state.Sessions) != 0 || len(runtime.desktopHandoffs.entries) != 0 {
		t.Fatalf("native recovery silently signed in: %v", err)
	}
	if _, restart, err := RecoverDesktopPassword(t.Context(), code, "Another recovered password 8!"); (!errors.Is(err, adminauth.ErrUnauthorized) && !errors.Is(err, adminauth.ErrRateLimited)) || restart {
		t.Fatalf("native recovery replay = %v", err)
	}
}

func TestDesktopNativeWrappersPreservePreparedRecoveryProof(t *testing.T) {
	for _, operation := range []string{"bootstrap", "recovery"} {
		t.Run(operation, func(t *testing.T) {
			_, runtime := newAdminAuthTestMux(t)
			code := ""
			if operation == "recovery" {
				_, recoveryCode, err := runtime.service.CompleteBootstrapWithRecovery(t.Context(), "one time bootstrap secret", desktopConsoleTestPassword)
				if err != nil {
					t.Fatal(err)
				}
				code = recoveryCode
			}
			var err error
			prepared := &desktopConsolePreparedRepository{Repository: runtime.store}
			runtime.service, err = adminauth.NewService(prepared, adminauth.Options{
				PasswordParams:    adminauth.PasswordParams{Memory: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32},
				BootstrapPassword: "one time bootstrap secret",
			})
			if err != nil {
				t.Fatal(err)
			}
			prepared.prepare = true
			setDesktopSecurityRuntimeForTest(t, runtime)
			if operation == "bootstrap" {
				replacement, credentials, err := CompleteDesktopBootstrapCredentials(t.Context(), "one time bootstrap secret", desktopConsoleTestPassword)
				if !errors.Is(err, adminauth.ErrMutationPrepared) || replacement == "" || credentials.Token != "" || len(runtime.desktopHandoffs.entries) != 0 {
					t.Fatalf("prepared bootstrap lost recovery proof: %v", err)
				}
			} else {
				replacement, restart, err := RecoverDesktopPassword(t.Context(), code, "Recovered native password 8!")
				if !errors.Is(err, adminauth.ErrMutationPrepared) || !restart || replacement == "" || replacement == code || len(runtime.desktopHandoffs.entries) != 0 {
					t.Fatalf("prepared recovery lost replacement proof: %v", err)
				}
			}
		})
	}
}

func TestDesktopConsolePreparedRecoveryReturnsReplacementOnceAndClearsSession(t *testing.T) {
	_, runtime := newAdminAuthTestMux(t)
	_, code, err := runtime.service.CompleteBootstrapWithRecovery(t.Context(), "one time bootstrap secret", desktopConsoleTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	repository := &desktopConsolePreparedRepository{Repository: runtime.store}
	runtime.service, err = adminauth.NewService(repository, adminauth.Options{
		PasswordParams: adminauth.PasswordParams{Memory: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32},
	})
	if err != nil {
		t.Fatal(err)
	}
	console := desktopConsoleForTest(t, runtime)
	desktopConsoleLoginForTest(t, console, "admin", desktopConsoleTestPassword)
	credentials, err := console.Credentials(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	repository.prepare = true
	request := DesktopConsoleRequest{Method: http.MethodPost, Path: "/admin/api/auth/recovery", Body: `{"recoveryCode":"` + code + `","newPassword":"Prepared recovery password 8!","confirmPassword":"Prepared recovery password 8!"}`}
	response := console.Request(t.Context(), request)
	var body struct {
		Error string `json:"error"`
		Data  struct {
			RecoveryCode string `json:"recoveryCode"`
			Restart      bool   `json:"restartRequired"`
			ShownOnce    bool   `json:"shownOnce"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatal(err)
	}
	if response.Status != http.StatusServiceUnavailable || response.CSRF != "" || body.Error != "security_restart_required" || body.Data.RecoveryCode == "" || body.Data.RecoveryCode == code || !body.Data.Restart || !body.Data.ShownOnce {
		t.Fatalf("prepared recovery response did not preserve the replacement: %d", response.Status)
	}
	if strings.Contains(response.Body, credentials.Token) || strings.Contains(response.Body, code) {
		t.Fatal("prepared recovery echoed an old credential")
	}
	requireDesktopConsoleSignedOut(t, console)
	replay := console.Request(t.Context(), request)
	if replay.Status != http.StatusServiceUnavailable || replay.CSRF != "" || strings.Contains(replay.Body, body.Data.RecoveryCode) || strings.Contains(replay.Body, `"recoveryCode"`) {
		t.Fatal("prepared recovery repeated the one-time replacement")
	}
	restarted, err := adminauth.NewService(runtime.store, adminauth.Options{
		PasswordParams: adminauth.PasswordParams{Memory: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.RecoverWithCode(t.Context(), code, "Invalid replay password 8!", "127.0.0.1"); !errors.Is(err, adminauth.ErrUnauthorized) {
		t.Fatalf("prepared mutation retained its old recovery code: %v", err)
	}
	if _, err := restarted.RecoverWithCode(t.Context(), body.Data.RecoveryCode, "Replacement recovery password 8!", "127.0.0.1"); err != nil {
		t.Fatalf("prepared replacement was not authoritative after restart: %v", err)
	}
}

type desktopConsolePreparedRepository struct {
	adminauth.Repository
	prepare bool
}

func (repository *desktopConsolePreparedRepository) Apply(ctx context.Context, event adminauth.AuditEvent, mutate func(*adminauth.State) error) error {
	if err := repository.Repository.Apply(ctx, event, mutate); err != nil {
		return err
	}
	if repository.prepare {
		return adminauth.ErrMutationPrepared
	}
	return nil
}

func TestDesktopConsoleCancellationAndZeroValueFailClosed(t *testing.T) {
	for _, console := range []*DesktopConsole{nil, {}} {
		response := console.Request(t.Context(), DesktopConsoleRequest{Method: http.MethodGet, Path: "/admin/api/auth/status"})
		if response.Status != http.StatusServiceUnavailable || response.CSRF != "" {
			t.Fatal("unconfigured console served a request")
		}
		if _, err := console.Credentials(t.Context()); !errors.Is(err, adminauth.ErrUnauthorized) {
			t.Fatal("unconfigured console returned credentials", err)
		}
		console.Clear()
		recorder := httptest.NewRecorder()
		console.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://wails.localhost/admin/", nil))
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatal("unconfigured console served static content")
		}
	}
	console, _, _ := initializedDesktopConsoleForTest(t)
	called := false
	console.handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	response := console.Request(ctx, DesktopConsoleRequest{Method: http.MethodPost, Path: "/admin/api/login", Body: `{}`})
	if response.Status != http.StatusRequestTimeout || called {
		t.Fatalf("cancelled request was dispatched: %d", response.Status)
	}
}

func TestDesktopConsoleResponseWriterPreservesCommittedHeaders(t *testing.T) {
	writer := newDesktopConsoleResponseWriter()
	writer.Header().Set("Content-Type", "text/plain")
	writer.WriteHeader(http.StatusCreated)
	writer.Header().Set("Content-Type", "changed")
	writer.Header().Set("Set-Cookie", "forged=late")
	writer.WriteHeader(http.StatusUnauthorized)
	if _, err := io.WriteString(writer, "body"); err != nil {
		t.Fatal(err)
	}
	response := writer.response()
	if response.Status != http.StatusCreated || response.ContentType != "text/plain" || response.Body != "body" || writer.committed.Get("Set-Cookie") != "" {
		t.Fatal("native writer did not preserve ordinary HTTP commit semantics")
	}
}
