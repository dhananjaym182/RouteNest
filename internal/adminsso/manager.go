package adminsso

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"

	"routenest/internal/adminauth"
)

var (
	ErrProviderNotFound    = errors.New("SSO provider not found")
	ErrProviderUnavailable = errors.New("SSO provider unavailable")
	ErrProviderConflict    = errors.New("SSO provider changed concurrently")
)

type ManagerOptions struct {
	Service      *adminauth.Service
	CanonicalURL *url.URL
	SecretRoots  []string
	HTTPClient   *http.Client
}

// LoginProvider is the only provider information exposed before login. Secret
// references and protocol configuration remain restricted to security admins.
type LoginProvider struct {
	ID      string               `json:"id"`
	Type    adminauth.AuthMethod `json:"type"`
	Enabled bool                 `json:"enabled"`
	Ready   bool                 `json:"ready"`
	Reason  string               `json:"reason,omitempty"`
}

type Manager struct {
	service         *adminauth.Service
	resolver        *SecretResolver
	client          *http.Client
	canonicalOrigin string
	throttle        *adminauth.Throttler
	samlReplay      *SAMLReplayStore
	stateMu         sync.Mutex

	mu                sync.RWMutex
	views             map[string]ProviderView
	formActionOrigins map[string][]string
	samlMetadata      map[string]cachedSAMLMetadata
	knownRevisions    map[string]uint64
}

type cachedSAMLMetadata struct {
	revision uint64
	data     []byte
}

func NewManager(ctx context.Context, opts ManagerOptions) (*Manager, error) {
	if opts.Service == nil || opts.CanonicalURL == nil {
		return nil, fmt.Errorf("SSO manager requires administrator identity and canonical URL")
	}
	scheme, host, err := adminauth.NormalizeCanonicalURL(opts.CanonicalURL)
	if err != nil {
		return nil, err
	}
	resolver, err := NewSecretResolver(opts.SecretRoots)
	if err != nil {
		return nil, fmt.Errorf("initialize SSO secret resolver: %w", err)
	}
	client := opts.HTTPClient
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.MaxResponseHeaderBytes = 32 << 10
		transport.ResponseHeaderTimeout = 10 * time.Second
		transport.TLSHandshakeTimeout = 10 * time.Second
		client = &http.Client{Transport: transport, Timeout: 10 * time.Second}
	}
	m := &Manager{
		service:           opts.Service,
		resolver:          resolver,
		client:            client,
		canonicalOrigin:   scheme + "://" + host,
		throttle:          adminauth.NewThrottler(),
		samlReplay:        NewSAMLReplayStore(),
		views:             make(map[string]ProviderView),
		formActionOrigins: make(map[string][]string),
		samlMetadata:      make(map[string]cachedSAMLMetadata),
		knownRevisions:    make(map[string]uint64),
	}
	if err := m.seed(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) seed(ctx context.Context) error {
	state, err := m.service.State(ctx)
	if err != nil {
		return err
	}
	views := make(map[string]ProviderView, len(state.Providers))
	revisions := make(map[string]uint64, len(state.Providers))
	for id, provider := range state.Providers {
		reason := "readiness_pending"
		if !provider.Enabled {
			reason = "disabled"
		}
		views[id] = ProviderView{ID: id, Type: provider.Type, Enabled: provider.Enabled, Revision: provider.Revision, Reason: reason}
		revisions[id] = provider.Revision
	}
	m.mu.Lock()
	m.views = views
	m.formActionOrigins = make(map[string][]string)
	m.samlMetadata = make(map[string]cachedSAMLMetadata)
	m.knownRevisions = revisions
	m.mu.Unlock()
	return nil
}

