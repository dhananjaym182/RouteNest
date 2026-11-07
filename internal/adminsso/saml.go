package adminsso

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/beevik/etree"
	"github.com/crewjam/saml"
	xrv "github.com/mattermost/xml-roundtrip-validator"
	dsig "github.com/russellhaering/goxmldsig"

	"routenest/internal/adminauth"
)

const (
	samlMetadataLimit = 1 << 20
	samlFormLimit     = 256 << 10
	samlXMLLimit      = 128 << 10
	samlClockSkew     = 30 * time.Second
	samlFlowLifetime  = 5 * time.Minute
	samlReplayLimit   = 4096
	samlProtocolNS    = "urn:oasis:names:tc:SAML:2.0:protocol"
	samlAssertionNS   = "urn:oasis:names:tc:SAML:2.0:assertion"
	samlMetadataNS    = "urn:oasis:names:tc:SAML:2.0:metadata"
	samlBearer        = "urn:oasis:names:tc:SAML:2.0:cm:bearer"
)

// Protocol failures deliberately discard crewjam's InvalidResponseError: that
// error contains the original assertion even though its Error method redacts it.
var errSAMLResponse = errors.New("SAML authentication failed")

// SAMLProvider retains public metadata and optional signing material only in
// memory. Its replay cache contains hashes of IDs, never assertions or tokens.
type SAMLProvider struct {
	id          string
	revision    uint64
	fingerprint string
	config      SAMLConfig
	sp          *saml.ServiceProvider
	flows       *adminauth.AuthFlowStore
	expiresAt   time.Time
	now         func() time.Time
	replay      *SAMLReplayStore
}

// SAMLReplayStore survives metadata/signing-material refreshes. A Manager owns
// one bounded store for its lifetime; entries are scoped to provider and issuer.
type SAMLReplayStore struct {
	mu      sync.Mutex
	entries map[[32]byte]time.Time
}

func NewSAMLReplayStore() *SAMLReplayStore {
	return &SAMLReplayStore{entries: make(map[[32]byte]time.Time)}
}

func (*SAMLProvider) String() string { return "[redacted SAML provider]" }

