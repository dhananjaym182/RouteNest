package adminauth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

type AuthFlow struct {
	Provider            string
	ProviderRevision    uint64
	ProviderFingerprint string
	Type                AuthMethod
	Source              string
	Nonce               string
	PKCEVerifier        string
	SAMLRequestID       string
	ExpectedIssuer      string
	Callback            string
	ReturnLocation      string
	CreatedAt           time.Time
	ExpiresAt           time.Time
}

func (AuthFlow) String() string { return "[redacted authentication flow]" }

type AuthFlowStore struct {
	mu          sync.Mutex
	flows       map[string]AuthFlow
	random      io.Reader
	now         func() time.Time
	max         int
	perSource   int
	perProvider int
}

func NewAuthFlowStore() *AuthFlowStore {
	return &AuthFlowStore{flows: map[string]AuthFlow{}, random: rand.Reader, now: time.Now, max: 4096, perSource: 8, perProvider: 512}
}
func (f *AuthFlowStore) Create(flow AuthFlow) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	for k, v := range f.flows {
		if !v.ExpiresAt.After(now) {
			delete(f.flows, k)
		}
	}
	if flow.Provider == "" || flow.ProviderRevision == 0 || flow.ProviderFingerprint == "" || len(flow.ProviderFingerprint) > 128 || (flow.Type != MethodOIDC && flow.Type != MethodSAML) || flow.Callback == "" {
		return "", fmt.Errorf("invalid authentication flow")
	}
	if strings.ContainsAny(flow.ReturnLocation, "\\\r\n") {
		return "", fmt.Errorf("invalid return location")
	}
	if flow.ReturnLocation != "" && (flow.ReturnLocation[0] != '/' || len(flow.ReturnLocation) > 2048 || len(flow.ReturnLocation) > 1 && flow.ReturnLocation[1] == '/') {
		return "", fmt.Errorf("invalid return location")
	}
	sources, providers := 0, 0
	for _, v := range f.flows {
		if v.Source == flow.Source {
			sources++
		}
		if v.Provider == flow.Provider {
			providers++
		}
	}
	if len(f.flows) >= f.max || sources >= f.perSource || providers >= f.perProvider {
		return "", ErrRateLimited
	}
	if flow.ExpiresAt.IsZero() {
		flow.ExpiresAt = now.Add(10 * time.Minute)
	}
	if !flow.ExpiresAt.After(now) || flow.ExpiresAt.After(now.Add(10*time.Minute)) {
		return "", fmt.Errorf("invalid flow lifetime")
	}
	flow.CreatedAt = now
	token, e := randomToken(f.random)
	if e != nil {
		return "", e
	}
	f.flows[hashToken(token)] = flow
	return token, nil
}

// Consume destroys the flow even when provider/type do not match, so handles
// cannot be probed repeatedly or reused following a failed callback.
func (f *AuthFlowStore) Consume(handle, provider string, kind AuthMethod) (AuthFlow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(handle) != 43 {
		return AuthFlow{}, ErrReplay
	}
	h := hashToken(handle)
	flow, ok := f.flows[h]
	delete(f.flows, h)
	if !ok || flow.Provider != provider || flow.Type != kind || !flow.ExpiresAt.After(f.now()) {
		return AuthFlow{}, ErrReplay
	}
	return flow, nil
}
func (f *AuthFlowStore) RevokeProvider(provider string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, v := range f.flows {
		if v.Provider == provider {
			delete(f.flows, k)
		}
	}
}
func (f *AuthFlowStore) Clear() { f.mu.Lock(); defer f.mu.Unlock(); clear(f.flows) }
func randomToken(r io.Reader) (string, error) {
	b := make([]byte, 32)
	if _, e := io.ReadFull(r, b); e != nil {
		return "", fmt.Errorf("secure random generation failed: %w", e)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