// Refresh probes every enabled provider independently. A failed provider only
// updates its own readiness and never changes local break-glass availability.
func (m *Manager) Refresh(ctx context.Context) error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	state, err := m.service.State(ctx)
	if err != nil {
		return err
	}
	type result struct {
		id       string
		view     ProviderView
		origins  []string
		metadata []byte
	}
	results := make(chan result, len(state.Providers))
	semaphore := make(chan struct{}, 4)
	var group sync.WaitGroup
	for id, provider := range state.Providers {
		id, provider := id, provider
		group.Add(1)
		go func() {
			defer group.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results <- result{id: id, view: unavailableView(provider, "readiness_cancelled")}
				return
			}
			view, origins, metadata := m.probe(ctx, provider)
			results <- result{id: id, view: view, origins: origins, metadata: metadata}
		}()
	}
	group.Wait()
	close(results)
	views := make(map[string]ProviderView, len(state.Providers))
	origins := make(map[string][]string)
	metadata := make(map[string]cachedSAMLMetadata)
	revisions := make(map[string]uint64, len(state.Providers))
	for item := range results {
		views[item.id] = item.view
		revisions[item.id] = item.view.Revision
		if len(item.origins) > 0 {
			origins[item.id] = append([]string(nil), item.origins...)
		}
		if len(item.metadata) > 0 {
			metadata[item.id] = cachedSAMLMetadata{revision: item.view.Revision, data: append([]byte(nil), item.metadata...)}
		}
	}
	m.mu.Lock()
	m.views = views
	m.formActionOrigins = origins
	m.samlMetadata = metadata
	m.knownRevisions = revisions
	m.mu.Unlock()
	return nil
}

func unavailableView(provider adminauth.ProviderState, reason string) ProviderView {
	return ProviderView{ID: provider.ID, Type: provider.Type, Enabled: provider.Enabled, Revision: provider.Revision, Reason: reason}
}

func (m *Manager) probe(ctx context.Context, provider adminauth.ProviderState) (ProviderView, []string, []byte) {
	config, err := m.decode(provider)
	if err != nil {
		return unavailableView(provider, "configuration_invalid"), nil, nil
	}
	view := ProviderView{ID: provider.ID, Type: provider.Type, Enabled: provider.Enabled, Revision: provider.Revision, Configuration: append(json.RawMessage(nil), provider.Configuration...)}
	if !provider.Enabled {
		view.Reason = "disabled"
		return view, nil, nil
	}
	switch provider.Type {
	case adminauth.MethodOIDC:
		adapter, err := NewOIDCProvider(ctx, provider.ID, *config.OIDC, m.resolver, m.client, m.service.Flows(), provider.Revision)
		if err != nil {
			view.Reason = "provider_unavailable"
			return view, nil, nil
		}
		view.Ready = true
		view.Fingerprint = adapter.Fingerprint()
	case adminauth.MethodSAML:
		adapter, err := NewSAMLProvider(ctx, provider.ID, provider.Revision, *config.SAML, m.resolver, m.client, m.service.Flows(), m.samlReplay)
		if err != nil {
			view.Reason = "provider_unavailable"
			return view, nil, nil
		}
		view.Ready = true
		view.Fingerprint = adapter.Fingerprint()
		metadata, err := adapter.Metadata()
		if err != nil {
			view.Ready = false
			view.Reason = "provider_unavailable"
			return view, nil, nil
		}
		return view, adapter.FormActionOrigins(), metadata
	default:
		view.Reason = "configuration_invalid"
	}
	return view, nil, nil
}

func (m *Manager) decode(provider adminauth.ProviderState) (ProviderConfig, error) {
	var config ProviderConfig
	decoder := json.NewDecoder(bytes.NewReader(provider.Configuration))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return ProviderConfig{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return ProviderConfig{}, fmt.Errorf("multiple SSO configuration values")
	}
	if config.ID != provider.ID || config.Type != provider.Type || config.Enabled != provider.Enabled {
		return ProviderConfig{}, fmt.Errorf("SSO provider state mismatch")
	}
	if err := config.validate(); err != nil {
		return ProviderConfig{}, err
	}
	if err := m.validateCanonicalEndpoints(config); err != nil {
		return ProviderConfig{}, err
	}
	return config, nil
}

func (m *Manager) validateCanonicalEndpoints(config ProviderConfig) error {
	switch config.Type {
	case adminauth.MethodOIDC:
		if config.OIDC.RedirectURL != m.canonicalOrigin+"/admin/api/auth/oidc/callback" {
			return fmt.Errorf("OIDC callback must match the canonical administrator callback")
		}
	case adminauth.MethodSAML:
		if config.SAML.ACSURL != m.canonicalOrigin+"/admin/api/auth/saml/acs" {
			return fmt.Errorf("SAML ACS must match the canonical administrator callback")
		}
		expectedMetadata := m.canonicalOrigin + "/admin/api/auth/saml/metadata?provider=" + url.QueryEscape(config.ID)
		if config.SAML.MetadataEndpoint != expectedMetadata {
			return fmt.Errorf("SAML metadata endpoint must match the canonical administrator endpoint")
		}
	}
	return nil
}

