package routing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/domain"
	"routenest/internal/egress"
	"routenest/internal/platform"
	"routenest/internal/provideradapters"
	"routenest/internal/providerstate"
)

func CallClineAPIWithAccount(acc *domain.Account, params map[string]any, stream bool) (*http.Response, *domain.Account, error) {
	return CallClineAPIWithAccountCtx(context.Background(), acc, params, stream)
}

func CallClineAPIWithAccountCtx(ctx context.Context, acc *domain.Account, params map[string]any, stream bool) (*http.Response, *domain.Account, error) {
	token, err := accounts.EnsureAccountToken(acc)
	if err != nil {

		return nil, acc, &ClineAccountUnavailableError{err: fmt.Errorf("account %s token failed: %w", acc.Email, err)}
	}

	body := BuildUpstreamBody(params, stream)
	sessionID, _ := body["session_id"].(string)

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, acc, fmt.Errorf("marshal body: %w", err)
	}

	bundle, ok := provideradapters.Default().Get("cline")
	if !ok {
		return nil, acc, fmt.Errorf("cline adapter is unavailable")
	}
	httpClient := &http.Client{Transport: egress.Client.Transport}
	credentialID := domain.ClineCredentialID(acc.AccountID, acc.Email)
	invoke := func(accessToken string) (*http.Response, error) {
		return bundle.Invoker.Invoke(ctx, provideradapters.Invocation{
			ProviderID: "cline", HTTP: httpClient, BaseURL: accounts.ClineAPIBase,
			Headers: liveConfig().Headers,
			Credential: provideradapters.Credential{
				ID: credentialID, Token: accessToken, Email: acc.Email,
			},
			Body: bodyJSON, Params: params, Stream: stream,
			ModelID: fmt.Sprintf("%v", body["model"]), SessionID: sessionID,
		})
	}

	toolCount := 0
	if tools, ok := params["tools"]; ok {
		if t, ok := tools.([]any); ok {
			toolCount = len(t)
		}
	}
	log.Printf("  upstream: account=%s stream=%v tools=%d msgs=%d max_tokens=%v effort=%v",
		TruncateEmail(acc.Email), stream, toolCount, getMsgCount(params), body["max_tokens"], body["reasoning_effort"])

	resp, err := invoke(token)
	if err != nil {
		until := time.Now().Add(5 * time.Minute)
		accounts.SetAccountCooldown(acc, until)
		observeClineTransportFailure(credentialID, bodyModel(body), until)
		return nil, acc, &ClineAccountUnavailableError{err: fmt.Errorf("upstream request: %w", err)}
	}

	if resp.StatusCode == 401 {
		resp.Body.Close()

		if err := accounts.RefreshAccountToken(acc); err == nil {
			token = accounts.CurrentAccessToken(acc)
			resp, err = invoke(token)
			if err != nil {
				until := time.Now().Add(5 * time.Minute)
				accounts.SetAccountCooldown(acc, until)
				observeClineTransportFailure(credentialID, bodyModel(body), until)
				return nil, acc, &ClineAccountUnavailableError{err: fmt.Errorf("upstream retry: %w", err)}
			}
			if resp.StatusCode == 401 {
				observeClineFailure(bundle, credentialID, bodyModel(body), resp, nil)
				resp.Body.Close()
				accounts.SetAccountStatus(acc, "expired")
				return nil, acc, &ClineAccountUnavailableError{err: fmt.Errorf("account %s token expired permanently", acc.Email)}
			}
		} else {
			observeClineAccountState(credentialID, bodyModel(body), acc)
			return nil, acc, &ClineAccountUnavailableError{err: fmt.Errorf("account %s refresh failed: %w", acc.Email, err)}
		}
	}

	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		bodyStr := string(bodyBytes)
		observeClineFailure(bundle, credentialID, bodyModel(body), resp, bodyBytes)

		if resp.StatusCode == 429 {
			model, _ := body["model"].(string)
			until := parseCooldownUntil(bodyStr)
			if model != "" {
				SetModelCooldown(acc, model, until)
			} else {
				accounts.SetAccountCooldown(acc, until)
			}
		}

		if model, _ := body["model"].(string); model != "" && IsModelGoneError(resp.StatusCode, bodyStr) {
			MarkModelGone(model)
		}
		return nil, acc, egress.NewAPIError(resp.StatusCode, platform.Truncate(bodyStr, 500))
	}

	accounts.RecordAccountUse(acc, time.Now())
	recoverClineState(credentialID, bodyModel(body))
	return resp, acc, nil
}

func bodyModel(body map[string]any) string {
	model, _ := body["model"].(string)
	return model
}

func observeClineFailure(bundle provideradapters.Bundle, credentialID domain.CredentialID, modelID string, response *http.Response, body []byte) {
	if bundle.FailureClassifier == nil || !providerstate.HasDefault() {
		return
	}
	scope := domain.AvailabilityScope{ProviderID: "cline", CredentialID: credentialID, ModelID: modelID}
	for _, observation := range bundle.FailureClassifier.ClassifyFailure(provideradapters.FailureInput{
		Scope: scope, StatusCode: response.StatusCode, Header: response.Header, Body: body,
	}) {
		if err := providerstate.Default().Apply(observation); err != nil {
			log.Printf("cline: apply availability observation: %v", err)
		}
	}
}

func observeClineTransportFailure(credentialID domain.CredentialID, modelID string, until time.Time) {
	if !providerstate.HasDefault() {
		return
	}
	_ = providerstate.Default().Apply(domain.AvailabilityObservation{
		Kind:  domain.ObservationTransientFailure,
		Scope: domain.AvailabilityScope{ProviderID: "cline", CredentialID: credentialID, ModelID: modelID},
		Until: until, Source: "transport", Confidence: domain.ConfidenceInferred,
	})
}

func observeClineAccountState(credentialID domain.CredentialID, modelID string, account *domain.Account) {
	if !providerstate.HasDefault() || account == nil {
		return
	}
	status, until := accounts.AccountAvailability(account)
	kind := domain.ObservationTransientFailure
	confidence := domain.ConfidenceInferred
	if status == "expired" {
		kind, until, confidence = domain.ObservationAuthExpired, time.Time{}, domain.ConfidenceExplicit
	}
	_ = providerstate.Default().Apply(domain.AvailabilityObservation{
		Kind:  kind,
		Scope: domain.AvailabilityScope{ProviderID: "cline", CredentialID: credentialID, ModelID: modelID},
		Until: until, Source: "token_refresh", Confidence: confidence,
	})
}

func recoverClineState(credentialID domain.CredentialID, modelID string) {
	if !providerstate.HasDefault() {
		return
	}
	for _, scope := range []domain.AvailabilityScope{
		{ProviderID: "cline", CredentialID: credentialID},
		{ProviderID: "cline", CredentialID: credentialID, ModelID: modelID},
	} {
		_ = providerstate.Default().Apply(domain.AvailabilityObservation{
			Kind: domain.ObservationRecovered, Scope: scope,
			Source: "success", Confidence: domain.ConfidenceExplicit,
		})
	}
}
