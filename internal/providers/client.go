package providers

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"routenest/internal/connections"
	"routenest/internal/dispatch"
	"routenest/internal/domain"
	"routenest/internal/egress"
	"routenest/internal/platform"
	"routenest/internal/provideradapters"
	"routenest/internal/providerreg"
	"routenest/internal/providerstate"
)

func messageCount(params map[string]any) int {
	if msgs, ok := params["messages"].([]any); ok {
		return len(msgs)
	}
	return 0
}

type callOptions struct {
	probe bool
}

func Call(p *CustomProvider, params map[string]any, stream bool) (*http.Response, error) {
	return call(p, params, stream, callOptions{})
}

// ProbeCall performs one explicit admin probe. It preserves normal upstream
// handling while bypassing transient connection cooling and configured budget
// admission; production Call semantics and accounting remain unchanged.
func ProbeCall(p *CustomProvider, params map[string]any, stream bool) (*http.Response, error) {
	return call(p, params, stream, callOptions{probe: true})
}

func call(p *CustomProvider, params map[string]any, stream bool, options callOptions) (*http.Response, error) {
	if p == nil {
		return nil, fmt.Errorf("provider is nil")
	}
	adapterID := customInstance(providerreg.Builtin(), *p).AdapterID
	bundle, ok := provideradapters.Default().Get(adapterID)
	if !ok {
		return nil, fmt.Errorf("provider %s: adapter %q is unavailable", p.Name, adapterID)
	}

	timeout := time.Duration(p.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	// Provider overrides get their own cached transport; an empty override
	// keeps reusing the global transport so existing behavior is unchanged.
	tr, err := providerTransportFor(p.ID, p.ProxyURL)
	if err != nil {
		return nil, fmt.Errorf("provider %s: %w", p.Name, err)
	}
	httpCli := &http.Client{Transport: tr, Timeout: timeout}
	modelKey := fmt.Sprintf("%v", params["model"])
	// modelKey stays the client-visible name: cooldowns, budgets and outcome
	// statistics are keyed by it. Only the request itself carries the upstream ID.
	upstreamModel := UpstreamModelFor(*p, modelKey)
	if upstreamModel != modelKey {
		sent := make(map[string]any, len(params))
		for key, value := range params {
			sent[key] = value
		}
		sent["model"] = upstreamModel
		params = sent
	}
	log.Printf("  provider upstream: name=%s model=%v upstream=%s stream=%v msgs=%d", p.Name, modelKey, upstreamModel, stream, messageCount(params))

	// Wave-4 connection pool: walk the usable connections in rotation order.
	// A connection-authenticated 401/403/429 parks that connection and retries
	// in place with the next one BEFORE the outer next-provider loop ever sees
	// a failure. API-key-only providers make exactly one attempt, as before.
	connIDs := connections.ProviderConnIDs(p)
	var conns []*Connection
	if options.probe {
		conns = connections.ProbeConnectionsForProvider(p)
	} else {
		conns = connections.UsableConnectionsForProvider(p)
	}
	if len(connIDs) > 0 && len(conns) == 0 {
		return nil, fmt.Errorf("provider %s: connection unavailable (expired or reauth required)", p.Name)
	}
	if len(conns) == 0 {
		conns = []*Connection{nil}
	}
	for i, conn := range conns {
		token := ""
		extra := map[string]string{}
		credential := provideradapters.Credential{}
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
			metadata := make(map[string]string, len(conn.ProviderSpecificData))
			for key, value := range conn.ProviderSpecificData {
				metadata[key] = value
			}
			credential = provideradapters.Credential{
				ID: domain.CredentialID("connection:" + conn.ID), Token: token,
				Email: conn.Email, Headers: extra, Metadata: metadata,
			}
		} else if p.APIKey != "" {
			credential.ID = domain.CredentialID("api-key:" + p.ID)
		}
		invocationID, err := dispatch.GenerateID()
		if err != nil {
			return nil, fmt.Errorf("provider %s: invocation identity: %w", p.Name, err)
		}
		started := time.Now()
		attemptID := invocationID
		if p.ID != "" && !options.probe {
			attemptID, err = AdmitDispatch(context.Background(), *p, modelKey, invocationID, started)
			if err != nil {
				return nil, fmt.Errorf("provider %s budget admission: %w", p.Name, err)
			}
		}
		var resp *http.Response
		resp, err = bundle.Invoker.Invoke(context.Background(), provideradapters.Invocation{
			ProviderID: domain.ProviderInstanceID(p.ID), HTTP: httpCli,
			BaseURL: p.BaseURL, APIKey: p.APIKey, Headers: p.Headers,
			Credential: credential, Params: params, Stream: stream,
			Protocol: Protocol(p), ModelID: upstreamModel,
		})
		latency := time.Since(started).Milliseconds()
		if err != nil {
			if p.ID != "" {
				recordProviderOutcome(p.ID, modelKey, false, latency)
			}
			return nil, fmt.Errorf("provider request: %w", err)
		}
		if resp.StatusCode == http.StatusOK {
			if p.ID != "" {
				recordProviderOutcome(p.ID, modelKey, true, latency)
				if !options.probe {
					TagResponseBudgetAttempt(resp, attemptID)
				}
			}
			if conn != nil {
				// Preserve the established rotation behavior for calls and probes.
				connections.AdvanceConnCursor(p, conn.ID)
			}
			connections.TagResponseConn(resp, conn)
			return resp, nil
		}
		if p.ID != "" {
			recordProviderOutcome(p.ID, modelKey, false, latency)
		}
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		resp.Body.Close()
		bodyStr := string(bodyBytes)
		if p.ID != "" {
			observeProviderFailure(bundle, p, credential.ID, modelKey, resp, bodyBytes)
		}
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
		if until := egress.CooldownUntil(resp.StatusCode, resp.Header.Get("Retry-After"), bodyStr); p.ID != "" && !until.IsZero() {
			SetCooldown(p.ID, modelKey, until)
		}
		return nil, egress.NewAPIError(resp.StatusCode, platform.Truncate(bodyStr, 500))
	}
	return nil, fmt.Errorf("provider %s: no connection attempts made", p.Name)
}

func observeProviderFailure(bundle provideradapters.Bundle, provider *CustomProvider, credentialID domain.CredentialID, modelID string, response *http.Response, body []byte) {
	if bundle.FailureClassifier == nil || !providerstate.HasDefault() {
		return
	}
	scope := domain.AvailabilityScope{
		ProviderID: domain.ProviderInstanceID(provider.ID), CredentialID: credentialID, ModelID: modelID,
	}
	observations := bundle.FailureClassifier.ClassifyFailure(provideradapters.FailureInput{
		Scope: scope, StatusCode: response.StatusCode, Header: response.Header, Body: body,
	})
	for _, observation := range observations {
		if err := providerstate.Default().Apply(observation); err != nil {
			log.Printf("provider %s: apply availability observation: %v", provider.Name, err)
		}
	}
}
