package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"

	"routenest/internal/adminapi"
)

func TestRecoveryCodeHTTPFlowRequiresCredentialAndRevokesSession(t *testing.T) {
	mux, runtime := newAdminAuthTestMux(t)
	credentials, code, err := runtime.service.CompleteBootstrapWithRecovery(t.Context(), "one time bootstrap secret", "AdminRecovery8!")
	if err != nil {
		t.Fatal(err)
	}

	bad := httptest.NewRecorder()
	mux.ServeHTTP(bad, adminTestRequest(http.MethodPost, "/admin/api/auth/recovery", `{"recoveryCode":"wrong","newPassword":"NewRecovery8!","confirmPassword":"NewRecovery8!"}`))
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("invalid code status=%d body=%s", bad.Code, bad.Body.String())
	}

	recorder := httptest.NewRecorder()
	body := `{"recoveryCode":"` + code + `","newPassword":"NewRecovery8!","confirmPassword":"NewRecovery8!"}`
	mux.ServeHTTP(recorder, adminTestRequest(http.MethodPost, "/admin/api/auth/recovery", body))
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), code) {
		t.Fatalf("recovery status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			RecoveryCode string `json:"recoveryCode"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.Data.RecoveryCode == "" {
		t.Fatalf("replacement response=%s err=%v", recorder.Body.String(), err)
	}

	request := adminTestRequest(http.MethodGet, "/admin/api/auth/session", "")
	request.AddCookie(&http.Cookie{Name: adminapi.SessionCookieName, Value: credentials.Token})
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("old session status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	replay := httptest.NewRecorder()
	mux.ServeHTTP(replay, adminTestRequest(http.MethodPost, "/admin/api/auth/recovery", body))
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}
}

func TestRecoveryCodeIssueRequiresSessionCSRFAndCurrentPassword(t *testing.T) {
	mux, runtime := newAdminAuthTestMux(t)
	credentials, err := runtime.service.CompleteBootstrap(t.Context(), "one time bootstrap secret", "AdminRecovery8!")
	if err != nil {
		t.Fatal(err)
	}
	request := func(csrf, password string) *http.Request {
		req := adminTestRequest(http.MethodPost, "/admin/api/auth/recovery-code", `{"currentPassword":"`+password+`"}`)
		req.AddCookie(&http.Cookie{Name: adminapi.SessionCookieName, Value: credentials.Token})
		if csrf != "" {
			req.Header.Set("X-RouteNest-CSRF", csrf)
		}
		return req
	}

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request("", "AdminRecovery8!"))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request(credentials.CSRFToken, "wrong password"))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request(credentials.CSRFToken, "AdminRecovery8!"))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"shownOnce":true`) {
		t.Fatalf("issue status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDesktopHandoffIsLoopbackBoundAndSingleUse(t *testing.T) {
	mux, runtime := newAdminAuthTestMux(t)
	credentials, err := runtime.service.CompleteBootstrap(t.Context(), "one time bootstrap secret", "AdminRecovery8!")
	if err != nil {
		t.Fatal(err)
	}
	token, err := runtime.desktopHandoffs.create(credentials)
	if err != nil {
		t.Fatal(err)
	}
	request := adminTestRequest(http.MethodPost, "/admin/api/auth/desktop-handoff", `{"token":"`+token+`"}`)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("handoff status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if cookieNamed(t, recorder, adminapi.SessionCookieName).Value == "" {
		t.Fatal("handoff did not set session cookie")
	}
	replay := httptest.NewRecorder()
	mux.ServeHTTP(replay, adminTestRequest(http.MethodPost, "/admin/api/auth/desktop-handoff", `{"token":"`+token+`"}`))
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("handoff replay status=%d", replay.Code)
	}
}

func TestDesktopBootstrapReturnsRecoveryCodeWhenHandoffGenerationFails(t *testing.T) {
	_, runtime := newAdminAuthTestMux(t)
	runtime.desktopHandoffs.random = iotest.ErrReader(errors.New("injected handoff entropy failure"))
	recoveryCode, handoff, err := completeDesktopBootstrap(t.Context(), runtime, "one time bootstrap secret", "AdminRecovery8!")
	if !errors.Is(err, ErrDesktopHandoffUnavailable) || recoveryCode == "" || handoff != "" {
		t.Fatalf("desktop bootstrap = code %q, handoff %q, error %v", recoveryCode, handoff, err)
	}
	state, stateErr := runtime.service.State(t.Context())
	if stateErr != nil || !state.Initialized || state.RecoveryCodePHC == "" {
		t.Fatalf("administrator was not durably initialized: %+v, %v", state, stateErr)
	}
	if _, err := runtime.service.RecoverWithCode(t.Context(), recoveryCode, "RecoveredAdmin8!", "127.0.0.1"); err != nil {
		t.Fatalf("returned recovery code was unusable: %v", err)
	}
}
