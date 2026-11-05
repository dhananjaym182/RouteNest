package zen

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"routenest/internal/domain"
	"routenest/internal/modelcatalog"
	"routenest/internal/provideradapters"
	opencodezenadapter "routenest/internal/provideradapters/opencodezen"
	"routenest/internal/providerstate"
)

type zenRecordingInvoker struct {
	inputs    []provideradapters.Invocation
	responses []*http.Response
}

type zenRecordingDiscoverer struct {
	inputs  []provideradapters.DiscoveryInput
	entries []modelcatalog.Entry
}

func (discoverer *zenRecordingDiscoverer) DiscoverModels(_ context.Context, input provideradapters.DiscoveryInput) ([]modelcatalog.Entry, error) {
	discoverer.inputs = append(discoverer.inputs, input)
	return append([]modelcatalog.Entry(nil), discoverer.entries...), nil
}

func (invoker *zenRecordingInvoker) Invoke(_ context.Context, input provideradapters.Invocation) (*http.Response, error) {
	invoker.inputs = append(invoker.inputs, input)
	index := len(invoker.inputs) - 1
	if index >= len(invoker.responses) {
		return nil, nil
	}
	return invoker.responses[index], nil
}

func zenAdapterResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func publishZenTestAdapter(t *testing.T, bundle provideradapters.Bundle) {
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

func installZenAdapterCallState(t *testing.T, client *http.Client) {
	t.Helper()
	ConfigMu.Lock()
	oldConfig := Config
	Config = &ConfigData{
		Enabled: true, BaseURL: "https://opencode.test/inference/v1", Key: "sk-real",
		Retries: 1, SessionIDs: []string{"ses_aaaaaaaaaaaaAAAAAAAAAAAAAA"},
	}
	ConfigMu.Unlock()
	TransportMu.Lock()
	oldClient := HTTPClient
	HTTPClient = client
	TransportMu.Unlock()
	zenPoolMu.Lock()
	oldStamp := zenPoolStamp
	zenPoolStamp = time.Time{}
	zenPoolMu.Unlock()
	oldSessions := zenSessions.PoolSessions()
	RemoteEnabledMu.Lock()
	oldRemoteEnabled := RemoteEnabled
	RemoteEnabled = false
	RemoteEnabledMu.Unlock()
	StateMu.Lock()
	oldFailCount, oldFailUntil := FailCount, FailUntil
	FailCount, FailUntil = 0, time.Time{}
	StateMu.Unlock()
	t.Cleanup(func() {
		ConfigMu.Lock()
		Config = oldConfig
		ConfigMu.Unlock()
		TransportMu.Lock()
		HTTPClient = oldClient
		TransportMu.Unlock()
		zenPoolMu.Lock()
		zenPoolStamp = oldStamp
		zenPoolMu.Unlock()
		zenSessions.SetPool(oldSessions)
		RemoteEnabledMu.Lock()
		RemoteEnabled = oldRemoteEnabled
		RemoteEnabledMu.Unlock()
		StateMu.Lock()
		FailCount, FailUntil = oldFailCount, oldFailUntil
		StateMu.Unlock()
	})
}

func TestCallInvokesOpenCodeProviderAdapter(t *testing.T) {
	directCalls := 0
	client := &http.Client{Transport: zenRoundTripFunc(func(*http.Request) (*http.Response, error) {
		directCalls++
		return zenAdapterResponse(http.StatusOK, `{}`), nil
	})}
	installZenAdapterCallState(t, client)
	invoker := &zenRecordingInvoker{responses: []*http.Response{zenAdapterResponse(http.StatusOK, `{}`)}}
	publishZenTestAdapter(t, provideradapters.Bundle{ID: "opencodezen", Invoker: invoker})

	response, err := Call(map[string]any{"model": "paid/model", "messages": []any{}}, true)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if directCalls != 0 {
		t.Fatalf("legacy direct calls = %d", directCalls)
	}
	if len(invoker.inputs) != 1 {
		t.Fatalf("adapter calls = %d", len(invoker.inputs))
	}
	input := invoker.inputs[0]
	if input.ProviderID != "opencode" || input.APIKey != "sk-real" || input.Params["model"] != "paid/model" {
		t.Fatalf("invocation = %+v", input)
	}
	if input.HTTP != client || input.SessionID == "" || input.Credential.ID != "opencode:public" {
		t.Fatalf("runtime identity = %+v", input)
	}
}

func TestCallProjectsClassifiedFailureIntoProviderState(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	store := providerstate.NewStore(func() time.Time { return now })
	if err := providerstate.Publish(store); err != nil {
		t.Fatal(err)
	}
	installZenAdapterCallState(t, &http.Client{Transport: zenRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return zenAdapterResponse(http.StatusUnauthorized, `{"error":"expired"}`), nil
	})})
	invoker := &zenRecordingInvoker{responses: []*http.Response{zenAdapterResponse(http.StatusUnauthorized, `{"error":"expired"}`)}}
	classifier := opencodezenadapter.New()
	publishZenTestAdapter(t, provideradapters.Bundle{
		ID: "opencodezen", Invoker: invoker, FailureClassifier: classifier,
	})

	_, err := Call(map[string]any{"model": "paid/model", "messages": []any{}}, false)
	if err == nil {
		t.Fatal("401 must fail")
	}
	scope := domain.AvailabilityScope{ProviderID: "opencode", CredentialID: "opencode:public", ModelID: "paid/model"}
	blocked := store.Blocking(scope)
	if len(blocked) != 1 || blocked[0].Kind != domain.ObservationAuthExpired {
		t.Fatalf("provider state = %+v", blocked)
	}
}

