package admin

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"routenest/internal/adminapi"
	"routenest/internal/adminauth"
	"routenest/internal/httpx"
)

const adminCSRFCookie = "routenest_admin_csrf"

type adminPrincipalContextKey struct{}
type adminRequestSecurityContextKey struct{}
type adminSecurityRuntimeContextKey struct{}

func adminPrincipal(r *http.Request) (adminauth.Principal, bool) {
	principal, ok := r.Context().Value(adminPrincipalContextKey{}).(adminauth.Principal)
	return principal, ok
}

func adminRequestSecurity(r *http.Request) (adminauth.RequestSecurity, bool) {
	security, ok := r.Context().Value(adminRequestSecurityContextKey{}).(adminauth.RequestSecurity)
	return security, ok
}

func adminSecurityRuntimeFromRequest(r *http.Request) (*adminSecurityRuntime, bool) {
	runtime, ok := r.Context().Value(adminSecurityRuntimeContextKey{}).(*adminSecurityRuntime)
	return runtime, ok && runtime != nil
}

func (runtime *adminSecurityRuntime) handler(registry *adminRouteRegistry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		applyAdminSecurityHeaders(w, false, runtime.formActionOrigins())
		security, err := adminauth.ResolveRequestSecurity(r, runtime.requestOptions)
		if err != nil {
			writeAdminSecurityError(w, http.StatusBadRequest, "invalid_request_context")
			return
		}
		if runtime.enableHSTS && security.Secure {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		ctx := context.WithValue(r.Context(), adminRequestSecurityContextKey{}, security)
		ctx = context.WithValue(ctx, adminSecurityRuntimeContextKey{}, runtime)
		r = r.WithContext(ctx)

		if r.Method == http.MethodOptions {
			runtime.handlePreflight(w, r, registry, security)
			return
		}
		policy, classified := registry.Policy(r.Method, r.URL.Path)
		if !classified {
			registry.ServeHTTP(w, r)
			return
		}

		var principal adminauth.Principal
		if !policy.Public {
			cookie, err := r.Cookie(adminapi.SessionCookieName)
			if err != nil || cookie.Value == "" {
				writeAdminSecurityError(w, http.StatusUnauthorized, "login_required")
				return
			}
			principal, err = runtime.service.Authenticate(r.Context(), cookie.Value)
			if err != nil {
				clearAdminCredentialCookies(w, security.Secure)
				writeAdminSecurityError(w, http.StatusUnauthorized, "session_expired")
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), adminPrincipalContextKey{}, principal))
		}

		if policy.CSRF != csrfOIDCCallback && policy.CSRF != csrfSAMLACS {
			mutation := policy.CSRF == csrfSession || policy.CSRF == csrfLocalEntry || policy.CSRF == csrfSSOStart
			allowLoopbackMissingOrigin := policy.CSRF == csrfSession || policy.CSRF == csrfLocalEntry || policy.CSRF == csrfSSOStart
			if err := adminauth.ValidateSameOrigin(r, security, mutation, allowLoopbackMissingOrigin); err != nil {
				if !policy.Public {
					runtime.service.RecordDenial(adminauth.AuditCSRFDenied, security.ClientIP.String())
				}
				writeAdminSecurityError(w, http.StatusForbidden, "same_origin_required")
				return
			}
		}

		if policy.CSRF == csrfSession {
			tokens := r.Header.Values("X-RouteNest-CSRF")
			if len(tokens) != 1 || strings.Contains(tokens[0], ",") || runtime.service.ValidateCSRF(principal, strings.TrimSpace(tokens[0])) != nil {
				runtime.service.RecordDenial(adminauth.AuditCSRFDenied, security.ClientIP.String())
				writeAdminSecurityError(w, http.StatusForbidden, "csrf_invalid")
				return
			}
		}
		for _, permission := range policy.Permissions {
			if err := principal.Authorize(permission); err != nil {
				runtime.service.RecordDenial(adminauth.AuditAccessDenied, security.ClientIP.String())
				writeAdminSecurityError(w, http.StatusForbidden, "permission_denied")
				return
			}
		}
		registry.ServeHTTP(w, r)
	})
}