func NewSAMLProvider(ctx context.Context, id string, revision uint64, config SAMLConfig, resolver *SecretResolver, client *http.Client, flows *adminauth.AuthFlowStore, replayStores ...*SAMLReplayStore) (*SAMLProvider, error) {
	if !providerIDPattern.MatchString(id) || revision == 0 || flows == nil {
		return nil, fmt.Errorf("invalid SAML provider binding")
	}
	if len(id) > 36 {
		return nil, fmt.Errorf("SAML provider ID exceeds the 80-byte RelayState limit")
	}
	replay := NewSAMLReplayStore()
	if len(replayStores) > 1 || len(replayStores) == 1 && replayStores[0] == nil {
		return nil, fmt.Errorf("invalid SAML replay store")
	}
	if len(replayStores) == 1 {
		replay = replayStores[0]
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	acs, _ := url.Parse(config.ACSURL)
	endpoint, _ := url.Parse(config.MetadataEndpoint)
	if !samlServiceURL(acs) || !samlServiceURL(endpoint) {
		return nil, fmt.Errorf("SAML service URLs require HTTPS or direct loopback HTTP")
	}
	metadataURL, _ := url.Parse(config.MetadataURL)
	metadata, expires, err := loadSAMLMetadata(ctx, client, metadataURL)
	if err != nil {
		return nil, err
	}
	sp := &saml.ServiceProvider{EntityID: config.EntityID, MetadataURL: *endpoint, AcsURL: *acs, IDPMetadata: metadata, AuthnNameIDFormat: saml.UnspecifiedNameIDFormat, SignatureVerifier: samlSecureVerifier{}}
	publicMetadata, err := xml.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("invalid SAML metadata")
	}
	fingerprintInput := publicMetadata
	if config.Certificate != nil {
		if resolver == nil {
			return nil, fmt.Errorf("SAML secret resolver is unavailable")
		}
		certificate, certificateFingerprint, err := resolver.Resolve(*config.Certificate)
		if err != nil {
			return nil, fmt.Errorf("SAML certificate is unavailable")
		}
		privateKey, keyFingerprint, err := resolver.Resolve(*config.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("SAML private key is unavailable")
		}
		pair, err := tls.X509KeyPair(certificate, privateKey)
		fingerprintInput = append(fingerprintInput, []byte("\x00"+certificateFingerprint+"\x00"+keyFingerprint)...)
		clear(certificate)
		clear(privateKey)
		if err != nil || len(pair.Certificate) == 0 {
			return nil, fmt.Errorf("invalid SAML signing key pair")
		}
		cert, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil || !samlCertificateValid(cert, time.Now()) {
			return nil, fmt.Errorf("invalid or expired SAML signing certificate")
		}
		switch key := pair.PrivateKey.(type) {
		case *rsa.PrivateKey:
			sp.Key = key
			sp.SignatureMethod = dsig.RSASHA256SignatureMethod
		case *ecdsa.PrivateKey:
			sp.Key = key
			sp.SignatureMethod = dsig.ECDSASHA256SignatureMethod
		default:
			return nil, fmt.Errorf("unsupported SAML signing key")
		}
		sp.Certificate = cert
		if cert.NotAfter.Before(expires) {
			expires = cert.NotAfter
		}
		for _, raw := range pair.Certificate[1:] {
			cert, err := x509.ParseCertificate(raw)
			if err != nil {
				return nil, fmt.Errorf("invalid SAML certificate chain")
			}
			sp.Intermediates = append(sp.Intermediates, cert)
		}
	}
	for _, descriptor := range metadata.IDPSSODescriptors {
		if descriptor.WantAuthnRequestsSigned != nil && *descriptor.WantAuthnRequestsSigned && sp.Key == nil {
			return nil, fmt.Errorf("IdP requires a configured SAML signing key")
		}
	}
	config.DefaultRoles = append([]adminauth.Role(nil), config.DefaultRoles...)
	mapping := make(map[string][]adminauth.Role, len(config.GroupRoleMap))
	for group, roles := range config.GroupRoleMap {
		mapping[group] = append([]adminauth.Role(nil), roles...)
	}
	config.GroupRoleMap = mapping
	fingerprint := sha256.Sum256(fingerprintInput)
	return &SAMLProvider{id: id, revision: revision, fingerprint: fmt.Sprintf("%x", fingerprint[:16]), config: config, sp: sp, flows: flows, expiresAt: expires, now: time.Now, replay: replay}, nil
}

func (p *SAMLProvider) Fingerprint() string { return p.fingerprint }

func (p *SAMLProvider) FormActionOrigins() []string {
	location, _ := url.Parse(p.sp.GetSSOBindingLocation(saml.HTTPRedirectBinding))
	return []string{location.Scheme + "://" + location.Host}
}

func samlServiceURL(u *url.URL) bool {
	if u.Scheme == "https" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "http" && ip != nil && ip.IsLoopback()
}

