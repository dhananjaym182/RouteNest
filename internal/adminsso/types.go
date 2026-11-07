package adminsso

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"routenest/internal/adminauth"
)

var providerIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

type SecretRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type OIDCConfig struct {
	Issuer        string                      `json:"issuer"`
	ClientID      string                      `json:"clientID"`
	ClientSecret  SecretRef                   `json:"clientSecret"`
	RedirectURL   string                      `json:"redirectURL"`
	Scopes        []string                    `json:"scopes,omitempty"`
	UsernameClaim string                      `json:"usernameClaim,omitempty"`
	GroupsClaim   string                      `json:"groupsClaim,omitempty"`
	DefaultRoles  []adminauth.Role            `json:"defaultRoles,omitempty"`
	GroupRoleMap  map[string][]adminauth.Role `json:"groupRoleMap,omitempty"`
}

type SAMLConfig struct {
	MetadataURL       string                      `json:"metadataURL"`
	EntityID          string                      `json:"entityID"`
	MetadataEndpoint  string                      `json:"metadataEndpoint"`
	ACSURL            string                      `json:"acsURL"`
	UsernameAttribute string                      `json:"usernameAttribute,omitempty"`
	GroupsAttribute   string                      `json:"groupsAttribute,omitempty"`
	DefaultRoles      []adminauth.Role            `json:"defaultRoles,omitempty"`
	GroupRoleMap      map[string][]adminauth.Role `json:"groupRoleMap,omitempty"`
	Certificate       *SecretRef                  `json:"certificate,omitempty"`
	PrivateKey        *SecretRef                  `json:"privateKey,omitempty"`
}

type ProviderConfig struct {
	ID      string               `json:"id"`
	Type    adminauth.AuthMethod `json:"type"`
	Enabled bool                 `json:"enabled"`
	OIDC    *OIDCConfig          `json:"oidc,omitempty"`
	SAML    *SAMLConfig          `json:"saml,omitempty"`
}

type ProviderView struct {
	ID            string               `json:"id"`
	Type          adminauth.AuthMethod `json:"type"`
	Enabled       bool                 `json:"enabled"`
	Revision      uint64               `json:"revision"`
	Ready         bool                 `json:"ready"`
	Reason        string               `json:"reason,omitempty"`
	Fingerprint   string               `json:"fingerprint,omitempty"`
	Configuration json.RawMessage      `json:"configuration,omitempty"`
}

// FederatedIdentity is the verified, protocol-neutral identity produced by an
// OIDC or SAML adapter. Only values derived from a fully verified response may
// cross this boundary into adminauth session issuance.
type FederatedIdentity struct {
	Provider string
	Subject  string
	Username string
	Roles    []adminauth.Role
	Method   adminauth.AuthMethod
}

func (c ProviderConfig) validate() error {
	if !providerIDPattern.MatchString(c.ID) {
		return fmt.Errorf("invalid SSO provider ID")
	}
	switch c.Type {
	case adminauth.MethodOIDC:
		if c.OIDC == nil || c.SAML != nil {
			return fmt.Errorf("OIDC configuration required")
		}
		return c.OIDC.validate()
	case adminauth.MethodSAML:
		if len(c.ID) > 36 {
			return fmt.Errorf("SAML provider ID is too long")
		}
		if c.SAML == nil || c.OIDC != nil {
			return fmt.Errorf("SAML configuration required")
		}
		return c.SAML.validate()
	default:
		return fmt.Errorf("invalid SSO provider type")
	}
}

func (c OIDCConfig) validate() error {
	issuer, err := url.Parse(c.Issuer)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" {
		return fmt.Errorf("OIDC issuer must be an HTTPS origin or issuer path")
	}
	redirect, err := url.Parse(c.RedirectURL)
	if err != nil || redirect.Scheme == "" || redirect.Host == "" || redirect.User != nil || redirect.RawQuery != "" || redirect.Fragment != "" {
		return fmt.Errorf("invalid OIDC redirect URL")
	}
	if c.ClientID == "" || len(c.ClientID) > 512 {
		return fmt.Errorf("invalid OIDC client ID")
	}
	if err := c.ClientSecret.validate(); err != nil {
		return err
	}
	return validateRoleMapping(c.DefaultRoles, c.GroupRoleMap)
}

