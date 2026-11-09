package admin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"routenest/internal/adminauth"
	"routenest/internal/adminsso"
	"routenest/internal/httpx"
)

type adminAuthStatusData struct {
	Initialized            bool                     `json:"initialized"`
	BootstrapRequired      bool                     `json:"bootstrapRequired"`
	RecoveryRequired       bool                     `json:"recoveryRequired"`
	LegacyRotationRequired bool                     `json:"legacyRotationRequired"`
	RecoveryCodeAvailable  bool                     `json:"recoveryCodeAvailable"`
	Readiness              adminauth.Readiness      `json:"readiness"`
	SSOProviders           []adminsso.LoginProvider `json:"ssoProviders,omitempty"`
}

func handleAdminAuthStatus(w http.ResponseWriter, r *http.Request) {
	runtime, ok := adminSecurityRuntimeFromRequest(r)
	if !ok {
		writeAdminSecurityError(w, http.StatusServiceUnavailable, "security_unavailable")
		return
	}
	state, err := runtime.service.State(r.Context())
	if err != nil {
		writeAdminAuthOperationError(w, err)
		return
	}
	legacyRotation := false
	if user, ok := state.Users[adminauth.BreakGlassID]; ok {
		legacyRotation = user.Legacy != nil
	}
	readiness := runtime.service.Readiness()
	recoveryCodeAvailable, err := runtime.service.RecoveryCodeStatus(r.Context())
	if err != nil {
		writeAdminAuthOperationError(w, err)
		return
	}
	var ssoProviders []adminsso.LoginProvider
	if runtime.sso != nil {
		if providers, providerErr := runtime.sso.LoginProviders(r.Context()); providerErr == nil {
			ssoProviders = providers
		}
		if oidc, saml, providerErr := runtime.sso.Readiness(r.Context()); providerErr == nil {
			readiness.OIDCProviders = oidc
			readiness.SAMLProviders = saml
		}
	}
	readiness.TransportReady = true
	readiness.RoutePolicyReady = true
	readiness.CSRFReady = true
	readiness.Recompute()
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: adminAuthStatusData{
		Initialized:            state.Initialized,
		BootstrapRequired:      !state.Initialized,
		RecoveryRequired:       state.RecoveryPHC != "",
		LegacyRotationRequired: legacyRotation,
		RecoveryCodeAvailable:  recoveryCodeAvailable,
		Readiness:              readiness,
		SSOProviders:           ssoProviders,
	}})
}

func handleAdminBootstrap(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Secret   string `json:"secret"`
		Password string `json:"password"`
	}
	if !decodeAdminJSON(w, r, &request) {
		return
	}
	runtime, security, ok := authRuntimeAndSecurity(w, r)
	if !ok {
		return
	}
	credentials, err := runtime.service.CompleteBootstrap(r.Context(), request.Secret, request.Password, security.ClientIP.String())
	if err != nil {
		writeAdminAuthOperationError(w, err)
		return
	}
	setAdminCredentialCookies(w, credentials, security.Secure)
	writeAdminCredentialsResponse(w, credentials)
}

func handleAdminLoginSecure(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeAdminJSON(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.Username) == "" {
		request.Username = adminauth.BreakGlassUsername
	}
	runtime, security, ok := authRuntimeAndSecurity(w, r)
	if !ok {
		return
	}
	credentials, err := runtime.service.Login(r.Context(), request.Username, request.Password, security.ClientIP.String())
	if err != nil {
		if errors.Is(err, adminauth.ErrPasswordRotationRequired) {
			httpx.WriteAPI(w, http.StatusConflict, httpx.Response{Error: "password_rotation_required", Data: map[string]any{"legacyRotationRequired": true}})
			return
		}
		writeAdminAuthOperationError(w, err)
		return
	}
	setAdminCredentialCookies(w, credentials, security.Secure)
	writeAdminCredentialsResponse(w, credentials)
}

func handleAdminLegacyRotation(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Username    string `json:"username"`
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if !decodeAdminJSON(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.Username) == "" {
		request.Username = adminauth.BreakGlassUsername
	}
	runtime, security, ok := authRuntimeAndSecurity(w, r)
	if !ok {
		return
	}
	credentials, err := runtime.service.CompleteLegacyRotation(r.Context(), request.Username, request.OldPassword, request.NewPassword, security.ClientIP.String())
	if err != nil {
		writeAdminAuthOperationError(w, err)
		return
	}
	setAdminCredentialCookies(w, credentials, security.Secure)
	writeAdminCredentialsResponse(w, credentials)
}

