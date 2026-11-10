package admin

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"routenest/internal/adminapi"
	"routenest/internal/adminauth"
)

const (
	desktopConsoleMaxBodyBytes     = 2 << 20
	desktopConsoleMaxResponseBytes = 16 << 20
	desktopConsoleMaxPathBytes     = 32 << 10
)

var errDesktopConsoleResponseTooLarge = errors.New("desktop console response exceeds its limit")

type DesktopConsoleRequest struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   string `json:"body,omitempty"`
	CSRF   string `json:"csrf,omitempty"`
}

type DesktopConsoleResponse struct {
	Status      int    `json:"status"`
	Body        string `json:"body"`
	ContentType string `json:"contentType"`
	CSRF        string `json:"csrf,omitempty"`
}

type DesktopConsole struct {
	mu           sync.Mutex
	runtime      *adminSecurityRuntime
	handler      http.Handler
	origin       string
	peer         string
	secure       bool
	sessionToken string
	csrfToken    string
}

func NewDesktopConsole() (*DesktopConsole, error) {
	runtime, err := configuredAdminSecurity()
	if err != nil {
		return nil, err
	}
	return newDesktopConsole(runtime)
}

func newDesktopConsole(runtime *adminSecurityRuntime) (*DesktopConsole, error) {
	if runtime == nil || runtime.service == nil {
		return nil, fmt.Errorf("desktop console security is unavailable")
	}
	scheme, host, err := adminauth.NormalizeCanonicalURL(runtime.requestOptions.CanonicalURL)
	if err != nil {
		return nil, err
	}
	peer := "127.0.0.1:0"
	if runtime.requestOptions.ProxyMode {
		peer = ""
		for _, network := range runtime.requestOptions.TrustedProxyCIDRs {
			if network != nil && network.IP != nil && network.Contains(network.IP) {
				peer = net.JoinHostPort(network.IP.String(), "0")
				break
			}
		}
		if peer == "" {
			return nil, fmt.Errorf("desktop console request security is unavailable")
		}
	}
	mux := http.NewServeMux()
	registerRoutesWithSecurity(mux, runtime)
	return &DesktopConsole{runtime: runtime, handler: mux, origin: scheme + "://" + host, peer: peer, secure: scheme == "https"}, nil
}

func (console *DesktopConsole) Request(ctx context.Context, input DesktopConsoleRequest) DesktopConsoleResponse {
	if console == nil {
		return desktopConsoleError(http.StatusServiceUnavailable, "security_unavailable")
	}
	console.mu.Lock()
	defer console.mu.Unlock()
	if console.runtime == nil || console.runtime.service == nil || console.handler == nil {
		console.clearLocked()
		return desktopConsoleError(http.StatusServiceUnavailable, "security_unavailable")
	}
	if ctx == nil {
		return desktopConsoleError(http.StatusBadRequest, "invalid_request_context")
	}
	if ctx.Err() != nil {
		return desktopConsoleError(http.StatusRequestTimeout, "request_cancelled")
	}
	if len(input.Body) > desktopConsoleMaxBodyBytes {
		return desktopConsoleError(http.StatusRequestEntityTooLarge, "request_too_large")
	}
	if !validAdminMethod(input.Method) {
		return desktopConsoleError(http.StatusMethodNotAllowed, "method_not_allowed")
	}
	if !validDesktopConsolePath(input.Path) || len(input.CSRF) > 256 || strings.ContainsAny(input.CSRF, "\r\n") {
		return desktopConsoleError(http.StatusBadRequest, "invalid_console_request")
	}
	ctx = context.WithValue(ctx, desktopHandoffContextKey{}, console.runtime)
	request, err := http.NewRequestWithContext(ctx, input.Method, console.origin+input.Path, strings.NewReader(input.Body))
	if err != nil {
		return desktopConsoleError(http.StatusBadRequest, "invalid_console_request")
	}
	request.RequestURI = request.URL.RequestURI()
	request.RemoteAddr = console.peer
	if console.secure {
		request.TLS = &tls.ConnectionState{}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Origin", console.origin)
	request.Header.Set("Referer", console.origin+"/admin/")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Sec-Fetch-Mode", "same-origin")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	if console.sessionToken != "" {
		request.AddCookie(&http.Cookie{Name: adminapi.SessionCookieName, Value: console.sessionToken})
	}
	if input.CSRF != "" {
		request.Header.Set("X-RouteNest-CSRF", input.CSRF)
	}
	writer := newDesktopConsoleResponseWriter()
	console.handler.ServeHTTP(writer, request)
	response := writer.response()
	changed := console.captureCookiesLocked(writer.committed)
	if response.Status == http.StatusUnauthorized {
		console.clearLocked()
	} else if console.sessionToken != "" {
		credentials, err := console.credentialsLocked(ctx)
		if err != nil && changed && response.Status >= 200 && response.Status < 300 {
			response = desktopConsoleError(http.StatusUnauthorized, "authentication_failed")
		} else if err == nil && response.Status >= 200 && response.Status < 300 {
			response.CSRF = credentials.CSRFToken
		}
	}
	if writer.overflow {
		return desktopConsoleError(http.StatusInternalServerError, "response_too_large")
	}
	if input.Method == http.MethodHead {
		response.Body = ""
	}
	return response
}

