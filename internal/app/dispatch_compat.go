package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/combos"
	"routenest/internal/config"
	"routenest/internal/connections"
	"routenest/internal/dispatch"
	"routenest/internal/domain"
	"routenest/internal/egress"
	"routenest/internal/provideradapters"
	"routenest/internal/providers"
	"routenest/internal/providerstate"
	"routenest/internal/routing"
	"routenest/internal/zen"
)

type dispatchAttemptResolver struct{}

func dispatchEgressSpec(attempt dispatch.CandidateAttempt) string {
	if attempt.EgressProxyURL != "" {
		return attempt.EgressProxyURL
	}
	if attempt.EgressMode == string(egress.ModeDirect) && attempt.EgressSource == "provider-direct" {
		return "direct"
	}
	return ""
}

func cloneDispatchParams(params map[string]any, model string) map[string]any {
	out := make(map[string]any, len(params)+1)
	for key, value := range params {
		out[key] = value
	}
	out["model"] = model
	delete(out, routing.ServedByParam)
	delete(out, routing.ProviderNameParam)
	delete(out, providers.ProviderIDParam)
	return out
}

func resolveProviderConnection(provider *providers.CustomProvider, credentialID domain.CredentialID) (*connections.Connection, error) {
	if credentialID == "" || strings.HasPrefix(string(credentialID), "api-key:") {
		return nil, nil
	}
	const prefix = "connection:"
	if !strings.HasPrefix(string(credentialID), prefix) {
		return nil, fmt.Errorf("provider %s: unsupported credential %q", provider.Name, credentialID)
	}
	want := strings.TrimPrefix(string(credentialID), prefix)
	for _, connection := range connections.UsableConnectionsForProvider(provider) {
		if connection.ID == want {
			return connection, nil
		}
	}
	return nil, fmt.Errorf("provider %s: credential %q is unavailable", provider.Name, credentialID)
}

func providerCredential(connection *connections.Connection) provideradapters.Credential {
	if connection == nil {
		return provideradapters.Credential{}
	}
	token := connection.AccessToken
	headers := map[string]string{}
	if spec, ok := connections.ProviderDeviceFlows[connection.Provider]; ok {
		token = connections.ConnectionBearer(connection, spec)
		if spec.OrgHeader != "" {
			if orgID := connection.ProviderSpecificData["orgId"]; orgID != "" {
				headers[spec.OrgHeader] = orgID
			}
		}
	}
	metadata := make(map[string]string, len(connection.ProviderSpecificData))
	for key, value := range connection.ProviderSpecificData {
		metadata[key] = value
	}
	return provideradapters.Credential{
		ID: domain.CredentialID("connection:" + connection.ID), Token: token,
		Email: connection.Email, Headers: headers, Metadata: metadata,
	}
}

