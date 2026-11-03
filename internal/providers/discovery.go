package providers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"routenest/internal/connections"
	"routenest/internal/egress"
	"routenest/internal/modelcatalog"
	"routenest/internal/upstream/qoder"
)

// discoveryTimeout bounds one catalogue read, matching the previous probes.
const discoveryTimeout = 15 * time.Second

// errConnectionUnavailable is reported when a provider is bound to login
// connections but none is usable (expired or reauth required).
const errConnectionUnavailable = "connection unavailable (expired or reauth required)"

// DiscoveryOverrides replace stored provider fields for one probe. Empty fields
// keep the stored value. A nil Headers keeps the stored headers. A non-nil map,
// including one with empty values (which delete headers), replaces them.
type DiscoveryOverrides struct {
	BaseURL string
	APIKey  string
	Headers map[string]string
}

// DiscoverModels is the single provider model-discovery entry point. Admin
// probes, import previews and periodic rechecks all call it. It:
//
//  1. resolves the base URL, key and headers, applying overrides;
//  2. resolves credentials from a bound login connection, including the
//     organization header when the flow defines one;
//  3. selects the catalogue protocol: the signed Qoder adapter for qoder
//     providers, and the generic OpenAI-shaped path for everything else;
//  4. redacts the credentials from every error it returns.
//
// It writes no state.
func DiscoverModels(p CustomProvider, ov DiscoveryOverrides) ([]upstreamModelEntry, error) {
	if b := strings.TrimRight(strings.TrimSpace(ov.BaseURL), "/"); b != "" {
		p.BaseURL = b
	}
	p.BaseURL = strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	if Protocol(&p) == "qoder" {
		// The Qoder adapter resolves its own connection and signature.
		entries, err := DiscoverQoderModels(&p)
		if err != nil {
			return nil, fmt.Errorf("%s", modelcatalog.Redact(err.Error(), connectionSecrets(&p)...))
		}
		return entries, nil
	}

	apiKey := p.APIKey
	if ov.APIKey != "" {
		apiKey = ov.APIKey
	}
	headers := map[string]string{}
	if ov.Headers != nil {
		for k, v := range ov.Headers {
			headers[k] = v
		}
	} else {
		for k, v := range p.Headers {
			// Stored empty values are not sent, as in recheck (no deletion).
			if v != "" {
				headers[k] = v
			}
		}
	}

	var connToken string
	if len(connections.ProviderConnIDs(&p)) > 0 {
		conn := connections.PickConnectionForProvider(&p)
		if conn == nil {
			return nil, fmt.Errorf("%s", errConnectionUnavailable)
		}
		if spec, hasSpec := connections.ProviderDeviceFlows[conn.Provider]; hasSpec {
			apiKey = connections.ConnectionBearer(conn, spec)
			if spec.OrgHeader != "" {
				if oid := conn.ProviderSpecificData["orgId"]; oid != "" {
					headers[spec.OrgHeader] = oid
				}
			}
		} else {
			apiKey = conn.AccessToken
		}
		connToken = apiKey
	}

	fetcherBase, fetcherType := modelcatalog.CatalogueSourceFor(p.BaseURL)
	entries, err := modelcatalog.Discover(modelcatalog.Request{
		Client:      discoveryClient(),
		BaseURL:     p.BaseURL,
		APIKey:      apiKey,
		Headers:     headers,
		FetcherBase: fetcherBase,
		FetcherType: fetcherType,
	})
	if err != nil {
		secrets := []string{apiKey, connToken}
		for _, v := range headers {
			if len(v) >= 8 {
				secrets = append(secrets, v)
			}
		}
		return nil, fmt.Errorf("%s", modelcatalog.Redact(err.Error(), secrets...))
	}
	return entries, nil
}

// connectionSecrets returns the bound connection's tokens, for redaction.
func connectionSecrets(p *CustomProvider) []string {
	conn := connections.PickConnectionForProvider(p)
	if conn == nil {
		return nil
	}
	var out []string
	for _, v := range []string{conn.AccessToken, conn.RefreshToken} {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// discoveryClient shares the egress transport, so catalogue reads use the same
// proxy pool as chat traffic.
func discoveryClient() *http.Client {
	return &http.Client{Timeout: discoveryTimeout, Transport: egress.Client.Transport}
}

// FetcherBaseForBase returns the preset catalogue base for a provider base URL.
func FetcherBaseForBase(baseURL string) string {
	return modelcatalog.FetcherBaseFor(baseURL)
}

// DiscoverQoderModels is the Qoder protocol adapter. The Qoder catalogue is
// COSY-signed with the bound account's identity, so it cannot go through the
// OpenAI-shaped path, and it needs the connection rather than a bearer key. It
// is selected by protocol in DiscoverModels.
func DiscoverQoderModels(p *CustomProvider) ([]upstreamModelEntry, error) {
	conn := connections.PickConnectionForProvider(p)
	if conn == nil {
		return nil, fmt.Errorf("%s", errConnectionUnavailable)
	}
	token := conn.AccessToken
	if spec, hasSpec := connections.ProviderDeviceFlows[conn.Provider]; hasSpec {
		token = connections.ConnectionBearer(conn, spec)
	}
	ids, err := qoder.Models(context.Background(), connections.OAuthHTTPClient(discoveryTimeout), qoder.Creds{
		UserID:    conn.ProviderSpecificData["userId"],
		AuthToken: token,
		Email:     conn.Email,
		MachineID: conn.ProviderSpecificData["machineId"],
		BaseURL:   p.BaseURL,
	})
	if err != nil {
		return nil, err
	}
	out := make([]upstreamModelEntry, 0, len(ids))
	for _, id := range ids {
		// Namespace entries under qoder/ so they cannot collide with model ids
		// from other providers (Chat strips the prefix before dispatch).
		out = append(out, upstreamModelEntry{ID: "qoder/" + id})
	}
	return out, nil
}
