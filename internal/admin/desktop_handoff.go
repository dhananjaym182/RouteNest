package admin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"routenest/internal/adminauth"
	"routenest/internal/httpx"
)

const (
	desktopHandoffLifetime = 2 * time.Minute
	maxDesktopHandoffs     = 128
)

var ErrDesktopHandoffUnavailable = errors.New("desktop session handoff unavailable; restart required")

type desktopHandoffContextKey struct{}

type desktopHandoff struct {
	credentials adminauth.Credentials
	expiresAt   time.Time
}

type desktopHandoffStore struct {
	mu      sync.Mutex
	entries map[string]desktopHandoff
	now     func() time.Time
	random  io.Reader
}

func newDesktopHandoffStore() *desktopHandoffStore {
	return &desktopHandoffStore{entries: map[string]desktopHandoff{}, now: time.Now, random: rand.Reader}
}

func (store *desktopHandoffStore) create(credentials adminauth.Credentials) (string, error) {
	if store == nil {
		return "", ErrDesktopHandoffUnavailable
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	now := store.now()
	replacing := false
	for key, entry := range store.entries {
		if !entry.expiresAt.After(now) {
			delete(store.entries, key)
		} else if credentials.Token != "" && entry.credentials.Token == credentials.Token {
			replacing = true
		}
	}
	if len(store.entries) >= maxDesktopHandoffs && !replacing {
		return "", adminauth.ErrRateLimited
	}
	buffer := make([]byte, 32)
	if store.random == nil {
		return "", fmt.Errorf("generate desktop handoff: random source unavailable")
	}
	if _, err := io.ReadFull(store.random, buffer); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(buffer)
	if _, exists := store.entries[token]; exists {
		return "", adminauth.ErrConflict
	}
	if replacing {
		for key, entry := range store.entries {
			if entry.credentials.Token == credentials.Token {
				delete(store.entries, key)
			}
		}
	}
	store.entries[token] = desktopHandoff{credentials: credentials, expiresAt: now.Add(desktopHandoffLifetime)}
	return token, nil
}

func (store *desktopHandoffStore) consume(token string) (adminauth.Credentials, bool) {
	if len(token) != 43 {
		return adminauth.Credentials{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	entry, ok := store.entries[token]
	delete(store.entries, token)
	if !ok || !entry.expiresAt.After(store.now()) {
		return adminauth.Credentials{}, false
	}
	return entry.credentials, true
}

func CreateDesktopSessionHandoff(credentials adminauth.Credentials) (string, error) {
	runtime, err := configuredAdminSecurity()
	if err != nil {
		return "", err
	}
	credentials, err = authenticateDesktopCredentials(context.Background(), runtime, credentials)
	if err != nil {
		return "", err
	}
	return runtime.desktopHandoffs.create(credentials)
}

func AuthenticateDesktopDevice(ctx context.Context, deviceID, secret string) (adminauth.Credentials, string, error) {
	runtime, err := configuredAdminSecurity()
	if err != nil {
		return adminauth.Credentials{}, "", err
	}
	credentials, err := runtime.service.AuthenticateTrustedDevice(ctx, deviceID, secret, "127.0.0.1")
	if err != nil {
		return adminauth.Credentials{}, "", err
	}
	handoff, err := runtime.desktopHandoffs.create(credentials)
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		refreshed, cleanupErr := authenticateDesktopCredentials(cleanupCtx, runtime, credentials)
		if cleanupErr == nil {
			cleanupErr = runtime.service.Logout(cleanupCtx, refreshed.Principal)
		}
		return adminauth.Credentials{}, "", errors.Join(err, cleanupErr)
	}
	return credentials, handoff, nil
}

func AuthenticateDesktopPassword(ctx context.Context, password string) (adminauth.Credentials, error) {
	return AuthenticateDesktopLocal(ctx, adminauth.BreakGlassUsername, password)
}

func SignOutDesktopSession(ctx context.Context, credentials adminauth.Credentials, _ ...string) error {
	runtime, err := configuredAdminSecurity()
	if err != nil {
		return err
	}
	credentials, err = authenticateDesktopCredentials(ctx, runtime, credentials)
	if err != nil {
		return err
	}
	return runtime.service.LogoutWithDevicePause(ctx, credentials.Principal)
}

func EndDesktopSession(ctx context.Context, credentials adminauth.Credentials) error {
	runtime, err := configuredAdminSecurity()
	if err != nil {
		return err
	}
	credentials, err = authenticateDesktopCredentials(ctx, runtime, credentials)
	if err != nil {
		return err
	}
	return runtime.service.Logout(ctx, credentials.Principal)
}

func EnrollDesktopDeviceAfterAuthentication(ctx context.Context, credentials adminauth.Credentials, metadata adminauth.TrustedDeviceMetadata, replaceID string) (adminauth.TrustedDeviceCredential, error) {
	runtime, err := configuredAdminSecurity()
	if err != nil {
		return adminauth.TrustedDeviceCredential{}, err
	}
	credentials, err = authenticateDesktopCredentials(ctx, runtime, credentials)
	if err != nil {
		return adminauth.TrustedDeviceCredential{}, err
	}
	return runtime.service.EnrollTrustedDeviceAfterAuthentication(ctx, credentials.Principal, metadata, replaceID)
}

func EnrollDesktopDevice(ctx context.Context, credentials adminauth.Credentials, currentPassword string, metadata adminauth.TrustedDeviceMetadata, replaceID string) (adminauth.TrustedDeviceCredential, error) {
	runtime, err := configuredAdminSecurity()
	if err != nil {
		return adminauth.TrustedDeviceCredential{}, err
	}
	credentials, err = authenticateDesktopCredentials(ctx, runtime, credentials)
	if err != nil {
		return adminauth.TrustedDeviceCredential{}, err
	}
	return runtime.service.EnrollTrustedDevice(ctx, credentials.Principal, currentPassword, metadata, replaceID)
}

func ActivateDesktopDevice(ctx context.Context, credentials adminauth.Credentials, deviceID string) error {
	runtime, err := configuredAdminSecurity()
	if err != nil {
		return err
	}
	credentials, err = authenticateDesktopCredentials(ctx, runtime, credentials)
	if err != nil {
		return err
	}
	return runtime.service.ActivateTrustedDevice(ctx, credentials.Principal, deviceID)
}

func DiscardDesktopDevice(ctx context.Context, credentials adminauth.Credentials, deviceID string) error {
	runtime, err := configuredAdminSecurity()
	if err != nil {
		return err
	}
	credentials, err = authenticateDesktopCredentials(ctx, runtime, credentials)
	if err != nil {
		return err
	}
	return runtime.service.DiscardTrustedDevice(ctx, credentials.Principal, deviceID)
}

func CompleteDesktopBootstrapWithCredentials(ctx context.Context, secret, password string) (string, adminauth.Credentials, string, error) {
	runtime, err := configuredAdminSecurity()
	if err != nil {
		return "", adminauth.Credentials{}, "", err
	}
	if runtime == nil || runtime.service == nil || runtime.desktopHandoffs == nil {
		return "", adminauth.Credentials{}, "", fmt.Errorf("desktop bootstrap runtime is unavailable")
	}
	credentials, recoveryCode, err := runtime.service.CompleteBootstrapWithRecovery(ctx, secret, password, "127.0.0.1")
	if err != nil {
		if errors.Is(err, adminauth.ErrMutationPrepared) && recoveryCode != "" {
			return recoveryCode, adminauth.Credentials{}, "", err
		}
		return "", adminauth.Credentials{}, "", err
	}
	handoff, err := runtime.desktopHandoffs.create(credentials)
	if err != nil {
		return recoveryCode, credentials, "", errors.Join(ErrDesktopHandoffUnavailable, err)
	}
	return recoveryCode, credentials, handoff, nil
}

func CompleteDesktopBootstrap(ctx context.Context, secret, password string) (string, string, error) {
	runtime, err := configuredAdminSecurity()
	if err != nil {
		return "", "", err
	}
	return completeDesktopBootstrap(ctx, runtime, secret, password)
}

func completeDesktopBootstrap(ctx context.Context, runtime *adminSecurityRuntime, secret, password string) (string, string, error) {
	if runtime == nil || runtime.service == nil || runtime.desktopHandoffs == nil {
		return "", "", fmt.Errorf("desktop bootstrap runtime is unavailable")
	}
	credentials, recoveryCode, err := runtime.service.CompleteBootstrapWithRecovery(ctx, secret, password, "127.0.0.1")
	if err != nil {
		if errors.Is(err, adminauth.ErrMutationPrepared) && recoveryCode != "" {
			return recoveryCode, "", err
		}
		return "", "", err
	}
	handoff, err := runtime.desktopHandoffs.create(credentials)
	if err != nil {
		return recoveryCode, "", errors.Join(ErrDesktopHandoffUnavailable, err)
	}
	return recoveryCode, handoff, nil
}

func handleDesktopSessionHandoff(w http.ResponseWriter, r *http.Request) {
	runtime, security, ok := authRuntimeAndSecurity(w, r)
	if !ok {
		return
	}
	handoffRuntime, _ := r.Context().Value(desktopHandoffContextKey{}).(*adminSecurityRuntime)
	if handoffRuntime != runtime {
		writeAdminSecurityError(w, http.StatusForbidden, "desktop_handoff_unavailable")
		return
	}
	var request struct {
		Token string `json:"token"`
	}
	if !decodeAdminJSON(w, r, &request) {
		return
	}
	if runtime.desktopHandoffs == nil {
		writeAdminSecurityError(w, http.StatusServiceUnavailable, "desktop_handoff_unavailable")
		return
	}
	credentials, ok := runtime.desktopHandoffs.consume(request.Token)
	if !ok {
		writeAdminSecurityError(w, http.StatusUnauthorized, "authentication_failed")
		return
	}
	credentials, err := authenticateDesktopCredentials(r.Context(), runtime, credentials)
	if err != nil {
		writeAdminSecurityError(w, http.StatusUnauthorized, "authentication_failed")
		return
	}
	setAdminCredentialCookies(w, credentials, security.Secure)
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true})
}

