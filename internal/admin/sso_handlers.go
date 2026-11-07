package admin

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"routenest/internal/adminauth"
	"routenest/internal/adminsso"
	"routenest/internal/httpx"
)

const samlACSBodyLimit = 256 << 10

func handleAdminSSOProviders(w http.ResponseWriter, r *http.Request) {
	runtime, _, ok := authRuntimeAndSecurity(w, r)
	if !ok || !requireSSOManager(w, runtime) {
		return
	}
	providers, err := runtime.sso.List(r.Context())
	if err != nil {
		writeAdminSSOOperationError(w, err, false)
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"providers": providers}})
}

func handleAdminSSOProviderSave(w http.ResponseWriter, r *http.Request) {
	var request adminsso.ProviderConfig
	if !decodeAdminJSON(w, r, &request) {
		return
	}
	runtime, _, ok := authRuntimeAndSecurity(w, r)
	if !ok || !requireSSOManager(w, runtime) {
		return
	}
	principal, ok := adminPrincipal(r)
	if !ok {
		writeAdminSecurityError(w, http.StatusUnauthorized, "login_required")
		return
	}
	provider, err := runtime.sso.Upsert(r.Context(), principal, request)
	if err != nil {
		writeAdminSSOOperationError(w, err, true)
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"provider": provider}})
}

func handleAdminSSOProviderDelete(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ID string `json:"id"`
	}
	if !decodeAdminJSON(w, r, &request) {
		return
	}
	runtime, _, ok := authRuntimeAndSecurity(w, r)
	if !ok || !requireSSOManager(w, runtime) {
		return
	}
	principal, ok := adminPrincipal(r)
	if !ok {
		writeAdminSecurityError(w, http.StatusUnauthorized, "login_required")
		return
	}
	if err := runtime.sso.Delete(r.Context(), principal, request.ID); err != nil {
		writeAdminSSOOperationError(w, err, true)
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true})
}

func handleAdminOIDCStart(w http.ResponseWriter, r *http.Request) {
	handleAdminSSOStart(w, r, adminauth.MethodOIDC)
}

func handleAdminSAMLStart(w http.ResponseWriter, r *http.Request) {
	handleAdminSSOStart(w, r, adminauth.MethodSAML)
}

func handleAdminSSOStart(w http.ResponseWriter, r *http.Request, method adminauth.AuthMethod) {
	var request struct {
		Provider       string `json:"provider"`
		ReturnLocation string `json:"returnLocation"`
	}
	if !decodeAdminJSON(w, r, &request) {
		return
	}
	if request.ReturnLocation == "" {
		request.ReturnLocation = "/admin/"
	}
	runtime, security, ok := authRuntimeAndSecurity(w, r)
	if !ok || !requireSSOManager(w, runtime) {
		return
	}
	var redirectURL string
	var err error
	if method == adminauth.MethodOIDC {
		redirectURL, err = runtime.sso.StartOIDC(r.Context(), request.Provider, security.ClientIP.String(), request.ReturnLocation)
	} else {
		redirectURL, err = runtime.sso.StartSAML(r.Context(), request.Provider, security.ClientIP.String(), request.ReturnLocation)
	}
	if err != nil {
		writeAdminSSOOperationError(w, err, false)
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"redirectURL": redirectURL}})
}

func handleAdminOIDCCallback(w http.ResponseWriter, r *http.Request) {
	runtime, security, ok := authRuntimeAndSecurity(w, r)
	if !ok || !requireSSOManager(w, runtime) {
		return
	}
	if len(r.URL.RawQuery) > 16<<10 {
		writeAdminSecurityError(w, http.StatusBadRequest, "invalid_sso_callback")
		return
	}
	query := r.URL.Query()
	state, ok := singleQueryValue(query, "state")
	if !ok {
		writeAdminSecurityError(w, http.StatusBadRequest, "invalid_sso_callback")
		return
	}
	code := ""
	if _, providerError := query["error"]; !providerError {
		var valid bool
		code, valid = singleQueryValue(query, "code")
		if !valid {
			writeAdminSecurityError(w, http.StatusBadRequest, "invalid_sso_callback")
			return
		}
	}
	credentials, returnLocation, err := runtime.sso.CompleteOIDC(r.Context(), state, code, security.ClientIP.String())
	if err != nil {
		writeAdminSSOOperationError(w, err, false)
		return
	}
	setAdminCredentialCookies(w, credentials, security.Secure)
	http.Redirect(w, r, returnLocation, http.StatusSeeOther)
}

