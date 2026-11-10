package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"routenest/internal/adminapi"
)

func TestTrustedDeviceHTTPManagementRequiresAuthenticationCSRFAndPassword(t *testing.T) {
	mux, runtime := newAdminAuthTestMux(t)
	credentials, err := runtime.service.CompleteBootstrap(t.Context(), "one time bootstrap secret", "TrustedDevice8!")
	if err != nil {
		t.Fatal(err)
	}

	anonymous := httptest.NewRecorder()
	mux.ServeHTTP(anonymous, adminTestRequest(http.MethodGet, "/admin/api/auth/trusted-devices", ""))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list status = %d", anonymous.Code)
	}

	listRequest := adminTestRequest(http.MethodGet, "/admin/api/auth/trusted-devices", "")
	listRequest.AddCookie(&http.Cookie{Name: adminapi.SessionCookieName, Value: credentials.Token})
	list := httptest.NewRecorder()
	mux.ServeHTTP(list, listRequest)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"devices":[]`) {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}

	revokeAllRequest := func(password string, csrf bool) *http.Request {
		request := adminTestRequest(http.MethodPost, "/admin/api/auth/trusted-devices/revoke-all", `{"currentPassword":"`+password+`"}`)
		request.AddCookie(&http.Cookie{Name: adminapi.SessionCookieName, Value: credentials.Token})
		if csrf {
			request.Header.Set("X-RouteNest-CSRF", credentials.CSRFToken)
		}
		return request
	}
	missingCSRF := httptest.NewRecorder()
	mux.ServeHTTP(missingCSRF, revokeAllRequest("TrustedDevice8!", false))
	if missingCSRF.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d", missingCSRF.Code)
	}
	wrongPassword := httptest.NewRecorder()
	mux.ServeHTTP(wrongPassword, revokeAllRequest("wrong password", true))
	if wrongPassword.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status=%d body=%s", wrongPassword.Code, wrongPassword.Body.String())
	}
	success := httptest.NewRecorder()
	mux.ServeHTTP(success, revokeAllRequest("TrustedDevice8!", true))
	if success.Code != http.StatusOK {
		t.Fatalf("revoke all status=%d body=%s", success.Code, success.Body.String())
	}
}

func TestSessionMutationRejectsMissingOriginEvenFromLoopback(t *testing.T) {
	mux, runtime := newAdminAuthTestMux(t)
	credentials, err := runtime.service.CompleteBootstrap(t.Context(), "one time bootstrap secret", "StrictOrigin8!")
	if err != nil {
		t.Fatal(err)
	}
	request := adminTestRequest(http.MethodPost, "/admin/api/logout", `{}`)
	request.Header.Del("Origin")
	request.AddCookie(&http.Cookie{Name: adminapi.SessionCookieName, Value: credentials.Token})
	request.Header.Set("X-RouteNest-CSRF", credentials.CSRFToken)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "same_origin_required") {
		t.Fatalf("missing-origin session mutation status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
