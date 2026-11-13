package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"routenest/internal/adminapi"
	"routenest/internal/adminauth"
	"routenest/internal/platform"
	storeadminauth "routenest/internal/store/adminauth"
)

const bootstrapRecoveryBody = `{"secret":"one time bootstrap secret","password":"permanent admin password"}`

type bootstrapResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Data    struct {
		RecoveryCode    string `json:"recoveryCode"`
		ShownOnce       bool   `json:"shownOnce"`
		RestartRequired bool   `json:"restartRequired"`
		Principal       *struct {
			Username string `json:"username"`
		} `json:"principal"`
	} `json:"data"`
}

func decodeBootstrapResponse(t *testing.T, recorder *httptest.ResponseRecorder) bootstrapResponse {
	t.Helper()
	var response bootstrapResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("bootstrap response is not JSON: %v: %s", err, recorder.Body.String())
	}
	return response
}

// Browser/CLI first-run setup must issue the recovery code, once, in the same call that sets
// the permanent password, so "Forgot password?" works from the first sign-in.
func TestWebBootstrapIssuesRecoveryCodeOnce(t *testing.T) {
	mux, runtime := newAdminAuthTestMux(t)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, adminTestRequest(http.MethodPost, "/admin/api/auth/bootstrap", bootstrapRecoveryBody))
	if recorder.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	response := decodeBootstrapResponse(t, recorder)
	if !response.Success || response.Data.RecoveryCode == "" || !response.Data.ShownOnce || response.Data.Principal == nil || response.Data.Principal.Username != "admin" {
		t.Fatalf("bootstrap response = %s", recorder.Body.String())
	}
	if len(response.Data.RecoveryCode) < 32 {
		t.Fatalf("recovery code is too short to be a 256-bit credential: %d characters", len(response.Data.RecoveryCode))
	}
	if recorder.Result().Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("a response carrying a one-time secret must be no-store, got %q", recorder.Result().Header.Get("Cache-Control"))
	}
	sessionCookie := cookieNamed(t, recorder, adminapi.SessionCookieName)

	// Durable: the verifier exists, and the status endpoint now advertises "Forgot password?".
	state, err := runtime.service.State(t.Context())
	if err != nil || !state.Initialized || state.RecoveryCodePHC == "" {
		t.Fatalf("recovery verifier was not stored with the password: %+v, %v", state, err)
	}
	status := httptest.NewRecorder()
	mux.ServeHTTP(status, adminTestRequest(http.MethodGet, "/admin/api/auth/status", ""))
	if !strings.Contains(status.Body.String(), `"recoveryCodeAvailable":true`) {
		t.Fatalf("status does not advertise the recovery code: %s", status.Body.String())
	}

	// Shown once: neither the verifier, the plaintext, nor any later read exposes it.
	if strings.Contains(status.Body.String(), response.Data.RecoveryCode) || strings.Contains(status.Body.String(), state.RecoveryCodePHC) {
		t.Fatalf("status leaked recovery material: %s", status.Body.String())
	}
	session := adminTestRequest(http.MethodGet, "/admin/api/auth/session", "")
	session.AddCookie(sessionCookie)
	sessionRecorder := httptest.NewRecorder()
	mux.ServeHTTP(sessionRecorder, session)
	if sessionRecorder.Code != http.StatusOK || strings.Contains(sessionRecorder.Body.String(), response.Data.RecoveryCode) {
		t.Fatalf("session read = %d %s", sessionRecorder.Code, sessionRecorder.Body.String())
	}

	// The returned code really resets the password (and only once).
	reset := `{"recoveryCode":"` + response.Data.RecoveryCode + `","newPassword":"RecoveredAdmin8!","confirmPassword":"RecoveredAdmin8!"}`
	used := httptest.NewRecorder()
	mux.ServeHTTP(used, adminTestRequest(http.MethodPost, "/admin/api/auth/recovery", reset))
	if used.Code != http.StatusOK {
		t.Fatalf("the code issued at setup could not reset the password: %d %s", used.Code, used.Body.String())
	}
	replay := httptest.NewRecorder()
	mux.ServeHTTP(replay, adminTestRequest(http.MethodPost, "/admin/api/auth/recovery", reset))
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("a used recovery code must not work twice: %d %s", replay.Code, replay.Body.String())
	}
}

