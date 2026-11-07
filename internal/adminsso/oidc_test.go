package adminsso

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"golang.org/x/oauth2"

	"routenest/internal/adminauth"
)

type oidcIssuerFixture struct {
	mu            sync.Mutex
	server        *httptest.Server
	signer        jose.Signer
	publicKey     jose.JSONWebKey
	metadata      map[string]any
	claims        map[string]any
	nonce         string
	challenge     string
	verifier      string
	tokenRequests int
	tokenError    bool
	tokenExtra    map[string]any
	omitClaims    []string
	oversizedPath string
	redirectPath  string
	redirectURL   string
	callbackURL   string
}

func newOIDCIssuerFixture(t *testing.T) *oidcIssuerFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privateJWK := jose.JSONWebKey{Key: key, KeyID: "fixture-key", Use: "sig", Algorithm: string(jose.RS256)}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: privateJWK}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		t.Fatal(err)
	}
	f := &oidcIssuerFixture{signer: signer, publicKey: privateJWK.Public(), claims: map[string]any{}, metadata: map[string]any{}}
	f.server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	f.callbackURL = f.server.URL + "/callback"
	f.metadata = map[string]any{
		"issuer": f.server.URL, "authorization_endpoint": f.server.URL + "/authorize", "token_endpoint": f.server.URL + "/token", "jwks_uri": f.server.URL + "/jwks",
		"id_token_signing_alg_values_supported": []string{"RS256"}, "token_endpoint_auth_methods_supported": []string{"client_secret_basic"},
		"code_challenge_methods_supported": []string{"S256"}, "response_types_supported": []string{"code"},
	}
	return f
}

func (f *oidcIssuerFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == f.oversizedPath {
		_, _ = io.WriteString(w, strings.Repeat(" ", oidcMaxBody+1))
		return
	}
	if r.URL.Path == f.redirectPath {
		http.Redirect(w, r, f.redirectURL, http.StatusFound)
		return
	}
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		_ = json.NewEncoder(w).Encode(f.metadata)
	case "/jwks":
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{f.publicKey}})
	case "/token":
		f.tokenRequests++
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		clientID, clientSecret, basic := r.BasicAuth()
		if !basic {
			clientID, clientSecret = r.Form.Get("client_id"), r.Form.Get("client_secret")
		}
		f.verifier = r.Form.Get("code_verifier")
		if clientID != "fixture-client" || clientSecret != "fixture-client-secret" || r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "valid-code" || r.Form.Get("redirect_uri") != f.callbackURL || f.verifier == "" || oauth2.S256ChallengeFromVerifier(f.verifier) != f.challenge {
			http.Error(w, "invalid authorization exchange", http.StatusBadRequest)
			return
		}
		if f.tokenError {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant", "error_description": "fixture-client-secret raw-private-token"})
			return
		}
		claims := map[string]any{
			"iss": f.server.URL, "aud": "fixture-client", "sub": "subject-123", "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
			"nonce": f.nonce, "preferred_username": " User@example.test ", "groups": []string{"operators"},
		}
		for key, value := range f.claims {
			claims[key] = value
		}
		for _, name := range f.omitClaims {
			delete(claims, name)
		}
		payload, err := json.Marshal(claims)
		if err != nil {
			http.Error(w, "fixture marshal failed", http.StatusInternalServerError)
			return
		}
		signed, err := f.signer.Sign(payload)
		if err != nil {
			http.Error(w, "fixture signing failed", http.StatusInternalServerError)
			return
		}
		raw, err := signed.CompactSerialize()
		if err != nil {
			http.Error(w, "fixture serialization failed", http.StatusInternalServerError)
			return
		}
		response := map[string]any{"access_token": "raw-private-token", "token_type": "Bearer", "expires_in": 3600, "id_token": raw, "refresh_token": "private-refresh-token"}
		for key, value := range f.tokenExtra {
			response[key] = value
		}
		_ = json.NewEncoder(w).Encode(response)
	default:
		http.NotFound(w, r)
	}
}

func (f *oidcIssuerFixture) config(t *testing.T) OIDCConfig {
	t.Helper()
	t.Setenv("ROUTENEST_TEST_OIDC_SECRET", "fixture-client-secret")
	return OIDCConfig{
		Issuer: f.server.URL, ClientID: "fixture-client", ClientSecret: SecretRef{Kind: "env", Name: "ROUTENEST_TEST_OIDC_SECRET"}, RedirectURL: f.callbackURL,
		GroupRoleMap: map[string][]adminauth.Role{"operators": {adminauth.RoleProviderAdmin}},
	}
}

