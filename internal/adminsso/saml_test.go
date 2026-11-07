package adminsso

import (
	"bytes"
	"compress/flate"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beevik/etree"
	"github.com/crewjam/saml"
	dsig "github.com/russellhaering/goxmldsig"

	"routenest/internal/adminauth"
)

type samlFixture struct {
	provider    *SAMLProvider
	key         *rsa.PrivateKey
	certificate *x509.Certificate
	metadata    saml.EntityDescriptor
	server      *httptest.Server
}

func newSAMLFixture(t *testing.T) *samlFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test IdP"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	f := &samlFixture{key: key, certificate: certificate}
	f.metadata = saml.EntityDescriptor{EntityID: "https://idp.example.test/metadata", ValidUntil: now.Add(time.Hour), IDPSSODescriptors: []saml.IDPSSODescriptor{{SSODescriptor: saml.SSODescriptor{RoleDescriptor: saml.RoleDescriptor{ProtocolSupportEnumeration: samlProtocolNS, KeyDescriptors: []saml.KeyDescriptor{{Use: "signing", KeyInfo: saml.KeyInfo{X509Data: saml.X509Data{X509Certificates: []saml.X509Certificate{{Data: base64.StdEncoding.EncodeToString(der)}}}}}}}}, SingleSignOnServices: []saml.Endpoint{{Binding: saml.HTTPRedirectBinding, Location: "https://idp.example.test/login"}}}}}
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := xml.Marshal(f.metadata)
		w.Header().Set("Content-Type", "application/samlmetadata+xml")
		_, _ = w.Write(raw)
	}))
	t.Cleanup(f.server.Close)
	config := SAMLConfig{MetadataURL: f.server.URL, EntityID: "https://admin.example.test/saml/entity", MetadataEndpoint: "https://admin.example.test/saml/metadata", ACSURL: "https://admin.example.test/saml/acs", UsernameAttribute: "uid", GroupsAttribute: "groups", DefaultRoles: []adminauth.Role{adminauth.RoleViewer}, GroupRoleMap: map[string][]adminauth.Role{"operators": {adminauth.RoleProviderAdmin}}}
	p, err := NewSAMLProvider(context.Background(), "test-idp", 7, config, nil, f.server.Client(), adminauth.NewAuthFlowStore())
	if err != nil {
		t.Fatal(err)
	}
	f.provider = p
	return f
}

func (f *samlFixture) start(t *testing.T, source string) (string, string) {
	t.Helper()
	redirect, err := f.provider.Start(source, "/admin/routes")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(redirect)
	if err != nil || u.Scheme != "https" || u.Host != "idp.example.test" {
		t.Fatal("unexpected redirect")
	}
	compressed, err := base64.StdEncoding.DecodeString(u.Query().Get("SAMLRequest"))
	if err != nil {
		t.Fatal(err)
	}
	reader := flate.NewReader(bytes.NewReader(compressed))
	defer reader.Close()
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	var request saml.AuthnRequest
	if err := xml.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	if request.AssertionConsumerServiceURL != f.provider.config.ACSURL || request.ProtocolBinding != saml.HTTPPostBinding || request.Issuer.Value != f.provider.config.EntityID {
		t.Fatal("request binding mismatch")
	}
	return request.ID, u.Query().Get("RelayState")
}

func (f *samlFixture) response(requestID, unique string) *saml.Response {
	now := time.Now().UTC()
	return &saml.Response{ID: "response-" + unique, InResponseTo: requestID, Version: "2.0", IssueInstant: now, Destination: f.provider.config.ACSURL, Issuer: &saml.Issuer{Value: f.metadata.EntityID}, Status: saml.Status{StatusCode: saml.StatusCode{Value: saml.StatusSuccess}}, Assertion: &saml.Assertion{ID: "assertion-" + unique, Version: "2.0", IssueInstant: now, Issuer: saml.Issuer{Value: f.metadata.EntityID}, Subject: &saml.Subject{NameID: &saml.NameID{Value: "stable-subject", NameQualifier: f.metadata.EntityID, SPNameQualifier: f.provider.config.EntityID}, SubjectConfirmations: []saml.SubjectConfirmation{{Method: samlBearer, SubjectConfirmationData: &saml.SubjectConfirmationData{InResponseTo: requestID, Recipient: f.provider.config.ACSURL, NotOnOrAfter: now.Add(2 * time.Minute)}}}}, Conditions: &saml.Conditions{NotBefore: now.Add(-10 * time.Second), NotOnOrAfter: now.Add(2 * time.Minute), AudienceRestrictions: []saml.AudienceRestriction{{Audience: saml.Audience{Value: f.provider.config.EntityID}}}}, AuthnStatements: []saml.AuthnStatement{{AuthnInstant: now, AuthnContext: saml.AuthnContext{AuthnContextClassRef: &saml.AuthnContextClassRef{Value: "urn:oasis:names:tc:SAML:2.0:ac:classes:PasswordProtectedTransport"}}}}, AttributeStatements: []saml.AttributeStatement{{Attributes: []saml.Attribute{{Name: "uid", Values: []saml.AttributeValue{{Type: "xs:string", Value: "Alice"}}}, {Name: "groups", Values: []saml.AttributeValue{{Type: "xs:string", Value: "operators"}, {Type: "xs:string", Value: "security_admin"}}}}}}}}
}