func loadSAMLMetadata(ctx context.Context, client *http.Client, location *url.URL) (*saml.EntityDescriptor, time.Time, error) {
	if client == nil {
		client = http.DefaultClient
	}
	bounded := *client
	bounded.Timeout = 10 * time.Second
	bounded.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || req.URL.Scheme != "https" || req.URL.Host != location.Host || req.URL.User != nil {
			return fmt.Errorf("SAML metadata redirect rejected")
		}
		if client.CheckRedirect != nil {
			return client.CheckRedirect(req, via)
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location.String(), nil)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("invalid SAML metadata URL")
	}
	req.Header.Set("Accept", "application/samlmetadata+xml, application/xml, text/xml")
	response, err := bounded.Do(req)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("SAML metadata retrieval failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > samlMetadataLimit {
		return nil, time.Time{}, fmt.Errorf("SAML metadata response rejected")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, samlMetadataLimit+1))
	if err != nil || len(raw) > samlMetadataLimit {
		return nil, time.Time{}, fmt.Errorf("SAML metadata exceeds limit or is unreadable")
	}
	if err := boundedSAMLXML(raw); err != nil {
		return nil, time.Time{}, fmt.Errorf("invalid SAML metadata XML")
	}
	if err := xrv.Validate(bytes.NewReader(raw)); err != nil {
		return nil, time.Time{}, fmt.Errorf("invalid SAML metadata XML")
	}
	var root struct{ XMLName xml.Name }
	if err := xml.Unmarshal(raw, &root); err != nil || root.XMLName.Space != samlMetadataNS {
		return nil, time.Time{}, fmt.Errorf("invalid SAML metadata root")
	}
	now := time.Now()
	expires := now.Add(24 * time.Hour)
	var metadata *saml.EntityDescriptor
	switch root.XMLName.Local {
	case "EntityDescriptor":
		metadata = &saml.EntityDescriptor{}
		if err := xml.Unmarshal(raw, metadata); err != nil {
			return nil, time.Time{}, fmt.Errorf("invalid SAML metadata")
		}
	case "EntitiesDescriptor":
		var entities saml.EntitiesDescriptor
		if err := xml.Unmarshal(raw, &entities); err != nil {
			return nil, time.Time{}, fmt.Errorf("invalid SAML metadata")
		}
		if entities.ValidUntil != nil && entities.ValidUntil.Before(expires) {
			expires = *entities.ValidUntil
		}
		for i := range entities.EntityDescriptors {
			if len(entities.EntityDescriptors[i].IDPSSODescriptors) == 0 {
				continue
			}
			if metadata != nil {
				return nil, time.Time{}, fmt.Errorf("ambiguous SAML IdP metadata")
			}
			metadata = &entities.EntityDescriptors[i]
		}
	default:
		return nil, time.Time{}, fmt.Errorf("invalid SAML metadata root")
	}
	if metadata == nil || !samlClaim(metadata.EntityID) || len(metadata.IDPSSODescriptors) != 1 {
		return nil, time.Time{}, fmt.Errorf("SAML metadata requires one IdP")
	}
	if !metadata.ValidUntil.IsZero() && metadata.ValidUntil.Before(expires) {
		expires = metadata.ValidUntil
	}
	descriptor := &metadata.IDPSSODescriptors[0]
	protocolSupported := false
	for _, protocol := range strings.Fields(descriptor.ProtocolSupportEnumeration) {
		if protocol == samlProtocolNS {
			protocolSupported = true
		}
	}
	if !protocolSupported {
		return nil, time.Time{}, fmt.Errorf("IdP metadata does not support SAML 2.0")
	}
	if descriptor.ValidUntil != nil && descriptor.ValidUntil.Before(expires) {
		expires = *descriptor.ValidUntil
	}
	if !expires.After(now) {
		return nil, time.Time{}, fmt.Errorf("SAML metadata has expired")
	}
	var keys []saml.KeyDescriptor
	for _, key := range descriptor.KeyDescriptors {
		if key.Use != "" && key.Use != "signing" {
			continue
		}
		var certs []saml.X509Certificate
		for _, value := range key.KeyInfo.X509Data.X509Certificates {
			raw, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(value.Data), ""))
			if err != nil {
				return nil, time.Time{}, fmt.Errorf("invalid SAML IdP signing certificate")
			}
			certificate, err := x509.ParseCertificate(raw)
			if err != nil {
				return nil, time.Time{}, fmt.Errorf("invalid SAML IdP signing certificate")
			}
			if samlCertificateValid(certificate, now) {
				certs = append(certs, value)
			}
		}
		if len(certs) > 0 {
			key.KeyInfo.X509Data.X509Certificates = certs
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return nil, time.Time{}, fmt.Errorf("SAML metadata has no valid signing certificate")
	}
	descriptor.KeyDescriptors = keys
	redirect := ""
	for _, service := range descriptor.SingleSignOnServices {
		if service.Binding != saml.HTTPRedirectBinding {
			continue
		}
		u, err := url.Parse(service.Location)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
			return nil, time.Time{}, fmt.Errorf("SAML IdP redirect endpoint requires HTTPS")
		}
		if redirect != "" {
			return nil, time.Time{}, fmt.Errorf("ambiguous SAML IdP redirect endpoint")
		}
		redirect = service.Location
	}
	if redirect == "" {
		return nil, time.Time{}, fmt.Errorf("SAML IdP has no HTTPS redirect binding")
	}
	return metadata, expires, nil
}

func samlCertificateValid(certificate *x509.Certificate, now time.Time) bool {
	if certificate == nil || now.Before(certificate.NotBefore) || !now.Before(certificate.NotAfter) || certificate.KeyUsage != 0 && certificate.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return false
	}
	switch key := certificate.PublicKey.(type) {
	case *rsa.PublicKey:
		return key.N.BitLen() >= 2048
	case *ecdsa.PublicKey:
		return key.Curve.Params().BitSize >= 256
	default:
		return false
	}
}

