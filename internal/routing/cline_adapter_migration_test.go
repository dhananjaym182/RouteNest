package routing

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/domain"
	"routenest/internal/egress"
	"routenest/internal/modelcatalog"
	"routenest/internal/provideradapters"
	clineadapter "routenest/internal/provideradapters/cline"
	"routenest/internal/providerstate"
)

type clineRecordingInvoker struct {
	inputs    []provideradapters.Invocation
	responses []*http.Response
}

type clineRecordingDiscoverer struct {
	inputs  []provideradapters.DiscoveryInput
	entries []modelcatalog.Entry
}

func (discoverer *clineRecordingDiscoverer) DiscoverModels(_ context.Context, input provideradapters.DiscoveryInput) ([]modelcatalog.Entry, error) {
	discoverer.inputs = append(discoverer.inputs, input)
	return append([]modelcatalog.Entry(nil), discoverer.entries...), nil
}

func (invoker *clineRecordingInvoker) Invoke(_ context.Context, input provideradapters.Invocation) (*http.Response, error) {
	invoker.inputs = append(invoker.inputs, input)
	index := len(invoker.inputs) - 1
	if index >= len(invoker.responses) {
		return nil, nil
	}
	return invoker.responses[index], nil
}

func clineAdapterResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func publishClineTestAdapter(t *testing.T, bundle provideradapters.Bundle) {
	t.Helper()
	old := provideradapters.Default()
	registry, err := provideradapters.NewRegistry(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := provideradapters.Publish(registry); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provideradapters.Publish(old) })
}

func installClineAccountPool(t *testing.T, account *domain.Account) {
	t.Helper()
	accounts.PoolMu.Lock()
	oldPool, oldPath := accounts.Pool, accounts.PoolPath
	accounts.PoolPath = t.TempDir() + "/.routenest-accounts.json"
	accounts.Pool = &domain.AccountPool{Accounts: []*domain.Account{account}}
	accounts.PoolMu.Unlock()
	t.Cleanup(func() {
		accounts.PoolMu.Lock()
		accounts.Pool, accounts.PoolPath = oldPool, oldPath
		accounts.PoolMu.Unlock()
	})
}

func TestCallClineAccountInvokesProviderAdapter(t *testing.T) {
	directCalls := 0
	oldTransport := egress.Client.Transport
	egress.Client.Transport = clineRoundTripFunc(func(*http.Request) (*http.Response, error) {
		directCalls++
		return clineAdapterResponse(http.StatusOK, `{}`), nil
	})
	t.Cleanup(func() { egress.Client.Transport = oldTransport })

	invoker := &clineRecordingInvoker{responses: []*http.Response{clineAdapterResponse(http.StatusOK, `{}`)}}
	publishClineTestAdapter(t, provideradapters.Bundle{ID: "cline", Invoker: invoker})
	account := &domain.Account{
		AccountID: "a1", Email: "a@example.com", AccessToken: "token",
		ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), Status: "active",
	}
	installClineAccountPool(t, account)
	params := map[string]any{
		"model": "model", "session_id": "session-1", "messages": []any{},
	}

	response, used, err := CallClineAPIWithAccount(account, params, true)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if used != account {
		t.Fatalf("used account = %p, want %p", used, account)
	}
	if directCalls != 0 {
		t.Fatalf("legacy direct calls = %d", directCalls)
	}
	if len(invoker.inputs) != 1 {
		t.Fatalf("adapter calls = %d", len(invoker.inputs))
	}
	input := invoker.inputs[0]
	if input.ProviderID != "cline" || input.Credential.ID != "cline:a1" || input.Credential.Token != "token" {
		t.Fatalf("credential invocation = %+v", input)
	}
	if input.ModelID != "model" || input.SessionID == "" || !input.Stream || len(input.Body) == 0 {
		t.Fatalf("request invocation = %+v", input)
	}
}

func TestCallClineAccountProjectsQuotaObservation(t *testing.T) {
	now := time.Now()
	oldTransport := egress.Client.Transport
	egress.Client.Transport = clineRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return clineAdapterResponse(http.StatusTooManyRequests, `{"error":"quota","message":"Try again in 1h 1m"}`), nil
	})
	t.Cleanup(func() { egress.Client.Transport = oldTransport })
	store := providerstate.NewStore(func() time.Time { return now })
	if err := providerstate.Publish(store); err != nil {
		t.Fatal(err)
	}
	invoker := &clineRecordingInvoker{responses: []*http.Response{
		clineAdapterResponse(http.StatusTooManyRequests, `{"error":"quota","message":"Try again in 1h 1m"}`),
	}}
	classifier := clineadapter.New()
	publishClineTestAdapter(t, provideradapters.Bundle{
		ID: "cline", Invoker: invoker, FailureClassifier: classifier,
	})
	account := &domain.Account{
		AccountID: "quota", Email: "quota@example.com", AccessToken: "token",
		ExpiresAt: now.Add(time.Hour).UnixMilli(), Status: "active",
	}
	installClineAccountPool(t, account)

	_, _, err := CallClineAPIWithAccount(account, map[string]any{"model": "model"}, false)
	if err == nil {
		t.Fatal("429 must fail")
	}
	scope := domain.AvailabilityScope{ProviderID: "cline", CredentialID: "cline:quota", ModelID: "model"}
	blocked := store.Blocking(scope)
	foundQuota := false
	for _, observation := range blocked {
		if observation.Kind == domain.ObservationQuotaExhausted && observation.Confidence == domain.ConfidenceExplicit && !observation.ResetAt.IsZero() {
			foundQuota = true
		}
	}
	if !foundQuota {
		t.Fatalf("quota observation missing: %+v", blocked)
	}
	if until := account.ModelCooldowns["model"]; until.Before(now.Add(60*time.Minute)) || until.After(now.Add(62*time.Minute)) {
		t.Fatalf("legacy model cooldown = %s", until)
	}
}

func TestClineModelSyncUsesProviderDiscoverer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"free":[{"id":"legacy-direct"}]}`))
	}))
	defer server.Close()
	oldURL := clineRecommendedModelsURL
	clineRecommendedModelsURL = server.URL
	t.Cleanup(func() { clineRecommendedModelsURL = oldURL })

	accounts.PoolMu.Lock()
	oldPool, oldPath := accounts.Pool, accounts.PoolPath
	accounts.PoolPath = t.TempDir() + "/.routenest-accounts.json"
	accounts.Pool = &domain.AccountPool{}
	accounts.PoolMu.Unlock()
	t.Cleanup(func() {
		accounts.PoolMu.Lock()
		accounts.Pool, accounts.PoolPath = oldPool, oldPath
		accounts.PoolMu.Unlock()
	})

	discoverer := &clineRecordingDiscoverer{entries: []modelcatalog.Entry{{ID: "adapter-model", FreeFlag: true}}}
	publishClineTestAdapter(t, provideradapters.Bundle{
		ID: "cline", Invoker: &clineRecordingInvoker{}, ModelDiscoverer: discoverer,
	})
	result := syncClineModels()
	if result.Error != "" {
		t.Fatalf("sync error: %s", result.Error)
	}
	if len(discoverer.inputs) != 1 || discoverer.inputs[0].BaseURL != server.URL {
		t.Fatalf("discovery inputs = %+v", discoverer.inputs)
	}
	models := accounts.Pool.Models
	if len(models) != 1 || models[0].ID != "adapter-model" || models[0].Cost != "free" {
		t.Fatalf("synced models = %+v", models)
	}
}

type clineRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn clineRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