func (f *samlFixture) signingContext(t *testing.T) *dsig.SigningContext {
	t.Helper()
	context, err := dsig.NewSigningContext(f.key, [][]byte{f.certificate.Raw})
	if err != nil {
		t.Fatal(err)
	}
	context.IdAttribute = "ID"
	context.Canonicalizer = dsig.MakeC14N10ExclusiveCanonicalizerWithPrefixList("")
	if err := context.SetSignatureMethod(dsig.RSASHA256SignatureMethod); err != nil {
		t.Fatal(err)
	}
	return context
}

func (f *samlFixture) signed(t *testing.T, response *saml.Response, signResponse bool) []byte {
	t.Helper()
	context := f.signingContext(t)
	root := response.Element()
	var err error
	if signResponse {
		root, err = context.SignEnveloped(root)
	} else {
		for _, child := range root.ChildElements() {
			if child.Tag == "Assertion" {
				signed, signErr := context.SignEnveloped(child)
				if signErr != nil {
					t.Fatal(signErr)
				}
				root.RemoveChild(child)
				root.AddChild(signed)
			}
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	doc := etree.NewDocument()
	doc.SetRoot(root)
	raw, err := doc.WriteToBytes()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (f *samlFixture) signedAssertionTree(t *testing.T, response *saml.Response, mutate func(*etree.Element)) []byte {
	t.Helper()
	context := f.signingContext(t)
	root := response.Element()
	assertion := root.FindElement("./Assertion")
	if assertion == nil {
		t.Fatal("response assertion missing")
	}
	mutate(assertion)
	signed, err := context.SignEnveloped(assertion)
	if err != nil {
		t.Fatal(err)
	}
	root.RemoveChild(assertion)
	root.AddChild(signed)
	doc := etree.NewDocument()
	doc.SetRoot(root)
	raw, err := doc.WriteToBytes()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func samlCallback(state string, raw []byte) *http.Request {
	form := url.Values{"RelayState": {state}, "SAMLResponse": {base64.StdEncoding.EncodeToString(raw)}}
	request := httptest.NewRequest(http.MethodPost, "https://admin.example.test/saml/acs", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return request
}

func TestSAMLVerifiedIdentityAndMetadata(t *testing.T) {
	f := newSAMLFixture(t)
	for i, signResponse := range []bool{false, true} {
		requestID, state := f.start(t, fmt.Sprint(i))
		raw := f.signed(t, f.response(requestID, fmt.Sprint(i)), signResponse)
		identity, location, revision, err := f.provider.Complete(context.Background(), samlCallback(state, raw))
		if err != nil {
			t.Fatalf("signed response=%v: %v", signResponse, err)
		}
		if identity.Provider != "test-idp" || identity.Subject != "stable-subject" || identity.Username != "alice" || identity.Method != adminauth.MethodSAML || location != "/admin/routes" || revision != 7 || !reflect.DeepEqual(identity.Roles, []adminauth.Role{adminauth.RoleProviderAdmin, adminauth.RoleViewer}) {
			t.Fatal("incorrect mapped identity")
		}
		if _, _, _, err := f.provider.Complete(context.Background(), samlCallback(state, raw)); !errors.Is(err, adminauth.ErrReplay) {
			t.Fatal("callback replay accepted")
		}
	}
	raw, err := f.provider.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	var metadata saml.EntityDescriptor
	if err := xml.Unmarshal(raw, &metadata); err != nil {
		t.Fatal(err)
	}
	sp := metadata.SPSSODescriptors[0]
	if metadata.EntityID != f.provider.config.EntityID || len(sp.AssertionConsumerServices) != 1 || sp.AssertionConsumerServices[0].Binding != saml.HTTPPostBinding || !*sp.WantAssertionsSigned || len(sp.SingleLogoutServices) != 0 {
		t.Fatal("incorrect SP metadata")
	}
	if !reflect.DeepEqual(f.provider.FormActionOrigins(), []string{"https://idp.example.test"}) {
		t.Fatal("CSP origin contains unexpected location")
	}
	if _, err := NewSAMLProvider(context.Background(), strings.Repeat("a", 37), 1, f.provider.config, nil, f.server.Client(), adminauth.NewAuthFlowStore()); err == nil {
		t.Fatal("oversized RelayState provider accepted")
	}
}

func TestSAMLRejectsSignedHostileAssertions(t *testing.T) {
	f := newSAMLFixture(t)
	tests := []struct {
		name   string
		mutate func(*saml.Response)
	}{
		{"response destination", func(r *saml.Response) { r.Destination = "https://evil.example/acs" }},
		{"missing destination", func(r *saml.Response) { r.Destination = "" }},
		{"response issuer", func(r *saml.Response) { r.Issuer.Value = "https://evil.example/idp" }},
		{"missing response issuer", func(r *saml.Response) { r.Issuer = nil }},
		{"response correlation", func(r *saml.Response) { r.InResponseTo = "other-request" }},
		{"assertion issuer", func(r *saml.Response) { r.Assertion.Issuer.Value = "https://evil.example/idp" }},
		{"subject correlation", func(r *saml.Response) {
			r.Assertion.Subject.SubjectConfirmations[0].SubjectConfirmationData.InResponseTo = "other-request"
		}},
		{"recipient", func(r *saml.Response) {
			r.Assertion.Subject.SubjectConfirmations[0].SubjectConfirmationData.Recipient = "https://evil.example/acs"
		}},
		{"missing subject", func(r *saml.Response) { r.Assertion.Subject = nil }},
		{"missing NameID", func(r *saml.Response) { r.Assertion.Subject.NameID = nil }},
		{"empty subject", func(r *saml.Response) { r.Assertion.Subject.NameID.Value = "" }},
		{"NameID issuer qualifier", func(r *saml.Response) { r.Assertion.Subject.NameID.NameQualifier = "other-idp" }},
		{"NameID SP qualifier", func(r *saml.Response) { r.Assertion.Subject.NameID.SPNameQualifier = "other-sp" }},
		{"missing confirmation", func(r *saml.Response) { r.Assertion.Subject.SubjectConfirmations = nil }},
		{"nonbearer confirmation", func(r *saml.Response) {
			r.Assertion.Subject.SubjectConfirmations[0].Method = "urn:oasis:names:tc:SAML:2.0:cm:holder-of-key"
		}},
		{"missing conditions", func(r *saml.Response) { r.Assertion.Conditions = nil }},
		{"missing audience", func(r *saml.Response) { r.Assertion.Conditions.AudienceRestrictions = nil }},
		{"wrong audience", func(r *saml.Response) { r.Assertion.Conditions.AudienceRestrictions[0].Audience.Value = "other-sp" }},
		{"additional wrong restriction", func(r *saml.Response) {
			r.Assertion.Conditions.AudienceRestrictions = append(r.Assertion.Conditions.AudienceRestrictions, saml.AudienceRestriction{Audience: saml.Audience{Value: "other-sp"}})
		}},
		{"future assertion", func(r *saml.Response) { r.Assertion.IssueInstant = time.Now().Add(time.Minute) }},
		{"future response", func(r *saml.Response) { r.IssueInstant = time.Now().Add(time.Minute) }},
		{"expired assertion", func(r *saml.Response) { r.Assertion.Conditions.NotOnOrAfter = time.Now().Add(-31 * time.Second) }},
		{"future conditions", func(r *saml.Response) { r.Assertion.Conditions.NotBefore = time.Now().Add(31 * time.Second) }},
		{"missing NotBefore", func(r *saml.Response) { r.Assertion.Conditions.NotBefore = time.Time{} }},
		{"expired bearer", func(r *saml.Response) {
			r.Assertion.Subject.SubjectConfirmations[0].SubjectConfirmationData.NotOnOrAfter = time.Now().Add(-31 * time.Second)
		}},
		{"future bearer", func(r *saml.Response) {
			r.Assertion.Subject.SubjectConfirmations[0].SubjectConfirmationData.NotBefore = time.Now().Add(31 * time.Second)
		}},
		{"missing authn", func(r *saml.Response) { r.Assertion.AuthnStatements = nil }},
		{"expired IdP session", func(r *saml.Response) {
			at := time.Now().Add(-31 * time.Second)
			r.Assertion.AuthnStatements[0].SessionNotOnOrAfter = &at
		}},
		{"duplicate username", func(r *saml.Response) {
			r.Assertion.AttributeStatements[0].Attributes[0].Values = append(r.Assertion.AttributeStatements[0].Attributes[0].Values, saml.AttributeValue{Value: "another"})
		}},
		{"oversized group", func(r *saml.Response) {
			r.Assertion.AttributeStatements[0].Attributes[1].Values[0].Value = strings.Repeat("a", 513)
		}},
		{"duplicate response assertion ID", func(r *saml.Response) { r.ID = r.Assertion.ID }},
	}
	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requestID, state := f.start(t, fmt.Sprint(i))
			response := f.response(requestID, fmt.Sprint(i))
			test.mutate(response)
			raw := f.signed(t, response, false)
			if _, _, _, err := f.provider.Complete(context.Background(), samlCallback(state, raw)); !errors.Is(err, errSAMLResponse) {
				t.Fatalf("hostile assertion accepted or error leaked: %v", err)
			}
			if _, _, _, err := f.provider.Complete(context.Background(), samlCallback(state, raw)); !errors.Is(err, adminauth.ErrReplay) {
				t.Fatal("failed callback retained flow")
			}
		})
	}
}

func TestSAMLRejectsParserEdgeCasesWithoutPanic(t *testing.T) {
	f := newSAMLFixture(t)
	tests := []struct {
		name   string
		mutate func(*etree.Element)
	}{
		{"missing assertion issuer", func(assertion *etree.Element) {
			issuer := assertion.FindElement("./Issuer")
			if issuer == nil {
				t.Fatal("assertion issuer missing before mutation")
			}
			assertion.RemoveChild(issuer)
		}},
		{"foreign namespace NameID", func(assertion *etree.Element) {
			subject := assertion.FindElement("./Subject")
			if subject == nil {
				t.Fatal("assertion subject missing before mutation")
			}
			nameID := subject.FindElement("./NameID")
			if nameID == nil {
				t.Fatal("assertion NameID missing before mutation")
			}
			subject.RemoveChild(nameID)
			foreign := etree.NewElement("x:NameID")
			foreign.CreateAttr("xmlns:x", "urn:routenest:test:foreign")
			foreign.SetText("spoofed-subject")
			subject.AddChild(foreign)
		}},
	}
	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requestID, state := f.start(t, fmt.Sprint(i))
			raw := f.signedAssertionTree(t, f.response(requestID, fmt.Sprint(i)), test.mutate)
			if _, _, _, err := f.provider.Complete(context.Background(), samlCallback(state, raw)); !errors.Is(err, errSAMLResponse) {
				t.Fatalf("hostile parser edge accepted or error leaked: %v", err)
			}
		})
	}
}

