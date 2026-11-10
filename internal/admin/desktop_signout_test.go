package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"routenest/internal/adminapi"
	"routenest/internal/adminauth"
)

func TestDesktopSignOutRevokesSessionAndPausesAutomaticSignIn(t *testing.T) {
	mux, runtime := newAdminAuthTestMux(t)
	credentials, err := runtime.service.CompleteBootstrap(t.Context(), "one time bootstrap secret", "DesktopSignOut8!")
	if err != nil {
		t.Fatal(err)
	}
	device, err := runtime.service.EnrollTrustedDeviceAfterAuthentication(t.Context(), credentials.Principal, adminauth.TrustedDeviceMetadata{Name: "Desktop"}, "")
	if err != nil {
		t.Fatal(err)
	}
	enrolling, err := runtime.service.Authenticate(t.Context(), credentials.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.service.ActivateTrustedDevice(t.Context(), enrolling, device.ID); err != nil {
		t.Fatal(err)
	}
	principal, err := runtime.service.Authenticate(t.Context(), credentials.Token)
	if err != nil {
		t.Fatal(err)
	}
	request := adminTestRequest(http.MethodPost, "/admin/api/auth/desktop-signout", `{}`)
	request.AddCookie(&http.Cookie{Name: adminapi.SessionCookieName, Value: credentials.Token})
	request.Header.Set("X-RouteNest-CSRF", credentials.CSRFToken)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("sign-out status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if _, err := runtime.service.Authenticate(t.Context(), credentials.Token); err == nil {
		t.Fatal("desktop sign-out retained the session")
	}
	state, err := runtime.service.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if state.TrustedDevices[principal.DeviceID].AutomaticSignIn {
		t.Fatal("desktop sign-out retained automatic sign-in")
	}
	if _, err := runtime.service.AuthenticateTrustedDevice(t.Context(), device.ID, device.Secret, "127.0.0.1"); err == nil {
		t.Fatal("desktop sign-out allowed immediate automatic reauthentication")
	}
}

func TestDesktopSignOutRequiresSessionOriginAndCSRF(t *testing.T) {
	mux, runtime := newAdminAuthTestMux(t)
	credentials, err := runtime.service.CompleteBootstrap(t.Context(), "one time bootstrap secret", "DesktopSignOut8!")
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name, origin, csrf string
		authenticated      bool
		status             int
	}{
		{"anonymous", "http://127.0.0.1:3457", "", false, http.StatusUnauthorized},
		{"no csrf", "http://127.0.0.1:3457", "", true, http.StatusForbidden},
		{"wrong csrf", "http://127.0.0.1:3457", "invalid", true, http.StatusForbidden},
		{"missing origin", "", credentials.CSRFToken, true, http.StatusForbidden},
		{"foreign origin", "http://attacker.invalid", credentials.CSRFToken, true, http.StatusForbidden},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			request := adminTestRequest(http.MethodPost, "/admin/api/auth/desktop-signout", `{}`)
			request.Header.Del("Origin")
			if scenario.origin != "" {
				request.Header.Set("Origin", scenario.origin)
			}
			request.Header.Set("X-RouteNest-CSRF", scenario.csrf)
			if scenario.authenticated {
				request.AddCookie(&http.Cookie{Name: adminapi.SessionCookieName, Value: credentials.Token})
			}
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, request)
			if recorder.Code != scenario.status {
				t.Fatalf("status = %d, want %d", recorder.Code, scenario.status)
			}
		})
	}
	if _, err := runtime.service.Authenticate(t.Context(), credentials.Token); err != nil {
		t.Fatal("denied sign-out requests revoked a legitimate session")
	}
}