func (f *oidcIssuerFixture) provider(t *testing.T, mutate func(*OIDCConfig)) *OIDCProvider {
	t.Helper()
	config := f.config(t)
	if mutate != nil {
		mutate(&config)
	}
	resolver, err := NewSecretResolver(nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewOIDCProvider(t.Context(), "fixture", config, resolver, f.server.Client(), adminauth.NewAuthFlowStore(), 7)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.client.CloseIdleConnections)
	return p
}

func (f *oidcIssuerFixture) start(t *testing.T, p *OIDCProvider) url.Values {
	t.Helper()
	authorizationURL, err := p.Start("127.0.0.1", "/admin/#security")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	values := parsed.Query()
	if parsed.Scheme != "https" || parsed.Host != strings.TrimPrefix(f.server.URL, "https://") || parsed.Path != "/authorize" || values.Get("client_id") != "fixture-client" || values.Get("response_type") != "code" || values.Get("redirect_uri") != f.callbackURL {
		t.Fatal("invalid authorization request")
	}
	if values.Get("code_challenge_method") != "S256" || !oidcOpaqueHandle(values.Get("code_challenge")) || !oidcOpaqueHandle(values.Get("nonce")) || !strings.HasPrefix(values.Get("state"), "fixture.") || values.Get("client_secret") != "" || values.Get("code_verifier") != "" || !oidcContains(strings.Fields(values.Get("scope")), "openid") {
		t.Fatal("authorization request lacks safe state, nonce, PKCE or scopes")
	}
	f.mu.Lock()
	f.nonce, f.challenge = values.Get("nonce"), values.Get("code_challenge")
	f.mu.Unlock()
	return values
}