func TestSAMLRejectsTamperingUnsignedAndWeakSignatures(t *testing.T) {
	f := newSAMLFixture(t)
	for _, kind := range []string{"tampered", "unsigned", "weak algorithm", "duplicate assertion", "DTD", "too deep"} {
		t.Run(kind, func(t *testing.T) {
			id, state := f.start(t, kind)
			response := f.response(id, kind)
			raw := f.signed(t, response, false)
			switch kind {
			case "tampered":
				raw = bytes.Replace(raw, []byte(">Alice<"), []byte(">Mallory<"), 1)
			case "unsigned":
				doc := etree.NewDocument()
				doc.SetRoot(response.Element())
				raw, _ = doc.WriteToBytes()
			case "weak algorithm":
				raw = bytes.ReplaceAll(raw, []byte(dsig.RSASHA256SignatureMethod), []byte(dsig.RSASHA1SignatureMethod))
			case "duplicate assertion":
				doc := etree.NewDocument()
				_ = doc.ReadFromBytes(raw)
				assertion := doc.Root().FindElement("./Assertion")
				doc.Root().AddChild(assertion.Copy())
				raw, _ = doc.WriteToBytes()
			case "DTD":
				raw = append([]byte("<!DOCTYPE Response []>"), raw...)
			case "too deep":
				raw = []byte(strings.Repeat("<nested>", 65) + strings.Repeat("</nested>", 65))
			}
			if _, _, _, err := f.provider.Complete(context.Background(), samlCallback(state, raw)); !errors.Is(err, errSAMLResponse) {
				t.Fatalf("accepted hostile XML: %v", err)
			}
		})
	}
}

