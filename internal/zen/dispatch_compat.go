package zen

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"routenest/internal/domain"
	"routenest/internal/provideradapters"
)

// DispatchPreparation is the provider-specific wire preparation consumed by
// the provider-neutral executor during the Phase 2 compatibility window.
type DispatchPreparation struct {
	Invocation       provideradapters.Invocation
	Anonymous        bool
	UsesResponses    bool
	DownstreamStream bool
	ModelID          string
}

// AcquireDispatchSlot preserves the legacy OpenCode concurrency cap for the
// provider-neutral executor. Capacity is released once response headers (or an
// invocation error) return, matching Call's historical stream behavior.
func AcquireDispatchSlot(ctx context.Context) (func(), error) {
	StateMu.Lock()
	sem := zenSem
	if sem == nil {
		rebuildZenSemLocked()
		sem = zenSem
	}
	StateMu.Unlock()
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// DispatchRetryDelay preserves bounded Zen same-attempt retries and backoff.
// Multiple registered sessions are flattened into distinct executor attempts,
// so rate limits advance immediately instead of replaying one session.
func DispatchRetryDelay(outcomeStatus int, header http.Header, body []byte, err error, failureIndex int) (time.Duration, bool) {
	cfg := GetConfig()
	retries := cfg.Retries
	if retries <= 0 {
		retries = 3
	}
	if failureIndex >= retries || errors.Is(err, context.Canceled) {
		return 0, false
	}
	delay := time.Second << min(failureIndex, 6)
	if err != nil {
		return withRetryJitter(delay), true
	}
	if outcomeStatus == http.StatusInternalServerError || outcomeStatus == http.StatusBadGateway || outcomeStatus == http.StatusGatewayTimeout {
		return withRetryJitter(delay), true
	}
	if !isRateLimited(outcomeStatus, string(body)) {
		return 0, false
	}
	if len(cfg.SessionIDs) > 1 {
		return 0, false
	}
	retryAfter := parseRetryAfter(header.Get("Retry-After"))
	if retryAfter > zenMaxRetryWait {
		return 0, false
	}
	if retryAfter > 0 {
		return withRetryJitter(retryAfter), true
	}
	return withRetryJitter(delay), true
}

// ObserveDispatchFailure mirrors the legacy proxy cooldown side effect once a
// rate-limited Zen attempt cannot rotate to another registered session.
func ObserveDispatchFailure(credentialID domain.CredentialID, status int, header http.Header, body []byte) {
	if !isRateLimited(status, string(body)) {
		return
	}
	cfg := GetConfig()
	if strings.HasPrefix(string(credentialID), "opencode:session:") && len(cfg.SessionIDs) > 1 {
		return
	}
	idx := lastZenProxyIdx()
	if idx < 0 {
		return
	}
	delay := parseRetryAfter(header.Get("Retry-After"))
	if delay <= 0 {
		delay = 10 * time.Minute
	}
	if delay > zenMaxProxyCooldown {
		delay = zenMaxProxyCooldown
	}
	cooldownZenProxy(idx, delay)
}

func sessionForCredential(credentialID domain.CredentialID, sessions []string) string {
	for _, session := range sessions {
		if SessionCredentialID(session) == credentialID {
			return session
		}
	}
	return ""
}

// PrepareDispatch builds exactly one adapter invocation. Retry and failover are
// owned by internal/dispatch; this function performs no upstream request.
func PrepareDispatch(params map[string]any, stream bool, credentialID domain.CredentialID) (DispatchPreparation, error) {
	cfg := GetConfig()
	apiKey := zenAPIKeyFor(params, cfg.Key)
	anonymous := apiKey == "public"
	usesResponses := zenUpstreamNeedsResponses(params)
	protocol := "chat"
	var body map[string]any
	if usesResponses {
		protocol = "responses"
		body = buildZenResponsesBody(params)
	} else {
		body = buildZenBody(params, stream, anonymous)
	}
	bodyJSON, err := marshalZenJSON(body)
	if err != nil {
		return DispatchPreparation{}, fmt.Errorf("marshal zen body: %w", err)
	}
	modelID := zenUpstreamModelID(params)
	sessionID := sessionForCredential(credentialID, cfg.SessionIDs)
	if sessionID == "" {
		sessionID = zenSessionID(params)
	}
	return DispatchPreparation{
		Invocation: provideradapters.Invocation{
			ProviderID: "opencode", HTTP: getZenHTTPClient(), BaseURL: cfg.BaseURL,
			APIKey: apiKey, Headers: cfg.ZenHeaders,
			Credential: provideradapters.Credential{ID: credentialID},
			Body:       bodyJSON, Params: params, Stream: stream, Protocol: protocol,
			ModelID: modelID, SessionID: sessionID, SessionIDs: append([]string(nil), cfg.SessionIDs...),
		},
		Anonymous: anonymous, UsesResponses: usesResponses,
		DownstreamStream: stream, ModelID: modelID,
	}, nil
}

// TransformDispatchResponse preserves Zen's historical Responses-to-chat and
// anonymous-stream collapsing behavior after the shared executor receives 200.
func (preparation DispatchPreparation) TransformDispatchResponse(response *http.Response) (*http.Response, error) {
	if response == nil {
		return nil, fmt.Errorf("zen response is nil")
	}
	if preparation.UsesResponses {
		if preparation.DownstreamStream {
			return streamZenResponsesAsChat(response, preparation.ModelID, context.CancelFunc(func() {})), nil
		}
		return collapseZenResponsesStream(response, preparation.ModelID, context.CancelFunc(func() {}))
	}
	if preparation.Anonymous && !preparation.DownstreamStream {
		return collapseZenStreamResponse(response, preparation.ModelID)
	}
	return response, nil
}

// MarkDispatchSuccess updates the existing Zen health window after a unified
// executor attempt succeeds.
func MarkDispatchSuccess() { markZenSuccess() }