func TestOIDCVerifiedSuccessAndStateReplay(t *testing.T) {
	f := newOIDCIssuerFixture(t)
	p := f.provider(t, func(c *OIDCConfig) { c.DefaultRoles = []adminauth.Role{adminauth.RoleViewer} })
	values := f.start(t, p)
	// Callback transport can differ from the start transport; protocol state,
	// nonce and PKCE bind the transaction independently of the client's IP.
	identity, location, revision, err := p.Complete(t.Context(), values.Get("state"), "valid-code", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if identity.Provider != "fixture" || identity.Subject != "subject-123" || identity.Username != "user@example.test" || identity.Method != adminauth.MethodOIDC || len(identity.Roles) != 2 || identity.Roles[0] != adminauth.RoleProviderAdmin || identity.Roles[1] != adminauth.RoleViewer || location != "/admin/#security" || revision != 7 {
		t.Fatal("verified identity or flow context is incorrect", identity, location, revision)
	}
	f.mu.Lock()
	verifier, requests := f.verifier, f.tokenRequests
	f.mu.Unlock()
	if !oidcOpaqueHandle(verifier) || oauth2.S256ChallengeFromVerifier(verifier) != values.Get("code_challenge") || requests != 1 {
		t.Fatal("token endpoint did not receive the stored PKCE verifier")
	}
	if _, _, _, err := p.Complete(t.Context(), values.Get("state"), "valid-code", "127.0.0.1"); !errors.Is(err, adminauth.ErrReplay) {
		t.Fatal("state replay accepted", err)
	}
	f.mu.Lock()
	requests = f.tokenRequests
	f.mu.Unlock()
	if requests != 1 {
		t.Fatal("state replay reached token endpoint")
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"fixture-client-secret", "raw-private-token", "private-refresh-token", verifier, values.Get("nonce")} {
		if strings.Contains(string(encoded), secret) || strings.Contains(fmt.Sprint(p), secret) {
			t.Fatal("provider or identity exposes an upstream secret")
		}
	}
	if p.Fingerprint() == "" {
		t.Fatal("resolved secret lacks a fingerprint")
	}
}

func TestOIDCRejectsHostileSignedClaimsAndConsumesState(t *testing.T) {
	cases := []struct {
		name   string
		claims map[string]any
		want   error
	}{
		{"nonce mismatch", map[string]any{"nonce": "wrong-nonce"}, adminauth.ErrUnauthorized},
		{"nonce missing", map[string]any{"nonce": nil}, adminauth.ErrUnauthorized},
		{"issuer mismatch", map[string]any{"iss": "https://attacker.example"}, adminauth.ErrUnauthorized},
		{"audience mismatch", map[string]any{"aud": "another-client"}, adminauth.ErrUnauthorized},
		{"expired token", map[string]any{"exp": time.Now().Add(-time.Minute).Unix()}, adminauth.ErrUnauthorized},
		{"future issue time", map[string]any{"iat": time.Now().Add(time.Hour).Unix()}, adminauth.ErrUnauthorized},
		{"missing issue time", map[string]any{"iat": nil}, adminauth.ErrUnauthorized},
		{"username object", map[string]any{"preferred_username": map[string]any{"name": "admin"}}, adminauth.ErrUnauthorized},
		{"username numeric", map[string]any{"preferred_username": 123}, adminauth.ErrUnauthorized},
		{"username null", map[string]any{"preferred_username": nil}, adminauth.ErrUnauthorized},
		{"username controls", map[string]any{"preferred_username": "admin\nforged"}, adminauth.ErrUnauthorized},
		{"username oversized", map[string]any{"preferred_username": strings.Repeat("a", 129)}, adminauth.ErrUnauthorized},
		{"subject empty", map[string]any{"sub": ""}, adminauth.ErrUnauthorized},
		{"subject type confusion", map[string]any{"sub": []string{"admin"}}, adminauth.ErrUnauthorized},
		{"groups scalar", map[string]any{"groups": "operators"}, adminauth.ErrUnauthorized},
		{"groups object", map[string]any{"groups": map[string]any{"operators": true}}, adminauth.ErrUnauthorized},
		{"groups null", map[string]any{"groups": nil}, adminauth.ErrUnauthorized},
		{"groups mixed types", map[string]any{"groups": []any{"operators", 12}}, adminauth.ErrUnauthorized},
		{"groups null element", map[string]any{"groups": []any{"operators", nil}}, adminauth.ErrUnauthorized},
		{"groups excessive count", map[string]any{"groups": make([]string, 257)}, adminauth.ErrUnauthorized},
		{"groups excessive length", map[string]any{"groups": []string{strings.Repeat("g", 513)}}, adminauth.ErrUnauthorized},
		{"unmapped groups", map[string]any{"groups": []string{"unmapped"}}, adminauth.ErrForbidden},
		{"no groups or roles", map[string]any{"groups": []string{}}, adminauth.ErrForbidden},
		{"authorized party mismatch", map[string]any{"azp": "another-client"}, adminauth.ErrUnauthorized},
		{"authorized party type confusion", map[string]any{"azp": 12}, adminauth.ErrUnauthorized},
		{"multiple audiences missing azp", map[string]any{"aud": []string{"fixture-client", "another-client"}}, adminauth.ErrUnauthorized},
		{"bad access token hash", map[string]any{"at_hash": "not-valid"}, adminauth.ErrUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newOIDCIssuerFixture(t)
			p := f.provider(t, nil)
			values := f.start(t, p)
			f.mu.Lock()
			f.claims = tc.claims
			f.mu.Unlock()
			identity, location, revision, err := p.Complete(t.Context(), values.Get("state"), "valid-code", "127.0.0.1")
			if !errors.Is(err, tc.want) || identity.Subject != "" || location != "" || revision != 0 {
				t.Fatal("hostile claims escaped verification", err)
			}
			if _, _, _, err := p.Complete(t.Context(), values.Get("state"), "valid-code", "127.0.0.1"); !errors.Is(err, adminauth.ErrReplay) {
				t.Fatal("failed verification did not consume state", err)
			}
		})
	}
}

