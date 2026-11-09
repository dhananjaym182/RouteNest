package admin

import (
	"errors"
	"net/http"

	"routenest/internal/adminauth"
	"routenest/internal/httpx"
)

func handleAdminRecoveryCodeIssue(w http.ResponseWriter, r *http.Request) {
	var request struct {
		CurrentPassword string `json:"currentPassword"`
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
	code, err := runtime.service.IssueRecoveryCode(r.Context(), principal, request.CurrentPassword, security.ClientIP.String())
	if err != nil {
		if errors.Is(err, adminauth.ErrMutationPrepared) && code != "" {
			clearAdminCredentialCookies(w, security.Secure)
			httpx.WriteAPI(w, http.StatusServiceUnavailable, httpx.Response{Error: "security_restart_required", Data: map[string]any{
				"recoveryCode":    code,
				"shownOnce":       true,
				"restartRequired": true,
			}})
			return
		}
		writeAdminAuthOperationError(w, err)
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{
		"recoveryCode": code,
		"shownOnce":    true,
	}})
}

func handleAdminRecoveryCodeUse(w http.ResponseWriter, r *http.Request) {
	var request struct {
		RecoveryCode    string `json:"recoveryCode"`
		NewPassword     string `json:"newPassword"`
		ConfirmPassword string `json:"confirmPassword"`
	}
	if !decodeAdminJSON(w, r, &request) {
		return
	}
	if request.NewPassword != request.ConfirmPassword {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "password_confirmation_mismatch"})
		return
	}
	runtime, security, ok := authRuntimeAndSecurity(w, r)
	if !ok {
		return
	}
	replacement, err := runtime.service.RecoverWithCode(r.Context(), request.RecoveryCode, request.NewPassword, security.ClientIP.String())
	if err != nil {
		if errors.Is(err, adminauth.ErrMutationPrepared) && replacement != "" {
			clearAdminCredentialCookies(w, security.Secure)
			httpx.WriteAPI(w, http.StatusServiceUnavailable, httpx.Response{Error: "security_restart_required", Data: map[string]any{
				"recoveryCode":    replacement,
				"shownOnce":       true,
				"restartRequired": true,
			}})
			return
		}
		writeAdminAuthOperationError(w, err)
		return
	}
	clearAdminCredentialCookies(w, security.Secure)
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{
		"recoveryCode":  replacement,
		"shownOnce":     true,
		"loginRequired": true,
	}})
}
