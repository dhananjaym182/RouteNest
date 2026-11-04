package providers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"routenest/internal/domain"
	"routenest/internal/provideradapters"
	"routenest/internal/providerstate"
)

type recordingInvoker struct {
	inputs    []provideradapters.Invocation
	responses []*http.Response
	errors    []error
}

func (invoker *recordingInvoker) Invoke(_ context.Context, input provideradapters.Invocation) (*http.Response, error) {
	invoker.inputs = append(invoker.inputs, input)
	index := len(invoker.inputs) - 1
	var response *http.Response
	if index < len(invoker.responses) {
		response = invoker.responses[index]
	}
	var err error
	if index < len(invoker.errors) {
		err = invoker.errors[index]
	}
	return response, err
}

type recordingClassifier struct {
	inputs []provideradapters.FailureInput
	now    time.Time
}

func (classifier *recordingClassifier) ClassifyFailure(input provideradapters.FailureInput) []domain.AvailabilityObservation {
	classifier.inputs = append(classifier.inputs, input)
	return []domain.AvailabilityObservation{{
		Kind: domain.ObservationRateLimited, Scope: input.Scope,
		Until: classifier.now.Add(time.Minute), Source: "test", Confidence: domain.ConfidenceExplicit,
	}}
}

func publishAdapterRegistry(t *testing.T, bundles ...provideradapters.Bundle) {
	t.Helper()
	registry, err := provideradapters.NewRegistry(bundles...)
	if err != nil {
		t.Fatal(err)
	}
	if err := provideradapters.Publish(registry); err != nil {
		t.Fatal(err)
	}
}

func adapterResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestCallInvokesProviderAdapterWithCompatibilityInputs(t *testing.T) {
	serverCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		serverCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	invoker := &recordingInvoker{responses: []*http.Response{adapterResponse(http.StatusOK, `{}`)}}
	publishAdapterRegistry(t,
		provideradapters.Bundle{ID: "openaicompat", Invoker: invoker},
		provideradapters.Bundle{ID: "qoder", Invoker: &recordingInvoker{}},
	)

	provider := &CustomProvider{
		ID: "p", Name: "Provider", BaseURL: server.URL, APIKey: "secret",
		Headers: map[string]string{"X-Custom": "value"}, TimeoutSec: 17, ProxyURL: "direct",
	}
	params := map[string]any{"model": "m", "messages": []any{}}
	response, err := Call(provider, params, true)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if serverCalls != 0 {
		t.Fatalf("legacy client made %d direct calls", serverCalls)
	}
	if len(invoker.inputs) != 1 {
		t.Fatalf("adapter invocations = %d", len(invoker.inputs))
	}
	input := invoker.inputs[0]
	if input.ProviderID != "p" || input.BaseURL != server.URL || input.APIKey != "secret" || input.Headers["X-Custom"] != "value" {
		t.Fatalf("invocation identity = %+v", input)
	}
	if input.HTTP == nil || input.HTTP.Timeout != 17*time.Second || input.ModelID != "m" || !input.Stream {
		t.Fatalf("invocation runtime = %+v", input)
	}
	if input.Params["model"] != "m" {
		t.Fatalf("params = %#v", input.Params)
	}
}

func TestCallSelectsQoderAdapterFromProviderDefinition(t *testing.T) {
	openAI := &recordingInvoker{}
	qoder := &recordingInvoker{responses: []*http.Response{adapterResponse(http.StatusOK, `{}`)}}
	publishAdapterRegistry(t,
		provideradapters.Bundle{ID: "openaicompat", Invoker: openAI},
		provideradapters.Bundle{ID: "qoder", Invoker: qoder},
	)

	response, err := Call(&CustomProvider{
		ID: "q", Name: "Qoder", BaseURL: "https://api3.qoder.sh",
	}, map[string]any{"model": "qoder/auto"}, false)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(qoder.inputs) != 1 || len(openAI.inputs) != 0 {
		t.Fatalf("qoder calls=%d openai calls=%d", len(qoder.inputs), len(openAI.inputs))
	}
}

func TestCallClassifiesFailureIntoProviderState(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	store := providerstate.NewStore(func() time.Time { return now })
	if err := providerstate.Publish(store); err != nil {
		t.Fatal(err)
	}
	response := adapterResponse(http.StatusTooManyRequests, `{"error":"rate limit"}`)
	response.Header.Set("Retry-After", "30")
	invoker := &recordingInvoker{responses: []*http.Response{response}}
	classifier := &recordingClassifier{now: now}
	publishAdapterRegistry(t,
		provideradapters.Bundle{ID: "openaicompat", Invoker: invoker, FailureClassifier: classifier},
		provideradapters.Bundle{ID: "qoder", Invoker: &recordingInvoker{}},
	)

	_, err := Call(&CustomProvider{
		ID: "p", Name: "Provider", BaseURL: "https://unknown.example/v1", APIKey: "secret",
	}, map[string]any{"model": "m"}, false)
	if err == nil {
		t.Fatal("429 must surface as an error")
	}
	if len(classifier.inputs) != 1 {
		t.Fatalf("classifier calls = %d", len(classifier.inputs))
	}
	scope := domain.AvailabilityScope{ProviderID: "p", CredentialID: "api-key:p", ModelID: "m"}
	if classifier.inputs[0].Scope != scope || classifier.inputs[0].StatusCode != http.StatusTooManyRequests {
		t.Fatalf("failure input = %+v", classifier.inputs[0])
	}
	if blocked := store.Blocking(scope); len(blocked) != 1 || blocked[0].Kind != domain.ObservationRateLimited {
		t.Fatalf("provider state = %+v", blocked)
	}
}

func TestCallAPIKeepsProviderAttribution(t *testing.T) {
	invoker := &recordingInvoker{responses: []*http.Response{adapterResponse(http.StatusOK, `{}`)}}
	publishAdapterRegistry(t,
		provideradapters.Bundle{ID: "openaicompat", Invoker: invoker},
		provideradapters.Bundle{ID: "qoder", Invoker: &recordingInvoker{}},
	)
	installProviders(t, &CustomProvider{
		ID: "attributed", Name: "Attributed", BaseURL: "https://unknown.example/v1",
		APIKey: "secret", ModelIDs: []string{"m"}, Enabled: true,
	})

	params := map[string]any{"model": "m", "messages": []any{}}
	response, err, attempted, name := CallAPI(params, false)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if !attempted || name != "Attributed" || params[ProviderIDParam] != "attributed" {
		t.Fatalf("attempted=%v name=%q providerID=%v", attempted, name, params[ProviderIDParam])
	}
}