func (m *Manager) List(ctx context.Context) ([]ProviderView, error) {
	if err := m.Refresh(ctx); err != nil {
		return nil, err
	}
	m.mu.RLock()
	views := make([]ProviderView, 0, len(m.views))
	for _, view := range m.views {
		view.Configuration = append(json.RawMessage(nil), view.Configuration...)
		views = append(views, view)
	}
	m.mu.RUnlock()
	sort.Slice(views, func(i, j int) bool { return views[i].ID < views[j].ID })
	return views, nil
}

func (m *Manager) LoginProviders(ctx context.Context) ([]LoginProvider, error) {
	state, err := m.service.State(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.RLock()
	providers := make([]LoginProvider, 0, len(state.Providers))
	for id, provider := range state.Providers {
		if !provider.Enabled {
			continue
		}
		cached, ok := m.views[id]
		login := LoginProvider{ID: id, Type: provider.Type, Enabled: provider.Enabled, Reason: "readiness_pending"}
		if ok && cached.Revision == provider.Revision && cached.Enabled == provider.Enabled {
			login.Ready = cached.Ready
			login.Reason = cached.Reason
		}
		providers = append(providers, login)
	}
	m.mu.RUnlock()
	sort.Slice(providers, func(i, j int) bool { return providers[i].ID < providers[j].ID })
	return providers, nil
}

func (m *Manager) Readiness(ctx context.Context) (map[string]adminauth.ProviderReadiness, map[string]adminauth.ProviderReadiness, error) {
	providers, err := m.LoginProviders(ctx)
	if err != nil {
		return nil, nil, err
	}
	oidcProviders := make(map[string]adminauth.ProviderReadiness)
	samlProviders := make(map[string]adminauth.ProviderReadiness)
	for _, provider := range providers {
		readiness := adminauth.ProviderReadiness{Ready: provider.Ready, Reason: provider.Reason}
		if provider.Type == adminauth.MethodOIDC {
			oidcProviders[provider.ID] = readiness
		} else if provider.Type == adminauth.MethodSAML {
			samlProviders[provider.ID] = readiness
		}
	}
	return oidcProviders, samlProviders, nil
}

func (m *Manager) Upsert(ctx context.Context, principal adminauth.Principal, config ProviderConfig) (ProviderView, error) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	if err := config.validate(); err != nil {
		return ProviderView{}, err
	}
	if err := m.validateCanonicalEndpoints(config); err != nil {
		return ProviderView{}, err
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return ProviderView{}, err
	}
	state, err := m.service.State(ctx)
	if err != nil {
		return ProviderView{}, err
	}
	existing, exists := state.Providers[config.ID]
	expectedRevision := uint64(0)
	if exists {
		expectedRevision = existing.Revision
	}
	err = m.service.Mutate(ctx, principal, adminauth.AuditSSOChange, config.ID, func(next *adminauth.State) error {
		current, currentExists := next.Providers[config.ID]
		if currentExists != exists || currentExists && current.Revision != expectedRevision {
			return ErrProviderConflict
		}
		next.Providers[config.ID] = adminauth.ProviderState{
			ID:            config.ID,
			Type:          config.Type,
			Enabled:       config.Enabled,
			Revision:      expectedRevision,
			Configuration: append(json.RawMessage(nil), raw...),
		}
		return nil
	})
	if err != nil {
		return ProviderView{}, err
	}
	state, err = m.service.State(ctx)
	if err != nil {
		return ProviderView{}, err
	}
	provider := state.Providers[config.ID]
	view, origins, metadata := m.probe(ctx, provider)
	m.mu.Lock()
	m.knownRevisions[config.ID] = provider.Revision
	m.views[config.ID] = view
	if len(origins) == 0 {
		delete(m.formActionOrigins, config.ID)
	} else {
		m.formActionOrigins[config.ID] = append([]string(nil), origins...)
	}
	if len(metadata) == 0 {
		delete(m.samlMetadata, config.ID)
	} else {
		m.samlMetadata[config.ID] = cachedSAMLMetadata{revision: provider.Revision, data: append([]byte(nil), metadata...)}
	}
	m.mu.Unlock()
	return view, nil
}

