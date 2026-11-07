package adminsso

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"routenest/internal/adminauth"
)

const (
	oidcHTTPTimeout = 10 * time.Second
	oidcMaxBody     = 1 << 20
	oidcMaxIDToken  = 256 << 10
)

// OIDCProvider holds verified discovery metadata and an in-memory resolved
// secret. Neither upstream tokens nor authorization responses are persisted.
type OIDCProvider struct {
	id          string
	config      OIDCConfig
	oauth       oauth2.Config
	verifier    *oidc.IDTokenVerifier
	client      *http.Client
	flows       *adminauth.AuthFlowStore
	revision    uint64
	fingerprint string
	random      io.Reader
}

func (*OIDCProvider) String() string        { return "[redacted OIDC provider]" }
func (p *OIDCProvider) Fingerprint() string { return p.fingerprint }

// NewOIDCProvider performs discovery without weakening issuer, signature,
// audience or expiration checks. The caller still owns canonical callback and
// same-origin start enforcement at the HTTP boundary.
func NewOIDCProvider(ctx context.Context, id string, config OIDCConfig, resolver *SecretResolver, client *http.Client, flows *adminauth.AuthFlowStore, revision uint64) (*OIDCProvider, error) {
	if !providerIDPattern.MatchString(id) || resolver == nil || flows == nil || revision == 0 {
		return nil, fmt.Errorf("invalid OIDC provider dependencies")
	}
	config = cloneOIDCConfig(config)
	if err := config.validate(); err != nil {
		return nil, err
	}
	redirect, _ := url.Parse(config.RedirectURL)
	redirectIP := net.ParseIP(redirect.Hostname())
	if !oidcHTTPSURL(config.Issuer) || len(config.RedirectURL) > 4096 || redirect.Scheme != "https" && !(redirect.Scheme == "http" && redirectIP != nil && redirectIP.IsLoopback()) {
		return nil, fmt.Errorf("OIDC redirect requires HTTPS or direct loopback HTTP")
	}
	if !oidcSafeText(config.ClientID, 512, false) {
		return nil, fmt.Errorf("invalid OIDC client ID")
	}
	if !oidcSafeText(config.UsernameClaim, 256, true) || !oidcSafeText(config.GroupsClaim, 256, true) {
		return nil, fmt.Errorf("invalid OIDC claim name")
	}
	scopes, err := oidcScopes(config.Scopes)
	if err != nil {
		return nil, err
	}
	secret, fingerprint, err := resolver.Resolve(config.ClientSecret)
	if err != nil {
		return nil, fmt.Errorf("OIDC client secret unavailable")
	}
	defer clear(secret)
	if len(secret) > 4096 {
		return nil, fmt.Errorf("OIDC client secret exceeds size limit")
	}
	client = oidcBoundedClient(client)
	ctx, cancel := context.WithTimeout(oidc.ClientContext(ctx, client), oidcHTTPTimeout)
	defer cancel()
	provider, err := oidc.NewProvider(ctx, config.Issuer)
	if err != nil {
		// Library errors can include untrusted response bodies. Never return them
		// across the adapter boundary, even for readiness diagnostics.
		return nil, fmt.Errorf("OIDC discovery failed")
	}
	var metadata struct {
		JWKSURL     string   `json:"jwks_uri"`
		Algorithms  []string `json:"id_token_signing_alg_values_supported"`
		AuthMethods []string `json:"token_endpoint_auth_methods_supported"`
		Challenges  []string `json:"code_challenge_methods_supported"`
		Responses   []string `json:"response_types_supported"`
	}
	if err := provider.Claims(&metadata); err != nil {
		return nil, fmt.Errorf("invalid OIDC discovery metadata")
	}
	endpoint := provider.Endpoint()
	for _, raw := range []string{endpoint.AuthURL, endpoint.TokenURL, metadata.JWKSURL} {
		if !oidcHTTPSURL(raw) {
			return nil, fmt.Errorf("OIDC endpoints must use HTTPS")
		}
	}
	if len(metadata.Challenges) > 0 && !oidcContains(metadata.Challenges, "S256") || len(metadata.Responses) > 0 && !oidcContains(metadata.Responses, "code") {
		return nil, fmt.Errorf("OIDC provider lacks authorization code or S256 support")
	}
	switch {
	case len(metadata.AuthMethods) == 0 || oidcContains(metadata.AuthMethods, "client_secret_basic"):
		endpoint.AuthStyle = oauth2.AuthStyleInHeader
	case oidcContains(metadata.AuthMethods, "client_secret_post"):
		endpoint.AuthStyle = oauth2.AuthStyleInParams
	default:
		return nil, fmt.Errorf("unsupported OIDC token endpoint authentication")
	}
	algs := make([]string, 0, len(metadata.Algorithms))
	for _, alg := range metadata.Algorithms {
		switch alg {
		case oidc.RS256, oidc.RS384, oidc.RS512, oidc.ES256, oidc.ES384, oidc.ES512, oidc.PS256, oidc.PS384, oidc.PS512, oidc.EdDSA:
			algs = append(algs, alg)
		}
	}
	if len(algs) == 0 {
		return nil, fmt.Errorf("OIDC provider lacks supported signing algorithms")
	}
	return &OIDCProvider{
		id: id, config: config, client: client, flows: flows, revision: revision,
		fingerprint: fingerprint, random: rand.Reader,
		oauth:    oauth2.Config{ClientID: config.ClientID, ClientSecret: string(secret), Endpoint: endpoint, RedirectURL: config.RedirectURL, Scopes: scopes},
		verifier: provider.Verifier(&oidc.Config{ClientID: config.ClientID, SupportedSigningAlgs: algs}),
	}, nil
}