func TestCallRotatesRegisteredSessionsThroughAdapter(t *testing.T) {
	installZenAdapterCallState(t, &http.Client{Transport: zenRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return zenAdapterResponse(http.StatusInternalServerError, `{}`), nil
	})})
	const firstSession = "ses_aaaaaaaaaaaaAAAAAAAAAAAAAA"
	const secondSession = "ses_bbbbbbbbbbbbBBBBBBBBBBBBBB"
	ConfigMu.Lock()
	Config.Key = "sk-paid-but-free-must-use-public"
	Config.SessionIDs = []string{firstSession, secondSession}
	ConfigMu.Unlock()
	zenPoolMu.Lock()
	zenPoolStamp = time.Time{}
	zenPoolMu.Unlock()

	invoker := &zenRecordingInvoker{responses: []*http.Response{
		zenAdapterResponse(http.StatusServiceUnavailable, `{"error":"busy"}`),
		zenAdapterResponse(http.StatusOK, `{}`),
	}}
	publishZenTestAdapter(t, provideradapters.Bundle{ID: "opencodezen", Invoker: invoker})

	response, err := Call(map[string]any{"model": "mimo-v2.6-flash-free", "messages": []any{}}, true)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(invoker.inputs) != 2 {
		t.Fatalf("adapter calls = %d", len(invoker.inputs))
	}
	first, second := invoker.inputs[0], invoker.inputs[1]
	if first.APIKey != "public" || second.APIKey != "public" {
		t.Fatalf("free model keys = %q, %q", first.APIKey, second.APIKey)
	}
	if first.SessionID != firstSession || second.SessionID != secondSession {
		t.Fatalf("sessions = %q then %q", first.SessionID, second.SessionID)
	}
	if first.Credential.ID != SessionCredentialID(firstSession) || second.Credential.ID != SessionCredentialID(secondSession) {
		t.Fatalf("credential IDs = %q then %q", first.Credential.ID, second.Credential.ID)
	}
}

func TestSyncModelsUsesOpenCodeDiscoverer(t *testing.T) {
	installZenSync(t, `{"data":[{"id":"legacy-direct"}]}`, nil)
	discoverer := &zenRecordingDiscoverer{entries: []modelcatalog.Entry{{ID: "adapter-model-free"}}}
	publishZenTestAdapter(t, provideradapters.Bundle{
		ID: "opencodezen", Invoker: &zenRecordingInvoker{}, ModelDiscoverer: discoverer,
	})

	result := SyncModels()
	if result.Error != "" {
		t.Fatalf("sync error: %s", result.Error)
	}
	if len(discoverer.inputs) != 1 {
		t.Fatalf("discoverer calls = %d", len(discoverer.inputs))
	}
	if discoverer.inputs[0].BaseURL != "https://zen.test/v1" || discoverer.inputs[0].APIKey != "public" {
		t.Fatalf("discovery input = %+v", discoverer.inputs[0])
	}
	models := zenRecords(t)
	if _, ok := models["adapter-model-free"]; !ok {
		t.Fatalf("adapter model missing: %+v", models)
	}
	if _, ok := models["legacy-direct"]; ok {
		t.Fatalf("legacy direct catalogue was used: %+v", models)
	}
}

type zenRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn zenRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