func (p *SAMLProvider) Metadata() ([]byte, error) {
	metadata := p.sp.Metadata()
	// Only the POST ACS is implemented. Do not advertise artifact or logout.
	metadata.SPSSODescriptors[0].AssertionConsumerServices = metadata.SPSSODescriptors[0].AssertionConsumerServices[:1]
	return xml.Marshal(metadata)
}

func (p *SAMLProvider) Start(source, returnLocation string) (string, error) {
	if !p.expiresAt.After(p.now()) {
		return "", fmt.Errorf("SAML metadata has expired")
	}
	request, err := p.sp.MakeAuthenticationRequest(p.sp.GetSSOBindingLocation(saml.HTTPRedirectBinding), saml.HTTPRedirectBinding, saml.HTTPPostBinding)
	if err != nil {
		return "", fmt.Errorf("SAML authentication request failed")
	}
	handle, err := p.flows.Create(adminauth.AuthFlow{Provider: p.id, ProviderRevision: p.revision, ProviderFingerprint: p.fingerprint, Type: adminauth.MethodSAML, Source: source, SAMLRequestID: request.ID, ExpectedIssuer: p.sp.IDPMetadata.EntityID, Callback: p.config.ACSURL, ReturnLocation: returnLocation, ExpiresAt: p.now().Add(samlFlowLifetime)})
	if err != nil {
		return "", err
	}
	state, err := encodeFlowState(p.id, handle)
	if err != nil {
		_, _ = p.flows.Consume(handle, p.id, adminauth.MethodSAML)
		return "", err
	}
	redirect, err := request.Redirect(state, p.sp)
	if err != nil {
		_, _ = p.flows.Consume(handle, p.id, adminauth.MethodSAML)
		return "", fmt.Errorf("SAML authentication request failed")
	}
	return redirect.String(), nil
}

// Complete consumes the server-side transaction before protocol verification.
// Even an invalid response burns its handle; callbacks can never be retried.
func (p *SAMLProvider) Complete(ctx context.Context, request *http.Request) (FederatedIdentity, string, uint64, error) {
	callback, err := p.readCallback(ctx, request)
	if err != nil {
		return FederatedIdentity{}, "", 0, err
	}
	provider, handle, err := ParseFlowState(callback.relayState)
	if err != nil || provider != p.id {
		return FederatedIdentity{}, "", 0, adminauth.ErrReplay
	}
	flow, err := p.flows.Consume(handle, p.id, adminauth.MethodSAML)
	if err != nil {
		return FederatedIdentity{}, "", 0, err
	}
	return p.completeCallback(callback, flow)
}

// completeConsumed validates a callback whose RelayState transaction was
// already atomically consumed by the manager before metadata network I/O.
func (p *SAMLProvider) completeConsumed(ctx context.Context, request *http.Request, relayState string, flow adminauth.AuthFlow) (FederatedIdentity, string, uint64, error) {
	callback, err := p.readCallback(ctx, request)
	if err != nil {
		return FederatedIdentity{}, "", 0, err
	}
	if callback.relayState != relayState {
		return FederatedIdentity{}, "", 0, adminauth.ErrReplay
	}
	return p.completeCallback(callback, flow)
}

type parsedSAMLCallback struct {
	relayState string
	response   string
}

func (p *SAMLProvider) readCallback(ctx context.Context, request *http.Request) (parsedSAMLCallback, error) {
	if err := ctx.Err(); err != nil {
		return parsedSAMLCallback{}, err
	}
	if request == nil || request.URL == nil || request.Body == nil || request.Method != http.MethodPost || request.URL.RawQuery != "" || request.URL.Fragment != "" || request.Host != p.sp.AcsURL.Host || request.URL.EscapedPath() != p.sp.AcsURL.EscapedPath() || request.URL.IsAbs() && (request.URL.Scheme != p.sp.AcsURL.Scheme || request.URL.Host != p.sp.AcsURL.Host) {
		return parsedSAMLCallback{}, errSAMLResponse
	}
	media, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || media != "application/x-www-form-urlencoded" || request.ContentLength > samlFormLimit {
		return parsedSAMLCallback{}, errSAMLResponse
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, samlFormLimit+1))
	if err != nil || len(body) > samlFormLimit {
		return parsedSAMLCallback{}, errSAMLResponse
	}
	form, err := url.ParseQuery(string(body))
	if err != nil || len(form["RelayState"]) != 1 || len(form["SAMLResponse"]) != 1 || len(form) != 2 {
		return parsedSAMLCallback{}, errSAMLResponse
	}
	return parsedSAMLCallback{relayState: form.Get("RelayState"), response: form.Get("SAMLResponse")}, nil
}