func handleAdminSAMLMetadata(w http.ResponseWriter, r *http.Request) {
	runtime, security, ok := authRuntimeAndSecurity(w, r)
	if !ok || !requireSSOManager(w, runtime) {
		return
	}
	query := r.URL.Query()
	provider, valid := singleQueryValue(query, "provider")
	if !valid || len(query) != 1 {
		writeAdminSecurityError(w, http.StatusBadRequest, "invalid_sso_provider")
		return
	}
	metadata, err := runtime.sso.SAMLMetadata(r.Context(), provider, security.ClientIP.String())
	if err != nil {
		writeAdminSSOOperationError(w, err, false)
		return
	}
	w.Header().Set("Content-Type", "application/samlmetadata+xml; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(metadata)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(metadata)
}

func handleAdminSAMLACS(w http.ResponseWriter, r *http.Request) {
	runtime, security, ok := authRuntimeAndSecurity(w, r)
	if !ok || !requireSSOManager(w, runtime) {
		return
	}
	if r.URL.RawQuery != "" || r.ContentLength > samlACSBodyLimit {
		writeAdminSecurityError(w, http.StatusBadRequest, "invalid_saml_response")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, samlACSBodyLimit+1))
	if err != nil || len(body) > samlACSBodyLimit {
		writeAdminSecurityError(w, http.StatusBadRequest, "invalid_saml_response")
		return
	}
	form, err := url.ParseQuery(string(body))
	if err != nil || len(form) != 2 || len(form["RelayState"]) != 1 || len(form["SAMLResponse"]) != 1 {
		writeAdminSecurityError(w, http.StatusBadRequest, "invalid_saml_response")
		return
	}
	relayState := form.Get("RelayState")
	provider, _, err := adminsso.ParseFlowState(relayState)
	if err != nil {
		writeAdminSecurityError(w, http.StatusBadRequest, "invalid_saml_response")
		return
	}
	request := r.Clone(r.Context())
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))
	request.Host = security.ExternalHost
	request.URL.Scheme = security.ExternalScheme
	request.URL.Host = security.ExternalHost
	credentials, returnLocation, err := runtime.sso.CompleteSAML(r.Context(), provider, relayState, request, security.ClientIP.String())
	if err != nil {
		writeAdminSSOOperationError(w, err, false)
		return
	}
	setAdminCredentialCookies(w, credentials, security.Secure)
	http.Redirect(w, r, returnLocation, http.StatusSeeOther)
}

func requireSSOManager(w http.ResponseWriter, runtime *adminSecurityRuntime) bool {
	if runtime == nil || runtime.sso == nil {
		writeAdminSecurityError(w, http.StatusServiceUnavailable, "sso_unavailable")
		return false
	}
	return true
}

func singleQueryValue(values url.Values, key string) (string, bool) {
	items := values[key]
	if len(items) != 1 || strings.TrimSpace(items[0]) == "" {
		return "", false
	}
	return items[0], true
}

func writeAdminSSOOperationError(w http.ResponseWriter, err error, invalidIsBadRequest bool) {
	switch {
	case errors.Is(err, adminsso.ErrProviderNotFound):
		writeAdminSecurityError(w, http.StatusNotFound, "sso_provider_not_found")
	case errors.Is(err, adminsso.ErrProviderUnavailable):
		writeAdminSecurityError(w, http.StatusServiceUnavailable, "sso_provider_unavailable")
	case errors.Is(err, adminsso.ErrProviderConflict):
		writeAdminSecurityError(w, http.StatusConflict, "sso_provider_conflict")
	case errors.Is(err, adminauth.ErrRateLimited):
		writeAdminAuthOperationError(w, err)
	case errors.Is(err, adminauth.ErrForbidden):
		writeAdminSecurityError(w, http.StatusForbidden, "sso_forbidden")
	case errors.Is(err, adminauth.ErrUnauthorized), errors.Is(err, adminauth.ErrReplay):
		writeAdminSecurityError(w, http.StatusUnauthorized, "sso_authentication_failed")
	case invalidIsBadRequest:
		writeAdminSecurityError(w, http.StatusBadRequest, "invalid_sso_provider")
	default:
		writeAdminSecurityError(w, http.StatusUnauthorized, "sso_authentication_failed")
	}
}
