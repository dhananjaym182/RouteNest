package admin

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"routenest/internal/adminapi"
	"routenest/internal/adminauth"
)

func TestDesktopHandoffRejectsEveryOrdinaryHTTPRequest(t *testing.T) {
	mux, runtime := newAdminAuthTestMux(t)
	credentials, err := runtime.service.CompleteBootstrap(t.Context(), "one time bootstrap secret", "DesktopNegative8!")
	if err != nil {
		t.Fatal(err)
	}
	token, err := runtime.desktopHandoffs.create(credentials)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"loopback", "origin spoof", "no origin", "other origin", "null origin", "custom scheme", "remote", "forwarded", "authenticated", "query marker", "invalid json"} {
		t.Run(mode, func(t *testing.T) {
			request := adminTestRequest(http.MethodPost, "/admin/api/auth/desktop-handoff", `{"token":"`+token+`"}`)
			switch mode {
			case "origin spoof":
				request.Header.Set("Origin", "http://127.0.0.1:3457")
				request.Header.Set("Referer", "http://127.0.0.1:3457/admin/")
				request.Header.Set("Sec-Fetch-Site", "same-origin")
				request.Header.Set("Sec-Fetch-Mode", "same-origin")
				request.Header.Set("User-Agent", "RouteNest Desktop")
				request.Header.Set("X-RouteNest-Native", "true")
			case "no origin":
				request.Header.Del("Origin")
			case "other origin":
				request.Header.Set("Origin", "https://evil.example")
			case "null origin":
				request.Header.Set("Origin", "null")
			case "custom scheme":
				request.Header.Set("Origin", "wails://wails")
			case "remote":
				request.RemoteAddr = "192.0.2.10:42000"
			case "forwarded":
				request.Header.Set("Forwarded", "for=127.0.0.1;proto=http;host=127.0.0.1:3457")
				request.Header.Set("X-Forwarded-For", "127.0.0.1")
			case "authenticated":
				request.AddCookie(&http.Cookie{Name: adminapi.SessionCookieName, Value: credentials.Token})
				request.Header.Set("X-RouteNest-CSRF", credentials.CSRFToken)
			case "query marker":
				request.URL.RawQuery = "native=true&handoffContext=true"
			case "invalid json":
				request = adminTestRequest(http.MethodPost, "/admin/api/auth/desktop-handoff", "{")
			}
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusForbidden || len(recorder.Result().Cookies()) != 0 {
				t.Fatalf("ordinary request accepted: status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
	response := desktopConsoleForTest(t, runtime).Request(t.Context(), DesktopConsoleRequest{Method: http.MethodPost, Path: "/admin/api/auth/desktop-handoff", Body: `{"token":"` + token + `"}`})
	if response.Status != http.StatusOK {
		t.Fatalf("ordinary denial consumed a valid private handoff: %d %s", response.Status, response.Body)
	}
}

func TestDesktopHandoffMarkerIsBoundToExactRuntime(t *testing.T) {
	mux, runtime := newAdminAuthTestMux(t)
	_, other := newAdminAuthTestMux(t)
	credentials, err := runtime.service.CompleteBootstrap(t.Context(), "one time bootstrap secret", "DesktopNegative8!")
	if err != nil {
		t.Fatal(err)
	}
	token, err := runtime.desktopHandoffs.create(credentials)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{true, runtime.service, other, (*adminSecurityRuntime)(nil)} {
		request := adminTestRequest(http.MethodPost, "/admin/api/auth/desktop-handoff", `{"token":"`+token+`"}`)
		request = request.WithContext(context.WithValue(request.Context(), desktopHandoffContextKey{}, value))
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden || len(recorder.Result().Cookies()) != 0 {
			t.Fatalf("foreign private marker = %d %s", recorder.Code, recorder.Body.String())
		}
	}
	request := adminTestRequest(http.MethodPost, "/admin/api/auth/desktop-handoff", `{"token":"`+token+`"}`)
	security := adminauth.RequestSecurity{PeerIP: net.ParseIP("127.0.0.1"), ClientIP: net.ParseIP("127.0.0.1"), TrustedProxy: true}
	ctx := context.WithValue(request.Context(), adminSecurityRuntimeContextKey{}, runtime)
	ctx = context.WithValue(ctx, adminRequestSecurityContextKey{}, security)
	recorder := httptest.NewRecorder()
	handleDesktopSessionHandoff(recorder, request.WithContext(ctx))
	if recorder.Code != http.StatusForbidden || len(recorder.Result().Cookies()) != 0 {
		t.Fatalf("request-security metadata became a native capability: %d", recorder.Code)
	}
	response := desktopConsoleForTest(t, runtime).Request(t.Context(), DesktopConsoleRequest{Method: http.MethodPost, Path: "/admin/api/auth/desktop-handoff", Body: `{"token":"` + token + `"}`})
	if response.Status != http.StatusOK {
		t.Fatalf("wrong-runtime denial consumed a valid handoff: %d", response.Status)
	}
}

func TestDesktopNativeHandoffRevalidatesExpiryRevocationAndCSRF(t *testing.T) {
	for _, mode := range []string{"expired handoff", "expired session", "revoked session", "revoked device", "missing csrf", "wrong csrf", "mismatched csrf hash", "mismatched session", "mismatched user"} {
		t.Run(mode, func(t *testing.T) {
			_, runtime := newAdminAuthTestMux(t)
			credentials, err := runtime.service.CompleteBootstrap(t.Context(), "one time bootstrap secret", "DesktopNegative8!")
			if err != nil {
				t.Fatal(err)
			}
			original := credentials
			switch mode {
			case "missing csrf":
				credentials.CSRFToken = ""
			case "wrong csrf":
				credentials.CSRFToken = strings.Repeat("x", 43)
			case "mismatched csrf hash":
				credentials.Principal.CSRFHash = strings.Repeat("0", 64)
			case "mismatched session":
				credentials.Principal.SessionHash = strings.Repeat("0", 64)
			case "mismatched user":
				credentials.Principal.UserID = "other-user"
			}
			var device adminauth.TrustedDeviceCredential
			if mode == "revoked device" {
				device, err = runtime.service.EnrollTrustedDeviceAfterAuthentication(t.Context(), credentials.Principal, adminauth.TrustedDeviceMetadata{Name: "Desktop"}, "")
				if err != nil {
					t.Fatal(err)
				}
				credentials.Principal, err = runtime.service.Authenticate(t.Context(), credentials.Token)
				if err != nil {
					t.Fatal(err)
				}
				if err := runtime.service.ActivateTrustedDevice(t.Context(), credentials.Principal, device.ID); err != nil {
					t.Fatal(err)
				}
			}
			token, err := runtime.desktopHandoffs.create(credentials)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "expired handoff":
				runtime.desktopHandoffs.mu.Lock()
				entry := runtime.desktopHandoffs.entries[token]
				entry.expiresAt = time.Now().Add(-time.Second)
				runtime.desktopHandoffs.entries[token] = entry
				runtime.desktopHandoffs.mu.Unlock()
			case "expired session":
				runtime.service, err = adminauth.NewService(runtime.store, adminauth.Options{Now: func() time.Time { return credentials.ExpiresAt }})
				if err != nil {
					t.Fatal(err)
				}
			case "revoked session":
				if err := runtime.service.Logout(t.Context(), original.Principal); err != nil {
					t.Fatal(err)
				}
			case "revoked device":
				if err := runtime.service.RevokeTrustedDevice(t.Context(), credentials.Principal, "DesktopNegative8!", device.ID, "127.0.0.1"); err != nil {
					t.Fatal(err)
				}
			}
			console := desktopConsoleForTest(t, runtime)
			registered := console.handler
			console.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				registered.ServeHTTP(w, r)
				if len(w.Header().Values("Set-Cookie")) != 0 {
					t.Error("invalid handoff set credential cookies")
				}
			})
			for range 2 {
				response := console.Request(t.Context(), DesktopConsoleRequest{Method: http.MethodPost, Path: "/admin/api/auth/desktop-handoff", Body: `{"token":"` + token + `"}`})
				if response.Status != http.StatusUnauthorized || response.CSRF != "" {
					t.Fatalf("invalid handoff = %d %s", response.Status, response.Body)
				}
			}
			requireDesktopConsoleSignedOut(t, console)
		})
	}
}