func TestOIDCStateBindingAndCancellation(t *testing.T) {
	for _, mode := range []string{"invalid code", "invalid source", "provider revision", "secret fingerprint", "callback mismatch", "issuer binding", "canceled callback"} {
		t.Run(mode, func(t *testing.T) {
			f := newOIDCIssuerFixture(t)
			p := f.provider(t, nil)
			values := f.start(t, p)
			code, source := "valid-code", "127.0.0.1"
			ctx := t.Context()
			switch mode {
			case "invalid code":
				code = ""
			case "invalid source":
				source = "192.0.2.1\nforged"
			case "provider revision":
				p.revision++
			case "secret fingerprint":
				p.fingerprint = "rotated-fingerprint"
			case "callback mismatch":
				p.config.RedirectURL += "/changed"
			case "issuer binding":
				p.config.Issuer += "/changed"
			case "canceled callback":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, _, _, err := p.Complete(ctx, values.Get("state"), code, source); !errors.Is(err, adminauth.ErrUnauthorized) {
				t.Fatal("unbound or canceled callback accepted", err)
			}
			if _, _, _, err := p.Complete(t.Context(), values.Get("state"), "valid-code", "127.0.0.1"); !errors.Is(err, adminauth.ErrReplay) {
				t.Fatal("invalid callback did not burn state", err)
			}
			f.mu.Lock()
			requests := f.tokenRequests
			f.mu.Unlock()
			if requests != 0 {
				t.Fatal("unbound callback contacted issuer")
			}
		})
	}
}

func TestOIDCRejectsMalformedStateAndOpenRedirect(t *testing.T) {
	f := newOIDCIssuerFixture(t)
	p := f.provider(t, nil)
	for _, location := range []string{"//attacker.example", "https://attacker.example", "/admin\nLocation: https://attacker.example", "/\\attacker.example"} {
		if _, err := p.Start("127.0.0.1", location); err == nil {
			t.Fatal("unsafe return location accepted")
		}
	}
	values := f.start(t, p)
	for _, state := range []string{"", "fixture", "fixture.short", "other." + strings.TrimPrefix(values.Get("state"), "fixture."), values.Get("state") + ".extra", strings.ToUpper(values.Get("state"))} {
		if _, _, _, err := p.Complete(t.Context(), state, "valid-code", "127.0.0.1"); !errors.Is(err, adminauth.ErrReplay) {
			t.Fatal("malformed state accepted", err)
		}
	}
	if _, _, _, err := p.Complete(t.Context(), values.Get("state"), "valid-code", "127.0.0.1"); err != nil {
		t.Fatal("malformed unrelated state consumed the valid flow", err)
	}
}

func TestOIDCDiscoveryReadinessFailsClosed(t *testing.T) {
	for _, mode := range []string{"issuer mismatch", "HTTP authorization", "HTTP token", "HTTP JWKS", "unsupported PKCE", "unsupported response", "unsigned algorithms", "unsupported client authentication", "oversized discovery", "redirect discovery"} {
		t.Run(mode, func(t *testing.T) {
			f := newOIDCIssuerFixture(t)
			config := f.config(t)
			f.mu.Lock()
			switch mode {
			case "issuer mismatch":
				f.metadata["issuer"] = "https://attacker.example"
			case "HTTP authorization":
				f.metadata["authorization_endpoint"] = "http://attacker.example/authorize"
			case "HTTP token":
				f.metadata["token_endpoint"] = "http://attacker.example/token"
			case "HTTP JWKS":
				f.metadata["jwks_uri"] = "http://attacker.example/jwks"
			case "unsupported PKCE":
				f.metadata["code_challenge_methods_supported"] = []string{"plain"}
			case "unsupported response":
				f.metadata["response_types_supported"] = []string{"token"}
			case "unsigned algorithms":
				f.metadata["id_token_signing_alg_values_supported"] = []string{"none", "HS256"}
			case "unsupported client authentication":
				f.metadata["token_endpoint_auth_methods_supported"] = []string{"private_key_jwt"}
			case "oversized discovery":
				f.oversizedPath = "/.well-known/openid-configuration"
			case "redirect discovery":
				f.redirectPath, f.redirectURL = "/.well-known/openid-configuration", f.server.URL+"/jwks"
			}
			f.mu.Unlock()
			resolver, err := NewSecretResolver(nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := NewOIDCProvider(t.Context(), "fixture", config, resolver, f.server.Client(), adminauth.NewAuthFlowStore(), 1); err == nil {
				t.Fatal("unsafe discovery declared ready")
			}
		})
	}
}

func TestOIDCUpstreamLimitsAndSecretErrors(t *testing.T) {
	for _, mode := range []string{"token error", "token oversized", "JWKS oversized", "wrong id token type", "oversized id token", "token redirect"} {
		t.Run(mode, func(t *testing.T) {
			f := newOIDCIssuerFixture(t)
			p := f.provider(t, nil)
			values := f.start(t, p)
			f.mu.Lock()
			switch mode {
			case "token error":
				f.tokenError = true
			case "token oversized":
				f.oversizedPath = "/token"
			case "JWKS oversized":
				f.oversizedPath = "/jwks"
			case "wrong id token type":
				f.tokenExtra = map[string]any{"id_token": map[string]any{"sub": "admin"}}
			case "oversized id token":
				f.tokenExtra = map[string]any{"id_token": strings.Repeat("a", oidcMaxIDToken+1)}
			case "token redirect":
				f.redirectPath, f.redirectURL = "/token", f.server.URL+"/jwks"
			}
			f.mu.Unlock()
			if _, _, _, err := p.Complete(t.Context(), values.Get("state"), "valid-code", "127.0.0.1"); !errors.Is(err, adminauth.ErrUnauthorized) || strings.Contains(err.Error(), "fixture-client-secret") || strings.Contains(err.Error(), "raw-private-token") {
				t.Fatal("upstream response accepted or secret leaked", err)
			}
		})
	}
}

func TestOIDCExplicitMappingAndImmutableConfiguration(t *testing.T) {
	f := newOIDCIssuerFixture(t)
	config := f.config(t)
	config.UsernameClaim = "custom_username"
	config.GroupsClaim = "custom_groups"
	config.Scopes = []string{"openid", "profile", "custom"}
	resolver, err := NewSecretResolver(nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewOIDCProvider(t.Context(), "fixture", config, resolver, f.server.Client(), adminauth.NewAuthFlowStore(), 7)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.client.CloseIdleConnections)
	config.GroupRoleMap["operators"][0] = adminauth.RoleBreakGlass
	config.Scopes[1] = "malicious"
	values := f.start(t, p)
	if values.Get("scope") != "openid profile custom" {
		t.Fatal("caller mutated active scopes")
	}
	f.mu.Lock()
	f.claims = map[string]any{"custom_username": "MappedUser", "custom_groups": []string{"operators"}, "roles": []string{"break_glass", "security_admin"}, "azp": "fixture-client", "aud": []string{"fixture-client", "other"}}
	f.mu.Unlock()
	identity, _, _, err := p.Complete(t.Context(), values.Get("state"), "valid-code", "127.0.0.1")
	if err != nil || identity.Username != "mappeduser" || len(identity.Roles) != 1 || identity.Roles[0] != adminauth.RoleProviderAdmin {
		t.Fatal("explicit role mapping was bypassed", identity, err)
	}
}

func TestOIDCRandomFailureAndClientBounds(t *testing.T) {
	f := newOIDCIssuerFixture(t)
	p := f.provider(t, nil)
	p.random = strings.NewReader("")
	if _, err := p.Start("127.0.0.1", "/admin/"); err == nil {
		t.Fatal("random failure accepted")
	}
	if p.client.Timeout <= 0 || p.client.Timeout > oidcHTTPTimeout || p.client.Jar != nil {
		t.Fatal("unbounded or cookie-bearing OIDC client")
	}
	if err := p.client.CheckRedirect(&http.Request{}, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatal("OIDC client follows redirects")
	}
}

func TestOIDCInvalidSignature(t *testing.T) {
	f := newOIDCIssuerFixture(t)
	p := f.provider(t, nil)
	values := f.start(t, p)
	wrongKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.publicKey.Key = &wrongKey.PublicKey
	f.mu.Unlock()
	if _, _, _, err := p.Complete(t.Context(), values.Get("state"), "valid-code", "127.0.0.1"); !errors.Is(err, adminauth.ErrUnauthorized) {
		t.Fatal("invalid signature accepted", err)
	}
}

func TestOIDCMissingClaimsAndDefaultRolePolicy(t *testing.T) {
	for _, mode := range []string{"subject username fallback", "explicit username missing", "default roles only", "malformed groups with defaults", "missing groups without defaults"} {
		t.Run(mode, func(t *testing.T) {
			f := newOIDCIssuerFixture(t)
			p := f.provider(t, func(config *OIDCConfig) {
				if mode == "explicit username missing" {
					config.UsernameClaim = "custom_username"
				}
				if mode == "default roles only" || mode == "malformed groups with defaults" {
					config.DefaultRoles = []adminauth.Role{adminauth.RoleViewer}
				}
			})
			values := f.start(t, p)
			f.mu.Lock()
			switch mode {
			case "subject username fallback":
				f.omitClaims = []string{"preferred_username"}
			case "default roles only", "missing groups without defaults":
				f.omitClaims = []string{"groups"}
			case "malformed groups with defaults":
				f.claims["groups"] = "operators"
			}
			f.mu.Unlock()
			identity, _, _, err := p.Complete(t.Context(), values.Get("state"), "valid-code", "127.0.0.1")
			switch mode {
			case "subject username fallback":
				if err != nil || identity.Username != "subject-123" {
					t.Fatal("default username fallback failed", identity, err)
				}
			case "default roles only":
				if err != nil || len(identity.Roles) != 1 || identity.Roles[0] != adminauth.RoleViewer {
					t.Fatal("configured default roles failed", identity, err)
				}
			case "missing groups without defaults":
				if !errors.Is(err, adminauth.ErrForbidden) {
					t.Fatal("missing roles permitted", err)
				}
			default:
				if !errors.Is(err, adminauth.ErrUnauthorized) {
					t.Fatal("malformed or missing explicit claim permitted", err)
				}
			}
		})
	}
}

func TestOIDCSecretRotationInvalidatesOutstandingFlow(t *testing.T) {
	f := newOIDCIssuerFixture(t)
	config := f.config(t)
	resolver, err := NewSecretResolver(nil)
	if err != nil {
		t.Fatal(err)
	}
	flows := adminauth.NewAuthFlowStore()
	p, err := NewOIDCProvider(t.Context(), "fixture", config, resolver, f.server.Client(), flows, 7)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.client.CloseIdleConnections)
	values := f.start(t, p)
	t.Setenv("ROUTENEST_TEST_OIDC_SECRET", "rotated-fixture-secret")
	rotated, err := NewOIDCProvider(t.Context(), "fixture", config, resolver, f.server.Client(), flows, 7)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rotated.client.CloseIdleConnections)
	if rotated.Fingerprint() == p.Fingerprint() {
		t.Fatal("rotation retained fingerprint")
	}
	if _, _, _, err := rotated.Complete(t.Context(), values.Get("state"), "valid-code", "127.0.0.1"); !errors.Is(err, adminauth.ErrUnauthorized) {
		t.Fatal("rotated provider accepted outstanding flow", err)
	}
	if _, _, _, err := p.Complete(t.Context(), values.Get("state"), "valid-code", "127.0.0.1"); !errors.Is(err, adminauth.ErrReplay) {
		t.Fatal("rotation rejection left flow reusable", err)
	}
	f.mu.Lock()
	requests := f.tokenRequests
	f.mu.Unlock()
	if requests != 0 {
		t.Fatal("rotated flow reached token exchange")
	}
}

