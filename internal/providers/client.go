package providers

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"routenest/internal/connections"
	"routenest/internal/egress"
	"routenest/internal/platform"
	"routenest/internal/upstream/openaicompat"
	"routenest/internal/upstream/qoder"
)

func messageCount(params map[string]any) int {
	if msgs, ok := params["messages"].([]any); ok {
		return len(msgs)
	}
	return 0
}

func Call(p *CustomProvider, params map[string]any, stream bool) (*http.Response, error) {
	protocol := Protocol(p)

	// The qoder protocol builds its own signed body, so the OpenAI body
	// preparation is skipped entirely for it.
	var bodyJSON []byte
	var err error
	if protocol != "qoder" {
		opts := BodyOptionsProvider()
		if opts.Logf == nil {
			opts.Logf = log.Printf
		}
		bodyJSON, err = openaicompat.PrepareBody(params, stream, opts)
		if err != nil {
			return nil, err
		}
	}

	timeout := time.Duration(p.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	// Provider overrides get their own cached transport; an empty override
	// keeps reusing the global transport so existing behavior is unchanged.
	tr, err := providerTransportFor(p.ProxyURL)
	if err != nil {
		return nil, fmt.Errorf("provider %s: %w", p.Name, err)
	}
	httpCli := &http.Client{Transport: tr, Timeout: timeout}
	pc := &openaicompat.Client{
		HTTP:    httpCli,
		BaseURL: p.BaseURL,
		APIKey:  p.APIKey,
		Headers: p.Headers,
	}

	modelKey := fmt.Sprintf("%v", params["model"])
	log.Printf("  provider upstream: name=%s model=%v stream=%v msgs=%d", p.Name, modelKey, stream, messageCount(params))

	// Wave-4 connection pool: walk the usable connections in rotation order.
	// A connection-authenticated 401/403/429 parks that connection and retries
	// in place with the next one BEFORE the outer next-provider loop ever sees
	// a failure. API-key-only providers make exactly one attempt, as before.
	connIDs := connections.ProviderConnIDs(p)
	conns := connections.UsableConnectionsForProvider(p)
	if len(connIDs) > 0 && len(conns) == 0 {
		return nil, fmt.Errorf("provider %s: connection unavailable (expired or reauth required)", p.Name)
	}
	if len(conns) == 0 {
		conns = []*Connection{nil}
	}
	// qoder signs every request with per-account credentials (user id + machine
	// id), so it cannot run on a bare API key.
	if protocol == "qoder" && conns[0] == nil {
		return nil, fmt.Errorf("provider %s: qoder requires a logged-in connection", p.Name)
	}
	for i, conn := range conns {
		token := ""
		extra := map[string]string{}
		// Bound connection: authenticate with the connection token and inject
		// connection-level dynamic headers (e.g. the Kilo org header).
		if conn != nil {
			spec, hasSpec := connections.ProviderDeviceFlows[conn.Provider]
			if hasSpec {
				token = connections.ConnectionBearer(conn, spec)
			} else {
				token = conn.AccessToken
			}
			if hasSpec && spec.OrgHeader != "" {
				if oid := conn.ProviderSpecificData["orgId"]; oid != "" {
					extra[spec.OrgHeader] = oid
				}
			}
		}
		// Count at dispatch: RPM/daily budget measures requests actually sent upstream.
		recordProviderDispatch(p.ID, modelKey)
		started := time.Now()
		var resp *http.Response
		if protocol == "qoder" {
			resp, err = qoder.Chat(context.Background(), httpCli, qoder.Creds{
				UserID:    conn.ProviderSpecificData["userId"],
				AuthToken: token,
				Email:     conn.Email,
				MachineID: conn.ProviderSpecificData["machineId"],
				BaseURL:   p.BaseURL,
			}, params, stream)
		} else {
			var req *http.Request
			req, err = pc.BuildRequest(bodyJSON, openaicompat.RequestOptions{Token: token, Headers: extra})
			if err != nil {
				return nil, fmt.Errorf("create provider request: %w", err)
			}
			resp, err = httpCli.Do(req)
		}
		latency := time.Since(started).Milliseconds()
		if err != nil {
			recordProviderOutcome(p.ID, modelKey, false, latency)
			return nil, fmt.Errorf("provider request: %w", err)
		}
		if resp.StatusCode == http.StatusOK {
			recordProviderOutcome(p.ID, modelKey, true, latency)
			if conn != nil {
				// Consumed a real dispatch: move rotation past this connection.
				connections.AdvanceConnCursor(p, conn.ID)
			}
			connections.TagResponseConn(resp, conn)
			return resp, nil
		}
		recordProviderOutcome(p.ID, modelKey, false, latency)
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		resp.Body.Close()
		bodyStr := string(bodyBytes)
		// Connection-specific failure with siblings left in the pool:
		// park this connection (401/403 -> reauth, 429 -> timed cooldown)
		// and retry with the next connection now instead of failing the provider.
		connRetryable := conn != nil && (resp.StatusCode == http.StatusUnauthorized ||
			resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests)
		if connRetryable && i < len(conns)-1 {
			if resp.StatusCode == http.StatusTooManyRequests {
				connections.SetConnectionCooldown(conn.ID, time.Now().Add(connections.ConnectionRateLimitCooldown))
			} else {
				connections.MarkConnectionReauth(conn.ID)
			}
			log.Printf("  provider conn failover: name=%s conn=%s status=%d -> next connection", p.Name, conn.ID, resp.StatusCode)
			continue
		}
		// Final failure: original marking semantics.
		if conn != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			connections.MarkConnectionReauth(conn.ID)
		}
		if until := egress.CooldownUntil(resp.StatusCode, resp.Header.Get("Retry-After"), bodyStr); !until.IsZero() {
			SetCooldown(p.ID, modelKey, until)
		}
		return nil, egress.NewAPIError(resp.StatusCode, platform.Truncate(bodyStr, 500))
	}
	return nil, fmt.Errorf("provider %s: no connection attempts made", p.Name)
}