func (runtime *adminSecurityRuntime) staticHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		applyAdminSecurityHeaders(w, false, runtime.formActionOrigins())
		security, err := adminauth.ResolveRequestSecurity(r, runtime.requestOptions)
		if err != nil {
			writeAdminSecurityError(w, http.StatusBadRequest, "invalid_request_context")
			return
		}
		if runtime.enableHSTS && security.Secure {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if err := adminauth.ValidateSameOrigin(r, security, false, false); err != nil {
			writeAdminSecurityError(w, http.StatusForbidden, "same_origin_required")
			return
		}
		ctx := context.WithValue(r.Context(), adminRequestSecurityContextKey{}, security)
		ctx = context.WithValue(ctx, adminSecurityRuntimeContextKey{}, runtime)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (runtime *adminSecurityRuntime) handlePreflight(w http.ResponseWriter, r *http.Request, registry *adminRouteRegistry, security adminauth.RequestSecurity) {
	if err := adminauth.ValidateSameOrigin(r, security, false, false); err != nil {
		writeAdminSecurityError(w, http.StatusForbidden, "same_origin_required")
		return
	}
	methodValues := r.Header.Values("Access-Control-Request-Method")
	if len(methodValues) != 1 {
		writeAdminSecurityError(w, http.StatusBadRequest, "invalid_preflight")
		return
	}
	method := strings.ToUpper(strings.TrimSpace(methodValues[0]))
	if _, ok := registry.Policy(method, r.URL.Path); !ok {
		w.Header().Set("Allow", strings.Join(registry.AllowedMethods(r.URL.Path), ", "))
		writeAdminSecurityError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	requestedHeaders := make([]string, 0, 2)
	seen := map[string]bool{}
	for _, line := range r.Header.Values("Access-Control-Request-Headers") {
		for _, raw := range strings.Split(line, ",") {
			header := http.CanonicalHeaderKey(strings.TrimSpace(raw))
			if header == "" {
				continue
			}
			if header != "Content-Type" && header != "X-Routenest-Csrf" {
				writeAdminSecurityError(w, http.StatusForbidden, "preflight_header_denied")
				return
			}
			if !seen[header] {
				seen[header] = true
				requestedHeaders = append(requestedHeaders, header)
			}
		}
	}
	sort.Strings(requestedHeaders)
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Methods", method)
	if len(requestedHeaders) > 0 {
		w.Header().Set("Access-Control-Allow-Headers", strings.Join(requestedHeaders, ", "))
	}
	w.Header().Add("Vary", "Origin")
	w.WriteHeader(http.StatusNoContent)
}

func (runtime *adminSecurityRuntime) formActionOrigins() []string {
	if runtime == nil || runtime.sso == nil {
		return nil
	}
	return runtime.sso.FormActionOrigins()
}

func applyAdminSecurityHeaders(w http.ResponseWriter, hsts bool, formActionOrigins []string) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
	w.Header().Set("Cache-Control", "no-store")
	formAction := []string{"'self'"}
	formAction = append(formAction, formActionOrigins...)
	w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; form-action "+strings.Join(formAction, " "))
	if hsts {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
	}
}

func writeAdminSecurityError(w http.ResponseWriter, status int, code string) {
	httpx.WriteAPI(w, status, httpx.Response{Error: code})
}

func setAdminCredentialCookies(w http.ResponseWriter, credentials adminauth.Credentials, secure bool) {
	maxAge := int(time.Until(credentials.ExpiresAt).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{
		Name:     adminapi.SessionCookieName,
		Value:    credentials.Token,
		Path:     "/admin",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
		Expires:  credentials.ExpiresAt,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     adminCSRFCookie,
		Value:    credentials.CSRFToken,
		Path:     "/admin",
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
		Expires:  credentials.ExpiresAt,
	})
}

func clearAdminCredentialCookies(w http.ResponseWriter, secure bool) {
	for _, cookie := range []http.Cookie{
		{Name: adminapi.SessionCookieName, Path: "/admin", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode},
		{Name: adminCSRFCookie, Path: "/admin", Secure: secure, SameSite: http.SameSiteStrictMode},
	} {
		cookie.Value = ""
		cookie.MaxAge = -1
		cookie.Expires = time.Unix(1, 0)
		http.SetCookie(w, &cookie)
	}
}

func adminAuthErrorStatus(err error) int {
	switch {
	case errors.Is(err, adminauth.ErrRateLimited):
		return http.StatusTooManyRequests
	case errors.Is(err, adminauth.ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, adminauth.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, adminauth.ErrNotInitialized), errors.Is(err, adminauth.ErrAlreadyInitialized), errors.Is(err, adminauth.ErrPasswordRotationRequired):
		return http.StatusConflict
	case errors.Is(err, adminauth.ErrDegraded), errors.Is(err, adminauth.ErrInvalidState):
		return http.StatusServiceUnavailable
	case errors.Is(err, adminauth.ErrConflict):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}
