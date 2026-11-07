package proxycore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/config"
	"routenest/internal/credentials"
	"routenest/internal/dispatch"
	"routenest/internal/domain"
	"routenest/internal/egress"
	"routenest/internal/provideradapters"
	clineadapter "routenest/internal/provideradapters/cline"
	"routenest/internal/providers"
	"routenest/internal/providerstate"
	"routenest/internal/routing"
)

type Account = domain.Account
type AccountPool = domain.AccountPool
type RequestLog = domain.RequestLog
type tokenUsage = TokenUsage

type freeModelRoundTripper func(*http.Request) (*http.Response, error)

func (f freeModelRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type proxycoreTestEgressSource string

func (source proxycoreTestEgressSource) ProfileID() string { return string(source) }

func (source proxycoreTestEgressSource) Snapshot() egress.Profile {
	return egress.Profile{ID: string(source), Name: string(source)}
}

type proxycoreTestAttemptResolver struct{}

func cloneProxycoreTestParams(params map[string]any, model string) map[string]any {
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

func (proxycoreTestAttemptResolver) Resolve(_ context.Context, request dispatch.Request, attempt dispatch.CandidateAttempt) (dispatch.ResolvedAttempt, error) {
	if attempt.ProviderID != "cline" {
		return dispatch.ResolvedAttempt{}, fmt.Errorf("proxycore test resolver: unsupported provider %q", attempt.ProviderID)
	}
	account := accounts.GetAccountByCredentialID(attempt.CredentialID)
	if account == nil {
		return dispatch.ResolvedAttempt{}, fmt.Errorf("proxycore test resolver: unavailable credential %q", attempt.CredentialID)
	}
	token, err := accounts.EnsureAccountToken(account)
	if err != nil {
		return dispatch.ResolvedAttempt{}, err
	}
	params := cloneProxycoreTestParams(request.Params, attempt.UpstreamModelID)
	body, err := json.Marshal(routing.BuildUpstreamBody(params, request.Stream))
	if err != nil {
		return dispatch.ResolvedAttempt{}, err
	}
	invocation := provideradapters.Invocation{
		ProviderID: "cline",
		HTTP:       &http.Client{Transport: egress.Client.Transport},
		BaseURL:    accounts.ClineAPIBase,
		Headers:    getProxyConfig().Headers,
		Credential: provideradapters.Credential{
			ID: attempt.CredentialID, Token: token, Email: account.Email,
		},
		Body: body, Params: params, Stream: request.Stream,
		ModelID: attempt.UpstreamModelID, SessionID: request.SessionID,
	}
	return dispatch.ResolvedAttempt{
		Invocation: invocation,
		Account:    account,
		Refresh: func(_ context.Context, outcome dispatch.AttemptOutcome) (provideradapters.Invocation, bool, error) {
			if outcome.StatusCode != http.StatusUnauthorized {
				return provideradapters.Invocation{}, false, nil
			}
			if err := accounts.RefreshAccountToken(account); err != nil {
				return provideradapters.Invocation{}, false, err
			}
			refreshed := invocation
			refreshed.Credential.Token = account.AccessToken
			return refreshed, true, nil
		},
	}, nil
}

type proxycoreTestLegacyExecutor struct{}

func (proxycoreTestLegacyExecutor) ExecuteLegacy(_ context.Context, request dispatch.Request) (dispatch.Result, error) {
	response, account, err := routing.CallClineAPI(request.Params, request.Stream)
	model, _ := request.Params["model"].(string)
	serving := dispatch.CandidateAttempt{
		LogicalModelID:  model,
		UpstreamModelID: model,
		ProviderID:      "cline",
		AdapterID:       "cline",
	}
	if account != nil {
		serving.CredentialID = domain.ClineCredentialID(account.AccountID, account.Email)
	}
	return dispatch.Result{Response: response, Serving: serving, Account: account}, err
}

var testProxyConfig = config.DefaultProxy()

func defaultProxyConfig() *config.Proxy { return config.DefaultProxy() }

func getProxyConfig() *config.Proxy { return testProxyConfig }

func setProxyConfig(cfg *config.Proxy) {
	testProxyConfig = cfg
	provider := func() *config.Proxy { return testProxyConfig }
	ConfigProvider = provider
	routing.ConfigProvider = provider
	egress.ConfigProvider = provider
	providers.ConfigProvider = provider
}

func init() {
	setProxyConfig(testProxyConfig)
	// Usage attribution is a composed hook in production; tests keep a no-op.
	RecordUsage = func(*domain.Account, *http.Response, *domain.RequestLog, string, TokenUsage) {}
	state := providerstate.NewStore(time.Now)
	if err := providerstate.Publish(state); err != nil {
		panic(err)
	}
	credentialRegistry, err := credentials.NewRegistry(state, credentials.AccountsSource{})
	if err != nil {
		panic(err)
	}
	if err := credentials.Publish(credentialRegistry); err != nil {
		panic(err)
	}
	egressRegistry, err := egress.NewRegistry(
		"global",
		proxycoreTestEgressSource("global"),
		proxycoreTestEgressSource("zen"),
	)
	if err != nil {
		panic(err)
	}
	egressResolver, err := egress.NewResolver(egressRegistry, state)
	if err != nil {
		panic(err)
	}
	egressResolver.SetEnvironmentProxy(func(*url.URL) (*url.URL, error) { return nil, nil })
	if err := egress.Publish(egressResolver); err != nil {
		panic(err)
	}
	// The real composition root installs the adapter registry; this package
	// cannot import app (app imports proxycore), so install the Cline bundle
	// the dispatch path needs.
	adapter := clineadapter.New()
	registry, err := provideradapters.NewRegistry(provideradapters.Bundle{
		ID: "cline", Invoker: adapter, FailureClassifier: adapter,
	})
	if err != nil {
		panic(err)
	}
	if err := provideradapters.Publish(registry); err != nil {
		panic(err)
	}
	executor := &dispatch.Executor{Registry: registry, Resolver: proxycoreTestAttemptResolver{}, Observe: state.Apply}
	if err := dispatch.PublishRuntime(&dispatch.Runtime{Executor: executor, Legacy: proxycoreTestLegacyExecutor{}}); err != nil {
		panic(err)
	}
}
