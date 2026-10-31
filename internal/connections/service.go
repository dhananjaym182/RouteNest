package connections

// Owner API for provider login connections. It has no HTTP in it: the admin
// adapters (internal/admin) parse requests, choose status codes and build the
// response envelopes. Every value that leaves this package through this API is
// redacted here, so no adapter can forget to do it.

import (
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"routenest/internal/platform"
)

// probeBodyLimit bounds the upstream body returned by a connection test.
const probeBodyLimit = 64 << 10

// probeBodyShown is how much of the upstream body a connection test returns.
const probeBodyShown = 240

// exchangedTokenKey is the providerSpecificData key that holds a short-lived job
// token. It is a secret and is never returned.
const exchangedTokenKey = "exchangedToken"

// RedactedConnection returns a copy of c that is safe to return to a client: the
// access and refresh tokens and the exchanged job token are removed.
func RedactedConnection(c Connection) Connection {
	out := c
	out.AccessToken = ""
	out.RefreshToken = ""
	if c.ProviderSpecificData != nil {
		out.ProviderSpecificData = make(map[string]string, len(c.ProviderSpecificData))
		for k, v := range c.ProviderSpecificData {
			if k == exchangedTokenKey {
				continue
			}
			out.ProviderSpecificData[k] = v
		}
	}
	return out
}

// ListConnections returns every connection, redacted, in stored order.
func ListConnections() []Connection {
	LoadConnections()
	ConnectionsMu.Lock()
	defer ConnectionsMu.Unlock()
	out := make([]Connection, 0, len(connections))
	for _, c := range connections {
		out = append(out, RedactedConnection(*c))
	}
	return out
}

// ConnectionFlowNames lists the provider login flows, sorted.
func ConnectionFlowNames() []string {
	flows := make([]string, 0, len(ProviderDeviceFlows))
	for p := range ProviderDeviceFlows {
		flows = append(flows, p)
	}
	sort.Strings(flows)
	return flows
}

// LoginStart is the result of starting a device login.
type LoginStart struct {
	SessionID string
	UserCode  string
	AuthURL   string
}

// StartProviderLogin begins a device login for provider. The provider name is
// used as given; adapters trim it.
func StartProviderLogin(provider string) (LoginStart, error) {
	sess, err := startProviderDeviceLogin(provider)
	if err != nil {
		return LoginStart{}, err
	}
	return LoginStart{SessionID: sess.ID, UserCode: sess.UserCode, AuthURL: sess.AuthURL}, nil
}

// LoginStatus is the state of a device login session.
type LoginStatus struct {
	Done       bool
	Success    bool
	Error      string
	UserCode   string
	AuthURL    string
	Connection *Connection // redacted; nil until the login completes
}

// ProviderLoginStatus reports the state of a session. ok is false when the
// session does not exist.
func ProviderLoginStatus(sessionID string) (status LoginStatus, ok bool) {
	providerLoginMu.Lock()
	defer providerLoginMu.Unlock()
	sess, found := providerLoginSessions[sessionID]
	if !found {
		return LoginStatus{}, false
	}
	status = LoginStatus{Done: sess.Done, Success: sess.Success, Error: sess.Error, UserCode: sess.UserCode, AuthURL: sess.AuthURL}
	if sess.Connection != nil {
		redacted := RedactedConnection(*sess.Connection)
		status.Connection = &redacted
	}
	return status, true
}

// ProbeResult is the outcome of a connection test. When Error is set, the test
// did not reach a verdict; otherwise OK, Status and Body describe the upstream
// answer. Body is redacted and shortened.
type ProbeResult struct {
	OK     bool
	Status int
	Body   string
	Error  string
}

// TestConnection probes the connection's model-list endpoint with its bearer
// credential. A 401 or 403 answer, or an expired token without refresh, marks the
// connection for re-login. found is false when no connection has the id.
func TestConnection(id string) (result ProbeResult, found bool) {
	LoadConnections()
	ConnectionsMu.Lock()
	var conn *Connection
	for _, c := range connections {
		if c.ID == id {
			cp := *c
			conn = &cp
			break
		}
	}
	ConnectionsMu.Unlock()
	if conn == nil {
		return ProbeResult{}, false
	}
	spec, ok := ProviderDeviceFlows[conn.Provider]
	if !ok || spec.ModelsTestURL == "" {
		return ProbeResult{Error: "no test endpoint for provider " + conn.Provider}, true
	}
	if conn.ExpiresAt > 0 && time.Now().UnixMilli() >= conn.ExpiresAt && conn.RefreshToken == "" {
		MarkConnectionReauth(conn.ID)
		return ProbeResult{Error: "token expired; please re-login"}, true
	}
	bearer := ConnectionBearer(conn, spec)
	hreq, _ := http.NewRequest(http.MethodGet, spec.ModelsTestURL, nil)
	hreq.Header.Set("Authorization", "Bearer "+bearer)
	if spec.UserAgent != "" {
		hreq.Header.Set("User-Agent", spec.UserAgent)
	}
	if spec.OrgHeader != "" && conn.ProviderSpecificData["orgId"] != "" {
		hreq.Header.Set(spec.OrgHeader, conn.ProviderSpecificData["orgId"])
	}
	resp, err := OAuthHTTPClient(20 * time.Second).Do(hreq)
	if err != nil {
		return ProbeResult{Error: redactSecrets(err.Error(), secretsOf(conn, bearer)...)}, true
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, probeBodyLimit))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		MarkConnectionReauth(conn.ID)
	}
	shown := platform.Truncate(strings.TrimSpace(string(body)), probeBodyShown)
	return ProbeResult{
		OK:     resp.StatusCode == http.StatusOK,
		Status: resp.StatusCode,
		Body:   redactSecrets(shown, secretsOf(conn, bearer)...),
	}, true
}

// secretsOf lists the values that must never appear in a probe answer.
func secretsOf(conn *Connection, bearer string) []string {
	out := []string{bearer, conn.AccessToken, conn.RefreshToken, conn.ProviderSpecificData[exchangedTokenKey]}
	var nonEmpty []string
	for _, s := range out {
		if len(s) >= 4 {
			nonEmpty = append(nonEmpty, s)
		}
	}
	return nonEmpty
}

// redactSecrets replaces every secret in s with a fixed mask.
func redactSecrets(s string, secrets ...string) string {
	for _, sec := range secrets {
		if sec != "" {
			s = strings.ReplaceAll(s, sec, "***")
		}
	}
	return s
}