func (p *SAMLProvider) completeCallback(callback parsedSAMLCallback, flow adminauth.AuthFlow) (FederatedIdentity, string, uint64, error) {
	fail := func(err error) (FederatedIdentity, string, uint64, error) { return FederatedIdentity{}, "", 0, err }
	if flow.ProviderRevision != p.revision || flow.ProviderFingerprint != p.fingerprint || flow.ExpectedIssuer != p.sp.IDPMetadata.EntityID || flow.Callback != p.config.ACSURL || flow.SAMLRequestID == "" || !p.expiresAt.After(p.now()) {
		return fail(errSAMLResponse)
	}
	raw, err := base64.StdEncoding.DecodeString(callback.response)
	if err != nil || len(raw) == 0 || len(raw) > samlXMLLimit || boundedSAMLXML(raw) != nil {
		return fail(errSAMLResponse)
	}
	var envelope saml.Response
	if err := xml.Unmarshal(raw, &envelope); err != nil || !samlClaim(envelope.ID) || envelope.Version != "2.0" || envelope.InResponseTo != flow.SAMLRequestID || envelope.Destination != flow.Callback || envelope.Issuer == nil || envelope.Issuer.Value != flow.ExpectedIssuer || !samlIssueValid(envelope.IssueInstant, flow.CreatedAt, p.now()) {
		return fail(errSAMLResponse)
	}
	if err := samlResponseShape(raw); err != nil {
		return fail(errSAMLResponse)
	}
	assertion, err := p.sp.ParseXMLResponse(raw, []string{flow.SAMLRequestID}, p.sp.AcsURL)
	if err != nil {
		return fail(errSAMLResponse)
	}
	if err := p.validateAssertion(assertion, flow); err != nil {
		return fail(errSAMLResponse)
	}
	identity, err := p.identity(assertion)
	if err != nil {
		return fail(err)
	}
	if err := p.consumeAssertion(envelope.ID, assertion); err != nil {
		return fail(err)
	}
	return identity, flow.ReturnLocation, flow.ProviderRevision, nil
}

func samlIssueValid(issued, created, now time.Time) bool {
	return !issued.IsZero() && !issued.Before(created.Add(-samlClockSkew)) && !issued.After(now.Add(samlClockSkew)) && !issued.Add(90*time.Second).Before(now)
}

func (p *SAMLProvider) validateAssertion(assertion *saml.Assertion, flow adminauth.AuthFlow) error {
	now := p.now()
	if assertion == nil || !samlClaim(assertion.ID) || assertion.Version != "2.0" || assertion.Issuer.Value != flow.ExpectedIssuer || !samlIssueValid(assertion.IssueInstant, flow.CreatedAt, now) || assertion.Subject == nil || assertion.Subject.NameID == nil || assertion.Conditions == nil {
		return errSAMLResponse
	}
	name := assertion.Subject.NameID
	if !samlClaim(name.Value) || name.NameQualifier != "" && name.NameQualifier != flow.ExpectedIssuer || name.SPNameQualifier != "" && name.SPNameQualifier != p.config.EntityID {
		return errSAMLResponse
	}
	conditions := assertion.Conditions
	if conditions.NotBefore.IsZero() || conditions.NotOnOrAfter.IsZero() || !conditions.NotOnOrAfter.After(conditions.NotBefore) || conditions.NotBefore.After(now.Add(samlClockSkew)) || !now.Before(conditions.NotOnOrAfter.Add(samlClockSkew)) || len(conditions.AudienceRestrictions) == 0 {
		return errSAMLResponse
	}
	for _, restriction := range conditions.AudienceRestrictions {
		if restriction.Audience.Value != p.config.EntityID {
			return errSAMLResponse
		}
	}
	if len(assertion.Subject.SubjectConfirmations) == 0 {
		return errSAMLResponse
	}
	for _, confirmation := range assertion.Subject.SubjectConfirmations {
		data := confirmation.SubjectConfirmationData
		if confirmation.Method != samlBearer || data == nil || data.InResponseTo != flow.SAMLRequestID || data.Recipient != flow.Callback || data.NotOnOrAfter.IsZero() || !now.Before(data.NotOnOrAfter.Add(samlClockSkew)) || !data.NotBefore.IsZero() && (data.NotBefore.After(now.Add(samlClockSkew)) || !data.NotOnOrAfter.After(data.NotBefore)) {
			return errSAMLResponse
		}
	}
	if len(assertion.AuthnStatements) != 1 {
		return errSAMLResponse
	}
	statement := assertion.AuthnStatements[0]
	if statement.AuthnInstant.IsZero() || statement.AuthnInstant.After(now.Add(samlClockSkew)) || statement.SessionNotOnOrAfter != nil && !now.Before(statement.SessionNotOnOrAfter.Add(samlClockSkew)) {
		return errSAMLResponse
	}
	return nil
}