func authenticateDesktopSession(ctx context.Context, runtime *adminSecurityRuntime, token, csrf string) (adminauth.Credentials, error) {
	if ctx == nil || runtime == nil || runtime.service == nil || token == "" {
		return adminauth.Credentials{}, adminauth.ErrUnauthorized
	}
	if err := ctx.Err(); err != nil {
		return adminauth.Credentials{}, err
	}
	principal, err := runtime.service.Authenticate(ctx, token)
	if err != nil {
		return adminauth.Credentials{}, err
	}
	if err := runtime.service.ValidateCSRF(principal, csrf); err != nil {
		return adminauth.Credentials{}, err
	}
	return adminauth.Credentials{Token: token, CSRFToken: csrf, Principal: principal, ExpiresAt: principal.ExpiresAt}, nil
}

func authenticateDesktopCredentials(ctx context.Context, runtime *adminSecurityRuntime, credentials adminauth.Credentials) (adminauth.Credentials, error) {
	refreshed, err := authenticateDesktopSession(ctx, runtime, credentials.Token, credentials.CSRFToken)
	if err != nil {
		return adminauth.Credentials{}, err
	}
	if refreshed.Principal.UserID != credentials.Principal.UserID || refreshed.Principal.SessionHash != credentials.Principal.SessionHash || refreshed.Principal.CSRFHash != credentials.Principal.CSRFHash {
		return adminauth.Credentials{}, adminauth.ErrUnauthorized
	}
	return refreshed, nil
}