func (m *Manager) Delete(ctx context.Context, principal adminauth.Principal, id string) error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	if !providerIDPattern.MatchString(id) {
		return ErrProviderNotFound
	}
	state, err := m.service.State(ctx)
	if err != nil {
		return err
	}
	provider, exists := state.Providers[id]
	if !exists {
		return ErrProviderNotFound
	}
	err = m.service.Mutate(ctx, principal, adminauth.AuditSSOChange, id, func(next *adminauth.State) error {
		current, ok := next.Providers[id]
		if !ok {
			return ErrProviderNotFound
		}
		if current.Revision != provider.Revision {
			return ErrProviderConflict
		}
		delete(next.Providers, id)
		return nil
	})
	if err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.knownRevisions, id)
	delete(m.views, id)
	delete(m.formActionOrigins, id)
	delete(m.samlMetadata, id)
	m.mu.Unlock()
	return nil
}

func (m *Manager) oidc(ctx context.Context, id string) (*OIDCProvider, error) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	provider, config, err := m.provider(ctx, id, adminauth.MethodOIDC)
	if err != nil {
		return nil, err
	}
	adapter, err := NewOIDCProvider(ctx, id, *config.OIDC, m.resolver, m.client, m.service.Flows(), provider.Revision)
	if err != nil {
		m.markUnavailable(provider)
		return nil, ErrProviderUnavailable
	}
	m.markReady(provider, adapter.Fingerprint(), nil)
	return adapter, nil
}

func (m *Manager) saml(ctx context.Context, id string) (*SAMLProvider, error) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	provider, config, err := m.provider(ctx, id, adminauth.MethodSAML)
	if err != nil {
		return nil, err
	}
	adapter, err := NewSAMLProvider(ctx, id, provider.Revision, *config.SAML, m.resolver, m.client, m.service.Flows(), m.samlReplay)
	if err != nil {
		m.markUnavailable(provider)
		return nil, ErrProviderUnavailable
	}
	m.markReady(provider, adapter.Fingerprint(), adapter.FormActionOrigins())
	return adapter, nil
}

func (m *Manager) provider(ctx context.Context, id string, method adminauth.AuthMethod) (adminauth.ProviderState, ProviderConfig, error) {
	if !providerIDPattern.MatchString(id) {
		return adminauth.ProviderState{}, ProviderConfig{}, ErrProviderNotFound
	}
	state, err := m.service.State(ctx)
	if err != nil {
		return adminauth.ProviderState{}, ProviderConfig{}, err
	}
	provider, ok := state.Providers[id]
	if !ok || provider.Type != method {
		return adminauth.ProviderState{}, ProviderConfig{}, ErrProviderNotFound
	}
	if !provider.Enabled {
		return adminauth.ProviderState{}, ProviderConfig{}, ErrProviderUnavailable
	}
	config, err := m.decode(provider)
	if err != nil {
		m.markUnavailable(provider)
		return adminauth.ProviderState{}, ProviderConfig{}, ErrProviderUnavailable
	}
	m.mu.Lock()
	m.knownRevisions[id] = provider.Revision
	m.mu.Unlock()
	return provider, config, nil
}

func (m *Manager) markUnavailable(provider adminauth.ProviderState) {
	m.mu.Lock()
	if m.knownRevisions[provider.ID] != provider.Revision {
		m.mu.Unlock()
		return
	}
	m.views[provider.ID] = unavailableView(provider, "provider_unavailable")
	delete(m.formActionOrigins, provider.ID)
	delete(m.samlMetadata, provider.ID)
	m.mu.Unlock()
}

func (m *Manager) markReady(provider adminauth.ProviderState, fingerprint string, origins []string) {
	m.mu.Lock()
	if m.knownRevisions[provider.ID] != provider.Revision {
		m.mu.Unlock()
		return
	}
	previous := m.views[provider.ID]
	if previous.Fingerprint != "" && previous.Fingerprint != fingerprint {
		delete(m.samlMetadata, provider.ID)
	}
	m.views[provider.ID] = ProviderView{ID: provider.ID, Type: provider.Type, Enabled: true, Revision: provider.Revision, Ready: true, Fingerprint: fingerprint, Configuration: append(json.RawMessage(nil), provider.Configuration...)}
	if len(origins) == 0 {
		delete(m.formActionOrigins, provider.ID)
	} else {
		m.formActionOrigins[provider.ID] = append([]string(nil), origins...)
	}
	m.mu.Unlock()
}