func (p *SAMLProvider) identity(assertion *saml.Assertion) (FederatedIdentity, error) {
	username := assertion.Subject.NameID.Value
	if p.config.UsernameAttribute != "" {
		values := samlAttributeValues(assertion, p.config.UsernameAttribute)
		if len(values) != 1 || !samlClaim(values[0]) {
			return FederatedIdentity{}, errSAMLResponse
		}
		username = values[0]
	}
	groups := samlAttributeValues(assertion, p.config.GroupsAttribute)
	if len(groups) > 256 {
		return FederatedIdentity{}, errSAMLResponse
	}
	for _, group := range groups {
		if !samlClaim(group) {
			return FederatedIdentity{}, errSAMLResponse
		}
	}
	roles := mapRoles(p.config.DefaultRoles, p.config.GroupRoleMap, groups)
	username = adminauth.NormalizeUsername(username)
	if !samlClaim(username) || len(username) > 128 {
		return FederatedIdentity{}, errSAMLResponse
	}
	if len(roles) == 0 {
		return FederatedIdentity{}, adminauth.ErrForbidden
	}
	return FederatedIdentity{Provider: p.id, Subject: assertion.Subject.NameID.Value, Username: username, Roles: roles, Method: adminauth.MethodSAML}, nil
}

func samlAttributeValues(assertion *saml.Assertion, name string) []string {
	if name == "" {
		return nil
	}
	var values []string
	for _, statement := range assertion.AttributeStatements {
		for _, attribute := range statement.Attributes {
			// Match the exact configured Name only; FriendlyName is not unique.
			if attribute.Name == name {
				for _, value := range attribute.Values {
					values = append(values, value.Value)
				}
			}
		}
	}
	return values
}

func samlClaim(value string) bool {
	return len(value) > 0 && len(value) <= 512 && strings.TrimSpace(value) != "" && !strings.ContainsFunc(value, unicode.IsControl)
}

func (p *SAMLProvider) consumeAssertion(responseID string, assertion *saml.Assertion) error {
	p.replay.mu.Lock()
	defer p.replay.mu.Unlock()
	now := p.now()
	if p.replay.entries == nil {
		p.replay.entries = make(map[[32]byte]time.Time)
	}
	for hash, expires := range p.replay.entries {
		if !expires.After(now) {
			delete(p.replay.entries, hash)
		}
	}
	prefix := p.id + "\x00" + p.sp.IDPMetadata.EntityID
	responseHash := sha256.Sum256([]byte(prefix + "\x00response\x00" + responseID))
	assertionHash := sha256.Sum256([]byte(prefix + "\x00assertion\x00" + assertion.ID))
	if _, exists := p.replay.entries[responseHash]; exists {
		return adminauth.ErrReplay
	}
	if _, exists := p.replay.entries[assertionHash]; exists {
		return adminauth.ErrReplay
	}
	if len(p.replay.entries)+2 > samlReplayLimit {
		return adminauth.ErrRateLimited
	}
	// IDs remain fenced longer than the maximum accepted issue age. Retaining
	// through the condition expiry also enforces OneTimeUse assertions.
	expires := assertion.Conditions.NotOnOrAfter.Add(samlClockSkew)
	if expires.Before(now.Add(2 * time.Minute)) {
		expires = now.Add(2 * time.Minute)
	}
	if expires.After(now.Add(10 * time.Minute)) {
		expires = now.Add(10 * time.Minute)
	}
	p.replay.entries[responseHash], p.replay.entries[assertionHash] = expires, expires
	return nil
}