func (console *DesktopConsole) Credentials(ctx context.Context) (adminauth.Credentials, error) {
	if console == nil {
		return adminauth.Credentials{}, adminauth.ErrUnauthorized
	}
	console.mu.Lock()
	defer console.mu.Unlock()
	return console.credentialsLocked(ctx)
}

func (console *DesktopConsole) credentialsLocked(ctx context.Context) (adminauth.Credentials, error) {
	credentials, err := authenticateDesktopSession(ctx, console.runtime, console.sessionToken, console.csrfToken)
	if err != nil {
		console.clearLocked()
		return adminauth.Credentials{}, err
	}
	return credentials, nil
}

func (console *DesktopConsole) Clear() {
	if console == nil {
		return
	}
	console.mu.Lock()
	defer console.mu.Unlock()
	console.clearLocked()
}

func (console *DesktopConsole) clearLocked() {
	console.sessionToken = ""
	console.csrfToken = ""
}

func (console *DesktopConsole) captureCookiesLocked(header http.Header) bool {
	var session, csrf *http.Cookie
	for _, cookie := range (&http.Response{Header: header}).Cookies() {
		switch cookie.Name {
		case adminapi.SessionCookieName:
			session = cookie
		case adminCSRFCookie:
			csrf = cookie
		}
	}
	if session == nil && csrf == nil {
		return false
	}
	console.clearLocked()
	if session != nil && csrf != nil && session.MaxAge >= 0 && csrf.MaxAge >= 0 && session.Value != "" && csrf.Value != "" {
		console.sessionToken = session.Value
		console.csrfToken = csrf.Value
	}
	return true
}

func (console *DesktopConsole) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	applyAdminSecurityHeaders(w, false, nil)
	if console == nil || console.runtime == nil || console.runtime.service == nil || console.handler == nil {
		writeAdminSecurityError(w, http.StatusServiceUnavailable, "security_unavailable")
		return
	}
	if r == nil || r.URL == nil || r.URL.Path != "/admin/" || r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.User != nil || r.URL.Fragment != "" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeAdminSecurityError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		return
	}
	adminStaticHandler(w, r)
}

func validDesktopConsolePath(path string) bool {
	if len(path) > desktopConsoleMaxPathBytes || !strings.HasPrefix(path, "/admin/api/") || strings.ContainsAny(path, "#\\") {
		return false
	}
	parsed, err := url.ParseRequestURI(path)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil || parsed.Opaque != "" || parsed.Fragment != "" || parsed.RawPath != "" || parsed.EscapedPath() != parsed.Path {
		return false
	}
	if validateAdminPath(parsed.Path) != nil {
		return false
	}
	_, err = url.ParseQuery(parsed.RawQuery)
	return err == nil
}

type desktopConsoleResponseWriter struct {
	header    http.Header
	committed http.Header
	body      bytes.Buffer
	status    int
	overflow  bool
}

func newDesktopConsoleResponseWriter() *desktopConsoleResponseWriter {
	return &desktopConsoleResponseWriter{header: make(http.Header)}
}

func (writer *desktopConsoleResponseWriter) Header() http.Header {
	return writer.header
}

func (writer *desktopConsoleResponseWriter) WriteHeader(status int) {
	if writer.status != 0 || status < 200 {
		return
	}
	writer.status = status
	writer.committed = writer.header.Clone()
}

func (writer *desktopConsoleResponseWriter) Write(body []byte) (int, error) {
	if writer.status == 0 {
		if writer.header.Get("Content-Type") == "" {
			writer.header.Set("Content-Type", http.DetectContentType(body))
		}
		writer.WriteHeader(http.StatusOK)
	}
	if writer.overflow || len(body) > desktopConsoleMaxResponseBytes-writer.body.Len() {
		writer.overflow = true
		writer.body.Reset()
		return 0, errDesktopConsoleResponseTooLarge
	}
	return writer.body.Write(body)
}

func (writer *desktopConsoleResponseWriter) response() DesktopConsoleResponse {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	return DesktopConsoleResponse{Status: writer.status, Body: writer.body.String(), ContentType: writer.committed.Get("Content-Type")}
}

func desktopConsoleError(status int, code string) DesktopConsoleResponse {
	writer := newDesktopConsoleResponseWriter()
	writeAdminSecurityError(writer, status, code)
	return writer.response()
}

func AuthenticateDesktopLocal(ctx context.Context, username, password string) (adminauth.Credentials, error) {
	runtime, err := configuredAdminSecurity()
	if err != nil {
		return adminauth.Credentials{}, err
	}
	if strings.TrimSpace(username) == "" {
		username = adminauth.BreakGlassUsername
	}
	return runtime.service.Login(ctx, username, password, "127.0.0.1")
}

func CompleteDesktopBootstrapCredentials(ctx context.Context, secret, password string) (string, adminauth.Credentials, error) {
	runtime, err := configuredAdminSecurity()
	if err != nil {
		return "", adminauth.Credentials{}, err
	}
	credentials, recoveryCode, err := runtime.service.CompleteBootstrapWithRecovery(ctx, secret, password, "127.0.0.1")
	return recoveryCode, credentials, err
}

func RecoverDesktopPassword(ctx context.Context, code, password string) (string, bool, error) {
	runtime, err := configuredAdminSecurity()
	if err != nil {
		return "", false, err
	}
	replacement, err := runtime.service.RecoverWithCode(ctx, code, password, "127.0.0.1")
	return replacement, errors.Is(err, adminauth.ErrMutationPrepared), err
}