func handleAdminCurrentSession(w http.ResponseWriter, r *http.Request) {
	principal, ok := adminPrincipal(r)
	if !ok {
		writeAdminSecurityError(w, http.StatusUnauthorized, "login_required")
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{
		"principal": principal,
		"expiresAt": principal.ExpiresAt,
	}})
}

func handleAdminLogoutSecure(w http.ResponseWriter, r *http.Request) {
	runtime, security, ok := authRuntimeAndSecurity(w, r)
	if !ok {
		return
	}
	principal, ok := adminPrincipal(r)
	if !ok {
		writeAdminSecurityError(w, http.StatusUnauthorized, "login_required")
		return
	}
	err := runtime.service.Logout(r.Context(), principal)
	clearAdminCredentialCookies(w, security.Secure)
	if err != nil {
		writeAdminAuthOperationError(w, err)
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true})
}

func handleAdminPasswordRotation(w http.ResponseWriter, r *http.Request) {
	var request struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if !decodeAdminJSON(w, r, &request) {
		return
	}
	runtime, security, ok := authRuntimeAndSecurity(w, r)
	if !ok {
		return
	}
	principal, ok := adminPrincipal(r)
	if !ok {
		writeAdminSecurityError(w, http.StatusUnauthorized, "login_required")
		return
	}
	if err := runtime.service.RotatePassword(r.Context(), principal, request.OldPassword, request.NewPassword); err != nil {
		writeAdminAuthOperationError(w, err)
		return
	}
	clearAdminCredentialCookies(w, security.Secure)
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"loginRequired": true}})
}

func handleAdminSessions(w http.ResponseWriter, r *http.Request) {
	runtime, _, ok := authRuntimeAndSecurity(w, r)
	if !ok {
		return
	}
	principal, ok := adminPrincipal(r)
	if !ok {
		writeAdminSecurityError(w, http.StatusUnauthorized, "login_required")
		return
	}
	sessions, err := runtime.service.ListSessions(r.Context(), principal)
	if err != nil {
		writeAdminAuthOperationError(w, err)
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"sessions": sessions}})
}

func handleAdminSessionRevoke(w http.ResponseWriter, r *http.Request) {
	var request struct {
		SessionHash string `json:"sessionHash"`
	}
	if !decodeAdminJSON(w, r, &request) {
		return
	}
	runtime, security, ok := authRuntimeAndSecurity(w, r)
	if !ok {
		return
	}
	principal, ok := adminPrincipal(r)
	if !ok {
		writeAdminSecurityError(w, http.StatusUnauthorized, "login_required")
		return
	}
	if err := runtime.service.RevokeSession(r.Context(), principal, request.SessionHash); err != nil {
		writeAdminAuthOperationError(w, err)
		return
	}
	current := request.SessionHash == principal.SessionHash
	if current {
		clearAdminCredentialCookies(w, security.Secure)
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"currentSessionRevoked": current}})
}

func handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	runtime, _, ok := authRuntimeAndSecurity(w, r)
	if !ok {
		return
	}
	principal, ok := adminPrincipal(r)
	if !ok {
		writeAdminSecurityError(w, http.StatusUnauthorized, "login_required")
		return
	}
	users, err := runtime.service.ListUsers(r.Context(), principal)
	if err != nil {
		writeAdminAuthOperationError(w, err)
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"users": users}})
}

func handleAdminUserCreate(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Username string           `json:"username"`
		Password string           `json:"password"`
		Roles    []adminauth.Role `json:"roles"`
	}
	if !decodeAdminJSON(w, r, &request) {
		return
	}
	runtime, _, ok := authRuntimeAndSecurity(w, r)
	if !ok {
		return
	}
	principal, ok := adminPrincipal(r)
	if !ok {
		writeAdminSecurityError(w, http.StatusUnauthorized, "login_required")
		return
	}
	user, err := runtime.service.CreateLocalUser(r.Context(), principal, request.Username, request.Password, request.Roles)
	if err != nil {
		writeAdminAuthOperationError(w, err)
		return
	}
	httpx.WriteAPI(w, http.StatusCreated, httpx.Response{Success: true, Data: map[string]any{"user": user}})
}

func handleAdminUserUpdate(w http.ResponseWriter, r *http.Request) {
	var request struct {
		UserID   string           `json:"userID"`
		Revision uint64           `json:"revision"`
		Roles    []adminauth.Role `json:"roles"`
		Disabled bool             `json:"disabled"`
	}
	if !decodeAdminJSON(w, r, &request) {
		return
	}
	runtime, _, ok := authRuntimeAndSecurity(w, r)
	if !ok {
		return
	}
	principal, ok := adminPrincipal(r)
	if !ok {
		writeAdminSecurityError(w, http.StatusUnauthorized, "login_required")
		return
	}
	user, err := runtime.service.UpdateUser(r.Context(), principal, request.UserID, request.Revision, request.Roles, request.Disabled)
	if err != nil {
		writeAdminAuthOperationError(w, err)
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"user": user}})
}