func (m *Manager) allowStart(source string) error {
	if ok, retry := m.throttle.Allow("sso:" + source); !ok {
		return &adminauth.RateLimitError{RetryAfter: retry}
	}
	return nil
}

func (m *Manager) StartOIDC(ctx context.Context, id, source, returnLocation string) (string, error) {
	if err := m.allowStart(source); err != nil {
		return "", err
	}
	provider, err := m.oidc(ctx, id)
	if err != nil {
		return "", err
	}
	return provider.Start(source, returnLocation)
}

func (m *Manager) CompleteOIDC(ctx context.Context, state, code, source string) (adminauth.Credentials, string, error) {
	id, handle, err := ParseFlowState(state)
	if err != nil {
		return adminauth.Credentials{}, "", adminauth.ErrReplay
	}
	flow, err := m.service.Flows().Consume(handle, id, adminauth.MethodOIDC)
	if err != nil {
		return adminauth.Credentials{}, "", err
	}
	provider, err := m.oidc(ctx, id)
	if err != nil {
		return adminauth.Credentials{}, "", err
	}
	identity, returnLocation, revision, err := provider.completeConsumed(ctx, flow, code, source)
	if err != nil {
		return adminauth.Credentials{}, "", err
	}
	credentials, err := m.service.IssueFederatedSession(ctx, identity.Provider, revision, identity.Subject, identity.Username, identity.Roles, identity.Method, source)
	return credentials, returnLocation, err
}

func (m *Manager) SAMLMetadata(ctx context.Context, id, source string) ([]byte, error) {
	state, err := m.service.State(ctx)
	if err != nil {
		return nil, err
	}
	configured, ok := state.Providers[id]
	if !ok || !configured.Enabled || configured.Type != adminauth.MethodSAML {
		return nil, ErrProviderNotFound
	}
	m.mu.RLock()
	cached, ok := m.samlMetadata[id]
	m.mu.RUnlock()
	if ok && cached.revision == configured.Revision {
		return append([]byte(nil), cached.data...), nil
	}
	if err := m.allowStart(source); err != nil {
		return nil, err
	}
	provider, err := m.saml(ctx, id)
	if err != nil {
		return nil, err
	}
	metadata, err := provider.Metadata()
	if err != nil {
		return nil, ErrProviderUnavailable
	}
	m.mu.Lock()
	if m.knownRevisions[id] == configured.Revision {
		m.samlMetadata[id] = cachedSAMLMetadata{revision: configured.Revision, data: append([]byte(nil), metadata...)}
	}
	m.mu.Unlock()
	return metadata, nil
}

func (m *Manager) StartSAML(ctx context.Context, id, source, returnLocation string) (string, error) {
	if err := m.allowStart(source); err != nil {
		return "", err
	}
	provider, err := m.saml(ctx, id)
	if err != nil {
		return "", err
	}
	return provider.Start(source, returnLocation)
}

func (m *Manager) CompleteSAML(ctx context.Context, id, relayState string, request *http.Request, source string) (adminauth.Credentials, string, error) {
	stateProvider, handle, parseErr := ParseFlowState(relayState)
	if parseErr != nil || stateProvider != id {
		return adminauth.Credentials{}, "", adminauth.ErrReplay
	}
	flow, err := m.service.Flows().Consume(handle, id, adminauth.MethodSAML)
	if err != nil {
		return adminauth.Credentials{}, "", err
	}
	provider, err := m.saml(ctx, id)
	if err != nil {
		return adminauth.Credentials{}, "", err
	}
	identity, returnLocation, revision, err := provider.completeConsumed(ctx, request, relayState, flow)
	if err != nil {
		return adminauth.Credentials{}, "", err
	}
	credentials, err := m.service.IssueFederatedSession(ctx, identity.Provider, revision, identity.Subject, identity.Username, identity.Roles, identity.Method, source)
	return credentials, returnLocation, err
}

func (m *Manager) FormActionOrigins() []string {
	m.mu.RLock()
	seen := make(map[string]struct{})
	for _, origins := range m.formActionOrigins {
		for _, origin := range origins {
			seen[origin] = struct{}{}
		}
	}
	m.mu.RUnlock()
	result := make([]string, 0, len(seen))
	for origin := range seen {
		result = append(result, origin)
	}
	sort.Strings(result)
	return result
}