func (c SAMLConfig) validate() error {
	metadata, err := url.Parse(c.MetadataURL)
	if err != nil || metadata.Scheme != "https" || metadata.Host == "" || metadata.User != nil || metadata.Fragment != "" {
		return fmt.Errorf("SAML metadata URL must use HTTPS")
	}
	entity, err := url.Parse(c.EntityID)
	if err != nil || entity.Scheme == "" || entity.Host == "" || entity.User != nil || entity.RawQuery != "" || entity.Fragment != "" {
		return fmt.Errorf("invalid SAML entity ID")
	}
	metadataEndpoint, err := url.Parse(c.MetadataEndpoint)
	if err != nil || metadataEndpoint.Scheme == "" || metadataEndpoint.Host == "" || metadataEndpoint.User != nil || metadataEndpoint.Fragment != "" {
		return fmt.Errorf("invalid SAML metadata endpoint")
	}
	acs, err := url.Parse(c.ACSURL)
	if err != nil || acs.Scheme == "" || acs.Host == "" || acs.User != nil || acs.RawQuery != "" || acs.Fragment != "" {
		return fmt.Errorf("invalid SAML ACS URL")
	}
	if (c.Certificate == nil) != (c.PrivateKey == nil) {
		return fmt.Errorf("SAML certificate and private key references must be configured together")
	}
	if c.Certificate != nil {
		if err := c.Certificate.validate(); err != nil {
			return err
		}
		if err := c.PrivateKey.validate(); err != nil {
			return err
		}
	}
	return validateRoleMapping(c.DefaultRoles, c.GroupRoleMap)
}

func (r SecretRef) validate() error {
	if (r.Kind != "env" && r.Kind != "file") || strings.TrimSpace(r.Name) == "" || len(r.Name) > 4096 {
		return fmt.Errorf("invalid secret reference")
	}
	return nil
}

func validateRoleMapping(defaults []adminauth.Role, mapping map[string][]adminauth.Role) error {
	roles := append([]adminauth.Role(nil), defaults...)
	for group, values := range mapping {
		if strings.TrimSpace(group) == "" || len(group) > 512 {
			return fmt.Errorf("invalid SSO group mapping")
		}
		roles = append(roles, values...)
	}
	if len(roles) == 0 {
		return fmt.Errorf("SSO provider must map at least one role")
	}
	for _, role := range roles {
		if !adminauth.ValidRole(role) || role == adminauth.RoleBreakGlass {
			return fmt.Errorf("invalid federated role %q", role)
		}
	}
	return nil
}

func mapRoles(defaults []adminauth.Role, mapping map[string][]adminauth.Role, groups []string) []adminauth.Role {
	seen := map[adminauth.Role]bool{}
	roles := make([]adminauth.Role, 0, len(defaults))
	add := func(values []adminauth.Role) {
		for _, role := range values {
			if !seen[role] {
				seen[role] = true
				roles = append(roles, role)
			}
		}
	}
	add(defaults)
	for _, group := range groups {
		add(mapping[group])
	}
	sort.Slice(roles, func(i, j int) bool { return roles[i] < roles[j] })
	return roles
}

func encodeFlowState(providerID, handle string) (string, error) {
	if !providerIDPattern.MatchString(providerID) || len(handle) != 43 || strings.Contains(handle, ".") {
		return "", fmt.Errorf("invalid SSO flow state")
	}
	return providerID + "." + handle, nil
}

// ParseFlowState identifies the provider without trusting browser-controlled
// configuration. The opaque handle remains authoritative in the server-side
// one-time flow store.
func ParseFlowState(value string) (providerID, handle string, err error) {
	providerID, handle, found := strings.Cut(value, ".")
	if !found || !providerIDPattern.MatchString(providerID) || len(handle) != 43 || strings.Contains(handle, ".") {
		return "", "", fmt.Errorf("invalid SSO flow state")
	}
	return providerID, handle, nil
}