func (p *OIDCProvider) Start(source, returnLocation string) (string, error) {
	if !oidcSafeText(source, 256, true) {
		return "", adminauth.ErrUnauthorized
	}
	nonce, err := oidcRandomToken(p.random)
	if err != nil {
		return "", err
	}
	verifier, err := oidcRandomToken(p.random)
	if err != nil {
		return "", err
	}
	handle, err := p.flows.Create(adminauth.AuthFlow{
		Provider: p.id, ProviderRevision: p.revision, ProviderFingerprint: p.fingerprint, Type: adminauth.MethodOIDC,
		Source: source, Nonce: nonce, PKCEVerifier: verifier,
		ExpectedIssuer: p.config.Issuer, Callback: p.config.RedirectURL, ReturnLocation: returnLocation,
	})
	if err != nil {
		return "", err
	}
	state, err := encodeFlowState(p.id, handle)
	if err != nil {
		_, _ = p.flows.Consume(handle, p.id, adminauth.MethodOIDC)
		return "", err
	}
	return p.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), nil
}

// Complete consumes state before any code exchange or claim processing. A
// failed callback therefore cannot retry the same authorization transaction.
func (p *OIDCProvider) Complete(ctx context.Context, state, code, source string) (FederatedIdentity, string, uint64, error) {
	provider, handle, err := ParseFlowState(state)
	if err != nil || provider != p.id || !oidcOpaqueHandle(handle) {
		return FederatedIdentity{}, "", 0, adminauth.ErrReplay
	}
	flow, err := p.flows.Consume(handle, p.id, adminauth.MethodOIDC)
	if err != nil {
		return FederatedIdentity{}, "", 0, err
	}
	return p.completeConsumed(ctx, flow, code, source)
}