func resolveCustomAttempt(request dispatch.Request, attempt dispatch.CandidateAttempt) (dispatch.ResolvedAttempt, error) {
	provider := providers.Find(string(attempt.ProviderID))
	if provider == nil || !provider.Enabled {
		return dispatch.ResolvedAttempt{}, fmt.Errorf("provider %q is unavailable", attempt.ProviderID)
	}
	connection, err := resolveProviderConnection(provider, attempt.CredentialID)
	if err != nil {
		return dispatch.ResolvedAttempt{}, err
	}
	transport, err := providers.TransportForDispatch(provider.ID, dispatchEgressSpec(attempt))
	if err != nil {
		return dispatch.ResolvedAttempt{}, err
	}
	timeout := time.Duration(provider.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	params := cloneDispatchParams(request.Params, attempt.UpstreamModelID)
	affinityKey := providers.SessionKeyFromParams(request.Params)
	credential := providerCredential(connection)
	if credential.ID == "" && provider.APIKey != "" {
		credential.ID = domain.CredentialID("api-key:" + provider.ID)
	}
	return dispatch.ResolvedAttempt{
		Invocation: provideradapters.Invocation{
			ProviderID: attempt.ProviderID, HTTP: &http.Client{Transport: transport, Timeout: timeout},
			BaseURL: provider.BaseURL, APIKey: provider.APIKey, Headers: provider.Headers,
			Credential: credential, Params: params, Stream: request.Stream,
			Protocol: providers.Protocol(provider), ModelID: attempt.UpstreamModelID,
			SessionID: request.SessionID,
		},
		ConnectionID: func() string {
			if connection != nil {
				return connection.ID
			}
			return ""
		}(),
		Admit: func(ctx context.Context, event dispatch.InvocationEvent) (string, error) {
			return providers.AdmitDispatch(ctx, *provider, attempt.LogicalModelID, event.ID, event.At)
		},
		TagResponse: func(response *http.Response) {
			if connection != nil {
				connections.TagResponseConn(response, connection)
				connections.AdvanceConnCursor(provider, connection.ID)
			}
		},
		Observe: func(outcome dispatch.AttemptOutcome) {
			if !attempt.Pinned {
				providers.ObserveSessionProvider(affinityKey, attempt.ProviderID, attempt.LogicalModelID, outcome.Success)
			}
			providers.RecordDispatchOutcome(provider.ID, attempt.LogicalModelID, outcome.Success, outcome.Duration.Milliseconds())
			if outcome.Success || outcome.StatusCode == 0 {
				return
			}
			if connection != nil {
				switch outcome.StatusCode {
				case http.StatusUnauthorized, http.StatusForbidden:
					connections.MarkConnectionReauth(connection.ID)
				case http.StatusTooManyRequests:
					connections.SetConnectionCooldown(connection.ID, time.Now().Add(connections.ConnectionRateLimitCooldown))
				}
			}
			if until := egress.CooldownUntil(outcome.StatusCode, outcome.Header.Get("Retry-After"), string(outcome.Body)); !until.IsZero() {
				providers.SetCooldown(provider.ID, attempt.LogicalModelID, until)
			}
		},
	}, nil
}

func resolveClineAttempt(request dispatch.Request, attempt dispatch.CandidateAttempt) (dispatch.ResolvedAttempt, error) {
	account := accounts.GetAccountByCredentialID(attempt.CredentialID)
	if account == nil {
		return dispatch.ResolvedAttempt{}, fmt.Errorf("cline credential %q is unavailable", attempt.CredentialID)
	}
	token, err := accounts.EnsureAccountToken(account)
	if err != nil {
		return dispatch.ResolvedAttempt{}, fmt.Errorf("account %s token failed: %w", account.Email, err)
	}
	params := cloneDispatchParams(request.Params, attempt.UpstreamModelID)
	affinityKey := providers.SessionKeyFromParams(request.Params)
	transport, err := providers.TransportForDispatch("cline", dispatchEgressSpec(attempt))
	if err != nil {
		return dispatch.ResolvedAttempt{}, err
	}
	body := routing.BuildUpstreamBody(params, request.Stream)
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return dispatch.ResolvedAttempt{}, fmt.Errorf("marshal cline body: %w", err)
	}
	sessionID, _ := body["session_id"].(string)
	invocation := provideradapters.Invocation{
		ProviderID: "cline", HTTP: &http.Client{Transport: transport},
		BaseURL: accounts.ClineAPIBase, Headers: configHeaders(),
		Credential: provideradapters.Credential{ID: attempt.CredentialID, Token: token, Email: account.Email},
		Body:       bodyJSON, Params: params, Stream: request.Stream,
		ModelID: attempt.UpstreamModelID, SessionID: sessionID,
	}
	return dispatch.ResolvedAttempt{
		Invocation: invocation,
		Account:    account,
		Refresh: func(_ context.Context, outcome dispatch.AttemptOutcome) (provideradapters.Invocation, bool, error) {
			if outcome.StatusCode != http.StatusUnauthorized {
				return provideradapters.Invocation{}, false, nil
			}
			if err := accounts.RefreshAccountToken(account); err != nil {
				return provideradapters.Invocation{}, false, fmt.Errorf("account %s refresh failed: %w", account.Email, err)
			}
			refreshed := invocation
			refreshed.Credential.Token = account.AccessToken
			return refreshed, true, nil
		},
		Observe: func(outcome dispatch.AttemptOutcome) {
			var affinityErr error
			if !outcome.Success {
				affinityErr = outcome.Err
				if affinityErr == nil {
					affinityErr = egress.NewAPIError(outcome.StatusCode, strings.TrimSpace(string(outcome.Body)))
				}
			}
			accounts.ObserveAffinityOutcome(affinityKey, account, affinityErr)
			if outcome.Success {
				account.LastUsed = time.Now()
				account.UsageCount++
				accounts.SavePool()
				for _, scope := range []domain.AvailabilityScope{
					{ProviderID: "cline", CredentialID: attempt.CredentialID},
					{ProviderID: "cline", CredentialID: attempt.CredentialID, ModelID: attempt.LogicalModelID},
				} {
					_ = providerstate.Default().Apply(domain.AvailabilityObservation{
						Kind: domain.ObservationRecovered, Scope: scope,
						Source: "success", Confidence: domain.ConfidenceExplicit,
					})
				}
				return
			}
			if outcome.Err != nil {
				account.Status = "cooldown"
				account.CooldownUntil = time.Now().Add(5 * time.Minute)
				accounts.SavePool()
				_ = providerstate.Default().Apply(domain.AvailabilityObservation{
					Kind:  domain.ObservationTransientFailure,
					Scope: domain.AvailabilityScope{ProviderID: "cline", CredentialID: attempt.CredentialID, ModelID: attempt.LogicalModelID},
					Until: account.CooldownUntil, Source: "transport", Confidence: domain.ConfidenceInferred,
				})
				return
			}
			if outcome.StatusCode == http.StatusTooManyRequests {
				routing.SetModelCooldown(account, attempt.LogicalModelID, egress.CooldownUntil(outcome.StatusCode, outcome.Header.Get("Retry-After"), string(outcome.Body)))
			}
			if routing.IsModelGoneError(outcome.StatusCode, string(outcome.Body)) {
				routing.MarkModelGone(attempt.LogicalModelID)
			}
		},
	}, nil
}