func handleAdminUserPasswordReset(w http.ResponseWriter, r *http.Request) {
	var request struct {
		UserID   string `json:"userID"`
		Revision uint64 `json:"revision"`
		Password string `json:"password"`
	}
	if !decodeAdminJSON(w, r, &request) {
		return
	}
	runtime, _, ok := authRuntimeAndSecurity(w, r)
	if !ok {
		return
	}
	principal, ok := adminPrincipal(r)
	if !ok {
		writeAdminSecurityError(w, http.StatusUnauthorized, "login_required")
		return
	}
	if err := runtime.service.SetLocalPasswordAtRevision(r.Context(), principal, request.UserID, request.Revision, request.Password); err != nil {
		writeAdminAuthOperationError(w, err)
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true})
}

func handleAdminUserDelete(w http.ResponseWriter, r *http.Request) {
	var request struct {
		UserID   string `json:"userID"`
		Revision uint64 `json:"revision"`
	}
	if !decodeAdminJSON(w, r, &request) {
		return
	}
	runtime, _, ok := authRuntimeAndSecurity(w, r)
	if !ok {
		return
	}
	principal, ok := adminPrincipal(r)
	if !ok {
		writeAdminSecurityError(w, http.StatusUnauthorized, "login_required")
		return
	}
	if err := runtime.service.DeleteLocalUser(r.Context(), principal, request.UserID, request.Revision); err != nil {
		writeAdminAuthOperationError(w, err)
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true})
}

func handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	runtime, _, ok := authRuntimeAndSecurity(w, r)
	if !ok {
		return
	}
	if runtime.store == nil {
		writeAdminSecurityError(w, http.StatusServiceUnavailable, "audit_unavailable")
		return
	}
	events, err := runtime.store.AuditEvents(r.Context())
	if err != nil {
		writeAdminAuthOperationError(w, err)
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"events": events}})
}

func authRuntimeAndSecurity(w http.ResponseWriter, r *http.Request) (*adminSecurityRuntime, adminauth.RequestSecurity, bool) {
	runtime, ok := adminSecurityRuntimeFromRequest(r)
	if !ok {
		writeAdminSecurityError(w, http.StatusServiceUnavailable, "security_unavailable")
		return nil, adminauth.RequestSecurity{}, false
	}
	security, ok := adminRequestSecurity(r)
	if !ok {
		writeAdminSecurityError(w, http.StatusBadRequest, "invalid_request_context")
		return nil, adminauth.RequestSecurity{}, false
	}
	return runtime, security, true
}

func decodeAdminJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	if contentType != "application/json" {
		httpx.WriteAPI(w, http.StatusUnsupportedMediaType, httpx.Response{Error: "application_json_required"})
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, adminauth.MaxLoginBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "invalid_json"})
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "invalid_json"})
		return false
	}
	return true
}

func writeAdminCredentialsResponse(w http.ResponseWriter, credentials adminauth.Credentials) {
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{
		"principal": credentials.Principal,
		"expiresAt": credentials.ExpiresAt,
	}})
}

func writeAdminAuthOperationError(w http.ResponseWriter, err error) {
	status := adminAuthErrorStatus(err)
	code := "security_operation_failed"
	switch {
	case errors.Is(err, adminauth.ErrRateLimited):
		code = "rate_limited"
		var limited *adminauth.RateLimitError
		if errors.As(err, &limited) && limited.RetryAfter > 0 {
			seconds := int(limited.RetryAfter.Seconds())
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
		}
	case errors.Is(err, adminauth.ErrUnauthorized):
		code = "authentication_failed"
	case errors.Is(err, adminauth.ErrForbidden):
		code = "permission_denied"
	case errors.Is(err, adminauth.ErrNotInitialized):
		code = "bootstrap_required"
	case errors.Is(err, adminauth.ErrAlreadyInitialized):
		code = "already_initialized"
	case errors.Is(err, adminauth.ErrDegraded):
		code = "security_degraded"
	case errors.Is(err, adminauth.ErrInvalidState):
		code = "security_state_invalid"
	case errors.Is(err, adminauth.ErrConflict):
		code = "security_state_conflict"
	case errors.Is(err, adminauth.ErrMutationPrepared):
		code = "security_restart_required"
	}
	httpx.WriteAPI(w, status, httpx.Response{Error: code})
}