// completeConsumed verifies a flow that the manager atomically consumed before
// performing discovery or token-endpoint I/O. Keeping this separate prevents
// forged callback states from turning public callbacks into outbound probes.
func (p *OIDCProvider) completeConsumed(ctx context.Context, flow adminauth.AuthFlow, code, source string) (FederatedIdentity, string, uint64, error) {
	if flow.ExpectedIssuer != p.config.Issuer || flow.Callback != p.config.RedirectURL || flow.ProviderRevision != p.revision || flow.ProviderFingerprint != p.fingerprint || !oidcSafeText(source, 256, true) || !oidcOpaqueHandle(flow.Nonce) || !oidcOpaqueHandle(flow.PKCEVerifier) || !oidcSafeText(code, 4096, false) {
		return FederatedIdentity{}, "", 0, adminauth.ErrUnauthorized
	}
	ctx, cancel := context.WithTimeout(oidc.ClientContext(ctx, p.client), oidcHTTPTimeout)
	defer cancel()
	token, err := p.oauth.Exchange(ctx, code, oauth2.VerifierOption(flow.PKCEVerifier))
	if err != nil {
		return FederatedIdentity{}, "", 0, adminauth.ErrUnauthorized
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok || len(raw) == 0 || len(raw) > oidcMaxIDToken {
		return FederatedIdentity{}, "", 0, adminauth.ErrUnauthorized
	}
	idToken, err := p.verifier.Verify(ctx, raw)
	if err != nil {
		return FederatedIdentity{}, "", 0, adminauth.ErrUnauthorized
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(flow.Nonce)) != 1 || idToken.Issuer != flow.ExpectedIssuer || !idToken.Expiry.After(time.Now()) || idToken.IssuedAt.IsZero() || idToken.IssuedAt.After(time.Now().Add(2*time.Minute)) || !oidcSafeText(idToken.Subject, 512, false) {
		return FederatedIdentity{}, "", 0, adminauth.ErrUnauthorized
	}
	if idToken.AccessTokenHash != "" && idToken.VerifyAccessToken(token.AccessToken) != nil {
		return FederatedIdentity{}, "", 0, adminauth.ErrUnauthorized
	}
	var claims map[string]json.RawMessage
	if err := idToken.Claims(&claims); err != nil {
		return FederatedIdentity{}, "", 0, adminauth.ErrUnauthorized
	}
	if raw, exists := claims["azp"]; exists {
		var party string
		if json.Unmarshal(raw, &party) != nil || party != p.config.ClientID {
			return FederatedIdentity{}, "", 0, adminauth.ErrUnauthorized
		}
	} else if len(idToken.Audience) > 1 {
		return FederatedIdentity{}, "", 0, adminauth.ErrUnauthorized
	}
	usernameClaim := p.config.UsernameClaim
	if usernameClaim == "" {
		usernameClaim = "preferred_username"
	}
	username := idToken.Subject
	if raw, exists := claims[usernameClaim]; exists {
		var claimedUsername string
		if json.Unmarshal(raw, &claimedUsername) != nil || !oidcSafeText(claimedUsername, adminauth.MaxUsernameBytes, false) {
			return FederatedIdentity{}, "", 0, adminauth.ErrUnauthorized
		}
		username = claimedUsername
	} else if p.config.UsernameClaim != "" {
		return FederatedIdentity{}, "", 0, adminauth.ErrUnauthorized
	}
	username = adminauth.NormalizeUsername(username)
	if !oidcSafeText(username, adminauth.MaxUsernameBytes, false) {
		return FederatedIdentity{}, "", 0, adminauth.ErrUnauthorized
	}
	groupsClaim := p.config.GroupsClaim
	if groupsClaim == "" {
		groupsClaim = "groups"
	}
	var groups []string
	if raw, exists := claims[groupsClaim]; exists {
		// json.Unmarshal accepts null as a nil slice. Require an actual array
		// so null, scalars and mixed-type arrays never acquire default roles.
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || trimmed[0] != '[' || json.Unmarshal(raw, &groups) != nil || len(groups) > 256 {
			return FederatedIdentity{}, "", 0, adminauth.ErrUnauthorized
		}
		for _, group := range groups {
			if !oidcSafeText(group, 512, false) {
				return FederatedIdentity{}, "", 0, adminauth.ErrUnauthorized
			}
		}
	}
	roles := mapRoles(p.config.DefaultRoles, p.config.GroupRoleMap, groups)
	if len(roles) == 0 {
		return FederatedIdentity{}, "", 0, adminauth.ErrForbidden
	}
	return FederatedIdentity{Provider: p.id, Subject: idToken.Subject, Username: username, Roles: roles, Method: adminauth.MethodOIDC}, flow.ReturnLocation, flow.ProviderRevision, nil
}

func cloneOIDCConfig(config OIDCConfig) OIDCConfig {
	config.Scopes = append([]string(nil), config.Scopes...)
	config.DefaultRoles = append([]adminauth.Role(nil), config.DefaultRoles...)
	mapping := make(map[string][]adminauth.Role, len(config.GroupRoleMap))
	for group, roles := range config.GroupRoleMap {
		mapping[group] = append([]adminauth.Role(nil), roles...)
	}
	config.GroupRoleMap = mapping
	return config
}

func oidcRandomToken(random io.Reader) (string, error) {
	var data [32]byte
	if _, err := io.ReadFull(random, data[:]); err != nil {
		return "", fmt.Errorf("secure OIDC random generation failed")
	}
	return base64.RawURLEncoding.EncodeToString(data[:]), nil
}

func oidcOpaqueHandle(value string) bool {
	if len(value) != 43 {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == 32 && base64.RawURLEncoding.EncodeToString(decoded) == value
}

func oidcSafeText(value string, limit int, empty bool) bool {
	if len(value) > limit || !utf8.ValidString(value) || !empty && strings.TrimSpace(value) == "" {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func oidcHTTPSURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.Fragment == "" && len(raw) <= 4096
}

func oidcContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func oidcScopes(configured []string) ([]string, error) {
	if len(configured) == 0 {
		configured = []string{"profile", "email"}
	}
	if len(configured) > 128 {
		return nil, fmt.Errorf("too many OIDC scopes")
	}
	scopes := []string{oidc.ScopeOpenID}
	seen := map[string]bool{oidc.ScopeOpenID: true}
	for _, scope := range configured {
		if scope == "" || len(scope) > 128 {
			return nil, fmt.Errorf("invalid OIDC scope")
		}
		for _, r := range scope {
			// RFC 6749 scope-token excludes spaces, quotes and backslashes.
			if r < '!' || r > '~' || r == '"' || r == '\\' {
				return nil, fmt.Errorf("invalid OIDC scope")
			}
		}
		if !seen[scope] {
			scopes = append(scopes, scope)
			seen[scope] = true
		}
	}
	return scopes, nil
}

// oidcResponseTransport limits bodies before the discovery/JWKS library reads
// them. Fully buffering one bounded response also detects oversized input
// instead of silently accepting a truncated but valid JSON prefix.
type oidcResponseTransport struct{ base http.RoundTripper }

func (t oidcResponseTransport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func (t oidcResponseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.ContentLength > oidcMaxBody {
		return nil, fmt.Errorf("OIDC response exceeds size limit")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, oidcMaxBody+1))
	if err != nil {
		return nil, fmt.Errorf("OIDC response read failed")
	}
	if len(body) > oidcMaxBody {
		return nil, fmt.Errorf("OIDC response exceeds size limit")
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, nil
}

func oidcBoundedClient(supplied *http.Client) *http.Client {
	// Manager adapters share one already-bounded client. Repeated discovery
	// must not clone transports and retain a fresh idle pool on every refresh.
	if supplied != nil {
		switch supplied.Transport.(type) {
		case oidcResponseTransport, managerTransport:
			return supplied
		}
	}
	var client http.Client
	if supplied != nil {
		client = *supplied
	}
	client.Jar = nil
	if client.Timeout <= 0 || client.Timeout > oidcHTTPTimeout {
		client.Timeout = oidcHTTPTimeout
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if concrete, ok := transport.(*http.Transport); ok {
		bounded := concrete.Clone()
		bounded.MaxResponseHeaderBytes = 32 << 10
		if bounded.ResponseHeaderTimeout <= 0 || bounded.ResponseHeaderTimeout > oidcHTTPTimeout {
			bounded.ResponseHeaderTimeout = oidcHTTPTimeout
		}
		if bounded.TLSHandshakeTimeout <= 0 || bounded.TLSHandshakeTimeout > oidcHTTPTimeout {
			bounded.TLSHandshakeTimeout = oidcHTTPTimeout
		}
		transport = bounded
	}
	client.Transport = oidcResponseTransport{base: transport}
	return &client
}