// Bound token count and nesting before XML libraries allocate trees or perform
// cryptography. Directives (including DTDs) are forbidden.
func boundedSAMLXML(raw []byte) error {
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	depth, tokens := 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			if depth != 0 {
				return errSAMLResponse
			}
			return nil
		}
		if err != nil {
			return errSAMLResponse
		}
		tokens++
		if tokens > 16000 {
			return errSAMLResponse
		}
		switch token.(type) {
		case xml.StartElement:
			depth++
			if depth > 64 {
				return errSAMLResponse
			}
		case xml.EndElement:
			depth--
		case xml.Directive:
			return errSAMLResponse
		}
	}
}

func samlResponseShape(raw []byte) error {
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	depth, assertions, signatures := 0, 0, 0
	signatureDepth := 0
	ids := map[string]bool{}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errSAMLResponse
		}
		switch element := token.(type) {
		case xml.StartElement:
			depth++
			if depth == 1 && (element.Name.Space != samlProtocolNS || element.Name.Local != "Response") {
				return errSAMLResponse
			}
			for _, attribute := range element.Attr {
				if attribute.Name.Local == "ID" {
					if !samlClaim(attribute.Value) || ids[attribute.Value] {
						return errSAMLResponse
					}
					ids[attribute.Value] = true
				}
			}
			if element.Name.Space == samlAssertionNS && (element.Name.Local == "Assertion" || element.Name.Local == "EncryptedAssertion") {
				if depth != 2 {
					return errSAMLResponse
				}
				assertions++
			}
			if element.Name.Space == "http://www.w3.org/2000/09/xmldsig#" {
				switch element.Name.Local {
				case "Signature":
					if signatureDepth != 0 {
						return errSAMLResponse
					}
					signatureDepth = depth
					signatures++
				case "SignatureMethod", "DigestMethod":
					if signatureDepth == 0 {
						continue
					}
					algorithm := ""
					for _, attribute := range element.Attr {
						if attribute.Name.Local == "Algorithm" {
							algorithm = attribute.Value
						}
					}
					if !secureSAMLAlgorithm(element.Name.Local, algorithm) {
						return errSAMLResponse
					}
				}
			}
		case xml.EndElement:
			if depth == signatureDepth {
				signatureDepth = 0
			}
			depth--
		}
	}
	if assertions != 1 || signatures > 2 {
		return errSAMLResponse
	}
	return nil
}

func secureSAMLAlgorithm(kind, algorithm string) bool {
	if kind == "SignatureMethod" {
		switch algorithm {
		case dsig.RSASHA256SignatureMethod, dsig.RSASHA384SignatureMethod, dsig.RSASHA512SignatureMethod, dsig.ECDSASHA256SignatureMethod, dsig.ECDSASHA384SignatureMethod, dsig.ECDSASHA512SignatureMethod:
			return true
		}
	} else {
		switch algorithm {
		case "http://www.w3.org/2001/04/xmlenc#sha256", "http://www.w3.org/2001/04/xmldsig-more#sha384", "http://www.w3.org/2001/04/xmlenc#sha512":
			return true
		}
	}
	return false
}

// Also enforce the algorithm allowlist when crewjam decrypts an assertion;
// its embedded signature is not visible in the outer response preflight.
type samlSecureVerifier struct{}

func (samlSecureVerifier) VerifySignature(validation *dsig.ValidationContext, element *etree.Element) error {
	var inspect func(*etree.Element, bool, int) error
	inspect = func(element *etree.Element, inSignature bool, depth int) error {
		if depth > 64 {
			return errSAMLResponse
		}
		if element.NamespaceURI() == "http://www.w3.org/2000/09/xmldsig#" && element.Tag == "Signature" {
			inSignature = true
		}
		if inSignature && element.NamespaceURI() == "http://www.w3.org/2000/09/xmldsig#" && (element.Tag == "SignatureMethod" || element.Tag == "DigestMethod") && !secureSAMLAlgorithm(element.Tag, element.SelectAttrValue("Algorithm", "")) {
			return errSAMLResponse
		}
		for _, child := range element.ChildElements() {
			if err := inspect(child, inSignature, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := inspect(element, false, 0); err != nil {
		return err
	}
	_, err := validation.Validate(element)
	return err
}