func TestSAMLReplayAcrossFlowsAndConcurrency(t *testing.T) {
	f := newSAMLFixture(t)
	id, state := f.start(t, "first")
	response := f.response(id, "same-id")
	raw := f.signed(t, response, false)
	var wg sync.WaitGroup
	errorsFound := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _, err := f.provider.Complete(context.Background(), samlCallback(state, raw))
			errorsFound <- err
		}()
	}
	wg.Wait()
	close(errorsFound)
	successes, replays := 0, 0
	for err := range errorsFound {
		if err == nil {
			successes++
		} else if errors.Is(err, adminauth.ErrReplay) {
			replays++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || replays != 1 {
		t.Fatal("concurrent callback did not consume once")
	}
	refreshed, err := NewSAMLProvider(context.Background(), f.provider.id, f.provider.revision, f.provider.config, nil, f.server.Client(), f.provider.flows, f.provider.replay)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Fingerprint() != f.provider.Fingerprint() {
		t.Fatal("unchanged metadata changed fingerprint")
	}
	f.provider = refreshed
	id, state = f.start(t, "second")
	response = f.response(id, "same-id")
	raw = f.signed(t, response, false)
	if _, _, _, err := f.provider.Complete(context.Background(), samlCallback(state, raw)); !errors.Is(err, adminauth.ErrReplay) {
		t.Fatal("assertion replay under new flow accepted")
	}
	distinct, err := NewSAMLProvider(context.Background(), "another-idp", 7, f.provider.config, nil, f.server.Client(), f.provider.flows, f.provider.replay)
	if err != nil {
		t.Fatal(err)
	}
	f.provider = distinct
	id, state = f.start(t, "third")
	response = f.response(id, "same-id")
	raw = f.signed(t, response, false)
	if _, _, _, err := f.provider.Complete(context.Background(), samlCallback(state, raw)); err != nil {
		t.Fatalf("distinct provider replay scope collided: %v", err)
	}
}

func TestSAMLFormBindingLimitsRevisionAndRoles(t *testing.T) {
	f := newSAMLFixture(t)
	for _, kind := range []string{"wrong method", "wrong host", "wrong path", "query", "wrong media", "oversized", "duplicate state", "artifact", "wrong provider", "stale revision", "rotated fingerprint", "no mapped role"} {
		t.Run(kind, func(t *testing.T) {
			id, state := f.start(t, kind)
			response := f.response(id, kind)
			if kind == "no mapped role" {
				f.provider.config.DefaultRoles = nil
				response.Assertion.AttributeStatements[0].Attributes[1].Values = []saml.AttributeValue{{Value: "security_admin"}}
				defer func() { f.provider.config.DefaultRoles = []adminauth.Role{adminauth.RoleViewer} }()
			}
			raw := f.signed(t, response, false)
			request := samlCallback(state, raw)
			switch kind {
			case "wrong method":
				request.Method = http.MethodGet
			case "wrong host":
				request.Host = "evil.example"
			case "wrong path":
				request.URL.Path = "/other"
			case "query":
				request.URL.RawQuery = "x=1"
			case "wrong media":
				request.Header.Set("Content-Type", "multipart/form-data")
			case "oversized":
				request.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", samlFormLimit+1)))
				request.ContentLength = -1
			case "duplicate state":
				body, _ := io.ReadAll(request.Body)
				request.Body = io.NopCloser(strings.NewReader(string(body) + "&RelayState=" + url.QueryEscape(state)))
			case "artifact":
				body, _ := io.ReadAll(request.Body)
				request.Body = io.NopCloser(strings.NewReader(string(body) + "&SAMLart=attack"))
			case "wrong provider":
				request = samlCallback(strings.Replace(state, "test-idp.", "wrong-idp.", 1), raw)
			case "stale revision":
				f.provider.revision++
				defer func() { f.provider.revision-- }()
			case "rotated fingerprint":
				fingerprint := f.provider.fingerprint
				f.provider.fingerprint = "rotated"
				defer func() { f.provider.fingerprint = fingerprint }()
			}
			if _, _, _, err := f.provider.Complete(context.Background(), request); err == nil {
				t.Fatal("invalid callback accepted")
			}
		})
	}
}

func TestSAMLEncryptedAssertionAndWeakInnerSignature(t *testing.T) {
	f := newSAMLFixture(t)
	f.provider.sp.Key = f.key
	f.provider.sp.Certificate = f.certificate
	for _, weak := range []bool{false, true} {
		id, state := f.start(t, fmt.Sprint(weak))
		response := f.response(id, fmt.Sprint(weak))
		method := dsig.RSASHA256SignatureMethod
		if weak {
			method = dsig.RSASHA1SignatureMethod
		}
		idpRequest := &saml.IdpAuthnRequest{IDP: &saml.IdentityProvider{Key: f.key, Certificate: f.certificate, SignatureMethod: method}, Assertion: response.Assertion, SPSSODescriptor: &f.provider.sp.Metadata().SPSSODescriptors[0]}
		if err := idpRequest.MakeAssertionEl(); err != nil {
			t.Fatal(err)
		}
		response.Assertion = nil
		response.EncryptedAssertion = idpRequest.AssertionEl
		doc := etree.NewDocument()
		doc.SetRoot(response.Element())
		raw, err := doc.WriteToBytes()
		if err != nil {
			t.Fatal(err)
		}
		identity, _, _, err := f.provider.Complete(context.Background(), samlCallback(state, raw))
		if weak {
			if !errors.Is(err, errSAMLResponse) {
				t.Fatal("weak encrypted signature accepted")
			}
		} else if err != nil || identity.Subject != "stable-subject" {
			t.Fatalf("encrypted assertion failed: %v", err)
		}
	}
}

func TestSAMLMetadataFailureBoundsAndSigningKey(t *testing.T) {
	f := newSAMLFixture(t)
	for _, kind := range []string{"status", "oversized", "HTTP redirect", "expired", "no signing certificate", "HTTP IdP", "multiple IdPs"} {
		t.Run(kind, func(t *testing.T) {
			metadata := f.metadata
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch kind {
				case "status":
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				case "oversized":
					_, _ = w.Write([]byte(strings.Repeat("x", samlMetadataLimit+1)))
					return
				case "HTTP redirect":
					http.Redirect(w, r, "http://127.0.0.1/metadata", http.StatusFound)
					return
				case "expired":
					metadata.ValidUntil = time.Now().Add(-time.Minute)
				case "no signing certificate":
					metadata.IDPSSODescriptors = []saml.IDPSSODescriptor{{SingleSignOnServices: []saml.Endpoint{{Binding: saml.HTTPRedirectBinding, Location: "https://idp.example.test/login"}}}}
				case "HTTP IdP":
					metadata.IDPSSODescriptors = []saml.IDPSSODescriptor{f.metadata.IDPSSODescriptors[0]}
					metadata.IDPSSODescriptors[0].SingleSignOnServices = []saml.Endpoint{{Binding: saml.HTTPRedirectBinding, Location: "http://idp.example.test/login"}}
				case "multiple IdPs":
					raw, _ := xml.Marshal(saml.EntitiesDescriptor{EntityDescriptors: []saml.EntityDescriptor{metadata, metadata}})
					_, _ = w.Write(raw)
					return
				}
				raw, _ := xml.Marshal(metadata)
				_, _ = w.Write(raw)
			}))
			defer server.Close()
			location, _ := url.Parse(server.URL)
			if _, _, err := loadSAMLMetadata(context.Background(), server.Client(), location); err == nil {
				t.Fatal("invalid metadata accepted")
			}
		})
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.certificate.Raw})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(f.key)})
	t.Setenv("ROUTENEST_SAML_TEST_CERT", string(certificatePEM))
	t.Setenv("ROUTENEST_SAML_TEST_KEY", string(keyPEM))
	resolver, err := NewSecretResolver(nil)
	if err != nil {
		t.Fatal(err)
	}
	config := f.provider.config
	config.Certificate = &SecretRef{Kind: "env", Name: "ROUTENEST_SAML_TEST_CERT"}
	config.PrivateKey = &SecretRef{Kind: "env", Name: "ROUTENEST_SAML_TEST_KEY"}
	p, err := NewSAMLProvider(context.Background(), "signed-idp", 1, config, resolver, f.server.Client(), adminauth.NewAuthFlowStore())
	if err != nil {
		t.Fatal(err)
	}
	redirect, err := p.Start("source", "/admin/")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(redirect)
	if u.Query().Get("Signature") == "" || u.Query().Get("SigAlg") != dsig.RSASHA256SignatureMethod {
		t.Fatal("AuthnRequest was not signed")
	}
	if strings.Contains(fmt.Sprint(p), "PRIVATE KEY") || strings.Contains(fmt.Sprint(p), "CERTIFICATE") {
		t.Fatal("provider formats secret material")
	}
}