func configHeaders() map[string]string {
	cfg := config.Live()
	return cfg.Headers
}

func resolveZenAttempt(request dispatch.Request, attempt dispatch.CandidateAttempt) (dispatch.ResolvedAttempt, error) {
	params := cloneDispatchParams(request.Params, attempt.UpstreamModelID)
	preparation, err := zen.PrepareDispatch(params, request.Stream, attempt.CredentialID)
	if err != nil {
		return dispatch.ResolvedAttempt{}, err
	}
	return dispatch.ResolvedAttempt{
		Invocation: preparation.Invocation,
		Acquire:    zen.AcquireDispatchSlot,
		Retry: func(outcome dispatch.AttemptOutcome, failureIndex int) (time.Duration, bool) {
			return zen.DispatchRetryDelay(outcome.StatusCode, outcome.Header, outcome.Body, outcome.Err, failureIndex)
		},
		TransformResponse: preparation.TransformDispatchResponse,
		Observe: func(outcome dispatch.AttemptOutcome) {
			if outcome.Success {
				zen.MarkDispatchSuccess()
				return
			}
			zen.ObserveDispatchFailure(attempt.CredentialID, outcome.StatusCode, outcome.Header, outcome.Body)
			if outcome.StatusCode != 0 && zen.IsModelGoneError(outcome.StatusCode, string(outcome.Body)) {
				zen.MarkModelGone(attempt.LogicalModelID)
			}
		},
	}, nil
}

func (dispatchAttemptResolver) Resolve(_ context.Context, request dispatch.Request, attempt dispatch.CandidateAttempt) (dispatch.ResolvedAttempt, error) {
	switch attempt.ProviderID {
	case "cline":
		return resolveClineAttempt(request, attempt)
	case "opencode":
		return resolveZenAttempt(request, attempt)
	default:
		return resolveCustomAttempt(request, attempt)
	}
}

type legacyDispatchExecutor struct{}

func comboTrace(trace []domain.AttemptLog) []dispatch.TraceRecord {
	out := make([]dispatch.TraceRecord, 0, len(trace))
	for _, attempt := range trace {
		out = append(out, dispatch.TraceRecord{
			Order: attempt.Order, ModelID: attempt.Model, ProviderName: attempt.ProviderName,
			StatusCode: attempt.Status, Outcome: attempt.Outcome, Reason: attempt.Reason,
			DurationMS: attempt.DurationMs,
		})
	}
	return out
}

func legacyResult(requestID string, params map[string]any, response *http.Response, account *domain.Account, trace []dispatch.TraceRecord) dispatch.Result {
	model, _ := params["model"].(string)
	providerID, _ := params[providers.ProviderIDParam].(string)
	providerName, _ := params[routing.ProviderNameParam].(string)
	attempt := dispatch.CandidateAttempt{LogicalModelID: model, UpstreamModelID: model, ProviderName: providerName}
	switch {
	case providerID != "":
		attempt.ProviderID = domain.ProviderInstanceID(providerID)
	case account != nil:
		attempt.ProviderID = "cline"
		attempt.CredentialID = domain.ClineCredentialID(account.AccountID, account.Email)
	default:
		attempt.ProviderID = "opencode"
	}
	connectionID := ""
	if connection := connections.ResponseConnection(response); connection != nil {
		connectionID = connection.ID
	}
	return dispatch.Result{
		RequestID: requestID, AttemptID: providers.ResponseBudgetAttempt(response),
		Response: response, Serving: attempt, Account: account, ConnectionID: connectionID, Trace: trace,
	}
}