func TestOIDCClientSecretPost(t *testing.T) {
	f := newOIDCIssuerFixture(t)
	f.mu.Lock()
	f.metadata["token_endpoint_auth_methods_supported"] = []string{"client_secret_post"}
	f.mu.Unlock()
	p := f.provider(t, nil)
	values := f.start(t, p)
	if p.oauth.Endpoint.AuthStyle != oauth2.AuthStyleInParams {
		t.Fatal("declared token authentication was ignored")
	}
	if _, _, _, err := p.Complete(t.Context(), values.Get("state"), "valid-code", "127.0.0.1"); err != nil {
		t.Fatal("client_secret_post exchange failed", err)
	}
}

type oidcDeadlineTransport struct{}

func (oidcDeadlineTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	<-request.Context().Done()
	return nil, request.Context().Err()
}

func TestOIDCDiscoveryDeadline(t *testing.T) {
	t.Setenv("ROUTENEST_TEST_OIDC_SECRET", "fixture-client-secret")
	config := OIDCConfig{
		Issuer: "https://issuer.example", ClientID: "fixture-client", ClientSecret: SecretRef{Kind: "env", Name: "ROUTENEST_TEST_OIDC_SECRET"}, RedirectURL: "https://routenest.example/callback",
		DefaultRoles: []adminauth.Role{adminauth.RoleViewer},
	}
	resolver, err := NewSecretResolver(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: oidcDeadlineTransport{}, Timeout: 25 * time.Millisecond}
	start := time.Now()
	if _, err := NewOIDCProvider(t.Context(), "fixture", config, resolver, client, adminauth.NewAuthFlowStore(), 7); err == nil {
		t.Fatal("unresponsive discovery succeeded")
	}
	if time.Since(start) > time.Second {
		t.Fatal("discovery exceeded configured deadline")
	}
}
