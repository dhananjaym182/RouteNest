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

const desktopHandoffLifetime = 2 * time.Minute

var ErrDesktopHandoffUnavailable = errors.New("desktop session handoff unavailable; restart required")

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
	buffer := make([]byte, 32)
	if store.random == nil {
		return "", fmt.Errorf("generate desktop handoff: random source unavailable")
	}
	if _, err := io.ReadFull(store.random, buffer); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(buffer)
	store.mu.Lock()
	defer store.mu.Unlock()
	now := store.now()
	for key, entry := range store.entries {
		if !entry.expiresAt.After(now) {
			delete(store.entries, key)
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
	return runtime.desktopHandoffs.create(credentials)
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
	var request struct {
		Token string `json:"token"`
	}
	if !decodeAdminJSON(w, r, &request) {
		return
	}
	runtime, security, ok := authRuntimeAndSecurity(w, r)
	if !ok {
		return
	}
	if security.TrustedProxy || security.PeerIP == nil || security.ClientIP == nil || !security.PeerIP.IsLoopback() || !security.ClientIP.IsLoopback() {
		writeAdminSecurityError(w, http.StatusForbidden, "desktop_handoff_unavailable")
		return
	}
	credentials, ok := runtime.desktopHandoffs.consume(request.Token)
	if !ok {
		writeAdminSecurityError(w, http.StatusUnauthorized, "authentication_failed")
		return
	}
	setAdminCredentialCookies(w, credentials, security.Secure)
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true})
}