func (legacyDispatchExecutor) ExecuteLegacy(_ context.Context, request dispatch.Request) (dispatch.Result, error) {
	params := request.Params
	model, _ := params["model"].(string)
	if combo, ok := combos.FindCombo(model); ok {
		run := combos.RunCombo(params, combo, request.Stream, request.SessionID)
		if run.Err != nil {
			return legacyResult(request.RequestID, params, run.Resp, run.Acc, comboTrace(run.Trace)), run.Err
		}
		return legacyResult(request.RequestID, params, run.Resp, run.Acc, comboTrace(run.Trace)), nil
	}
	switch zen.RouteModel(model) {
	case "reject":
		return dispatch.Result{}, fmt.Errorf("model %q is a paid opencode model; only free models are proxied", model)
	case "zen":
		response, err := zen.Call(params, request.Stream)
		if err == nil {
			return legacyResult(request.RequestID, params, response, nil, nil), nil
		}
		if fallback, account, fallbackErr, attempted := routing.ZenFailoverToCline(params, request.Stream); attempted {
			if fallbackErr == nil {
				return legacyResult(request.RequestID, params, fallback, account, nil), nil
			}
			return legacyResult(request.RequestID, params, fallback, account, nil), fmt.Errorf("zen: %v; cline free chain: %w", err, fallbackErr)
		}
		return legacyResult(request.RequestID, params, response, nil, nil), err
	default:
		response, account, err := routing.CallClineAPI(params, request.Stream)
		if err != nil {
			return legacyResult(request.RequestID, params, response, account, nil), err
		}
		return legacyResult(request.RequestID, params, response, account, nil), nil
	}
}

func applyDispatchRuleCooldown(failure dispatch.Failure) {
	until := egress.RuleCooldownUntil(failure.StatusCode)
	attempt := failure.Attempt
	switch attempt.ProviderID {
	case "cline":
		if account := accounts.GetAccountByCredentialID(attempt.CredentialID); account != nil {
			routing.SetModelCooldown(account, attempt.LogicalModelID, until)
		}
	case "opencode":
		_ = providerstate.Default().Apply(domain.AvailabilityObservation{
			Kind: domain.ObservationTransientFailure,
			Scope: domain.AvailabilityScope{
				ProviderID: attempt.ProviderID, CredentialID: attempt.CredentialID,
				ModelID: attempt.LogicalModelID, EgressID: attempt.EgressID,
			},
			Until: until, Source: "request_error_rule", Confidence: domain.ConfidenceExplicit,
		})
	default:
		providers.SetCooldown(string(attempt.ProviderID), attempt.LogicalModelID, until)
	}
}

func dispatchFailurePolicy(failure dispatch.Failure) dispatch.FailureClass {
	baseline := dispatch.DefaultFailureClass(failure)
	if failure.StatusCode == 0 {
		return baseline
	}
	action, matched := config.Live().MatchRequestError(failure.StatusCode, strings.TrimSpace(string(failure.Body)))
	if !matched {
		return baseline
	}
	switch action {
	case config.ErrorActionStopAndCooldown:
		applyDispatchRuleCooldown(failure)
		return dispatch.FailureDeterministic
	case config.ErrorActionStop:
		return dispatch.FailureDeterministic
	case config.ErrorActionContinueAndCooldown:
		applyDispatchRuleCooldown(failure)
		return dispatch.FailureRetryable
	case config.ErrorActionContinue:
		return dispatch.FailureRetryable
	default:
		return baseline
	}
}

func configureDispatch() error {
	executor := &dispatch.Executor{
		Registry: provideradapters.Default(), Resolver: dispatchAttemptResolver{},
		Observe: providerstate.Default().Apply, Policy: dispatchFailurePolicy,
	}
	return dispatch.PublishRuntime(&dispatch.Runtime{Executor: executor, Legacy: legacyDispatchExecutor{}})
}