func TestWebBootstrapFailuresNeverIssueACode(t *testing.T) {
	mux, runtime := newAdminAuthTestMux(t)
	wrong := httptest.NewRecorder()
	mux.ServeHTTP(wrong, adminTestRequest(http.MethodPost, "/admin/api/auth/bootstrap", `{"secret":"not the secret at all","password":"permanent admin password"}`))
	if wrong.Code == http.StatusOK || strings.Contains(wrong.Body.String(), "recoveryCode") {
		t.Fatalf("wrong secret = %d %s", wrong.Code, wrong.Body.String())
	}
	if state, err := runtime.service.State(t.Context()); err != nil || state.Initialized || state.RecoveryCodePHC != "" {
		t.Fatalf("a rejected bootstrap changed state: %+v, %v", state, err)
	}
	ok := httptest.NewRecorder()
	mux.ServeHTTP(ok, adminTestRequest(http.MethodPost, "/admin/api/auth/bootstrap", bootstrapRecoveryBody))
	again := httptest.NewRecorder()
	mux.ServeHTTP(again, adminTestRequest(http.MethodPost, "/admin/api/auth/bootstrap", bootstrapRecoveryBody))
	if ok.Code != http.StatusOK || again.Code == http.StatusOK || strings.Contains(again.Body.String(), "recoveryCode") {
		t.Fatalf("second bootstrap must be refused without a code: first=%d second=%d %s", ok.Code, again.Code, again.Body.String())
	}
}

// preparedRepository persists the mutation, then reports the "journal written, snapshot not
// finished" outcome, the case where the plaintext code must still reach the user exactly once.
type preparedRepository struct {
	adminauth.Repository
	armed *bool
}

func (r preparedRepository) Apply(ctx context.Context, event adminauth.AuditEvent, mutate func(*adminauth.State) error) error {
	if err := r.Repository.Apply(ctx, event, mutate); err != nil {
		return err
	}
	if *r.armed {
		return fmt.Errorf("injected snapshot failure: %w", adminauth.ErrMutationPrepared)
	}
	return nil
}

func TestWebBootstrapPreparedMutationReturnsCodeWithRestartRequired(t *testing.T) {
	dir := t.TempDir()
	if err := platform.EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	repo, err := storeadminauth.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	armed := false
	service, err := adminauth.NewService(preparedRepository{Repository: repo, armed: &armed}, adminauth.Options{
		PasswordParams:    adminauth.PasswordParams{Memory: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32},
		BootstrapPassword: "one time bootstrap secret",
	})
	if err != nil {
		_ = repo.Close()
		t.Fatal(err)
	}
	canonical, _ := url.Parse("http://127.0.0.1:3457")
	runtime := &adminSecurityRuntime{service: service, store: repo, requestOptions: adminauth.RequestSecurityOptions{CanonicalURL: canonical}}
	mux := http.NewServeMux()
	registerRoutesWithSecurity(mux, runtime)
	armed = true // construction journals the pending bootstrap; inject only on completion

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, adminTestRequest(http.MethodPost, "/admin/api/auth/bootstrap", bootstrapRecoveryBody))
	response := decodeBootstrapResponse(t, recorder)
	if recorder.Code != http.StatusServiceUnavailable || response.Error != "security_restart_required" || response.Data.RecoveryCode == "" || !response.Data.RestartRequired || !response.Data.ShownOnce {
		t.Fatalf("prepared bootstrap = %d %s", recorder.Code, recorder.Body.String())
	}
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Value != "" && cookie.MaxAge >= 0 {
			t.Fatalf("a restart-required response must not leave a live credential cookie: %+v", cookie)
		}
	}
}
