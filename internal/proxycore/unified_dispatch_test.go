package proxycore

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"routenest/internal/config"
	"routenest/internal/dispatch"
	"routenest/internal/domain"
	"routenest/internal/provideradapters"
	"routenest/internal/routing"
)

func TestUnifiedDispatchHTTPStatusPrioritizesWrappedAvailability(t *testing.T) {
	errors := []struct {
		name string
		err  error
	}{
		{
			name: "no eligible candidates",
			err:  fmt.Errorf("dispatch failed: %w", &routing.FreeModelUnavailableError{Message: "no eligible provider candidates"}),
		},
		{name: "attempts exhausted", err: fmt.Errorf("all attempts failed: %w", dispatch.ErrExhausted)},
	}
	models := []string{"deepseek-v4-flash-free", "provider/model"}
	for _, tc := range errors {
		for _, model := range models {
			t.Run(tc.name+"/"+model, func(t *testing.T) {
				if got := unifiedDispatchHTTPStatus(model, tc.err); got != http.StatusServiceUnavailable {
					t.Fatalf("unified status = %d, want %d", got, http.StatusServiceUnavailable)
				}
				if got := clineErrorHTTPStatus(tc.err); got != http.StatusServiceUnavailable {
					t.Fatalf("Cline compatibility status = %d, want %d", got, http.StatusServiceUnavailable)
				}
			})
		}
	}
}

type unifiedRetryInvoker struct{}

func (unifiedRetryInvoker) Invoke(_ context.Context, invocation provideradapters.Invocation) (*http.Response, error) {
	status := http.StatusOK
	body := "ok"
	if invocation.ProviderID == "second" {
		status = http.StatusServiceUnavailable
		body = "busy"
	}
	return &http.Response{
		StatusCode: status, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(body)),
	}, nil
}

type unifiedRetryResolver struct{}

func (unifiedRetryResolver) Resolve(_ context.Context, _ dispatch.Request, attempt dispatch.CandidateAttempt) (dispatch.ResolvedAttempt, error) {
	return dispatch.ResolvedAttempt{Invocation: provideradapters.Invocation{
		ProviderID: attempt.ProviderID, ModelID: attempt.UpstreamModelID,
		Credential: provideradapters.Credential{ID: attempt.CredentialID},
	}}, nil
}

func TestAnthropicSemanticCommitCallbackTracksContentNotEnvelope(t *testing.T) {
	run := func(body string) (bool, int) {
		t.Helper()
		response := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}
		recorder := httptest.NewRecorder()
		commits := 0
		handled := HandleAnthropicStream(
			context.Background(), NewSSEWriter(recorder), response, nil,
			&domain.RequestLog{StartedAt: time.Now(), Model: "model"}, time.Second, "", nil,
			func() { commits++ },
		)
		return handled, commits
	}

	handled, commits := run("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\ndata: [DONE]\n\n")
	if handled || commits != 0 {
		t.Fatalf("empty envelope handled=%v commits=%d, want retryable and uncommitted", handled, commits)
	}
	handled, commits = run("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: [DONE]\n\n")
	if !handled || commits != 1 {
		t.Fatalf("content stream handled=%v commits=%d, want terminal and committed once", handled, commits)
	}
}

func TestUnifiedDispatchPlanMetadataPreservesComboAndExclusions(t *testing.T) {
	reqLog := &domain.RequestLog{Model: "combo-id"}
	call := &unifiedDispatchCall{
		reqLog: reqLog,
		diagnostics: routing.PlanDiagnostics{
			RequestedModel: "combo-id",
			Exclusions: []routing.CandidateExclusion{{
				ProviderID: "paid", ModelID: "shared/model", Reason: routing.ExclusionNotFreeEligible,
			}},
		},
	}
	call.applyPlanMetadata(domain.ModelCombo{ID: "combo-id", Name: "Combo Name"}, true)
	if reqLog.RequestedModel != "combo-id" || reqLog.ComboID != "combo-id" || reqLog.ComboName != "Combo Name" {
		t.Fatalf("combo attribution = %+v", reqLog)
	}
	if len(reqLog.PlannerExclusions) != 1 || reqLog.PlannerExclusions[0].ProviderID != "paid" || reqLog.PlannerExclusions[0].Reason != string(routing.ExclusionNotFreeEligible) {
		t.Fatalf("planner exclusions = %+v", reqLog.PlannerExclusions)
	}
}

func TestTraceToAttemptLogPreservesRoutingIdentityAndCompatibleOutcome(t *testing.T) {
	logs := traceToAttemptLog([]dispatch.TraceRecord{{
		Order: 1, Group: 0, ModelID: "logical", UpstreamModelID: "upstream",
		ProviderID: "provider", ProviderName: "Provider", CredentialID: "credential",
		EgressID: "egress", Outcome: "selected",
	}})
	if len(logs) != 1 {
		t.Fatalf("attempt logs = %+v", logs)
	}
	got := logs[0]
	if got.Outcome != "success" || got.DispatchOutcome != "selected" || got.UpstreamModelID != "upstream" || got.CredentialID != "credential" || got.EgressID != "egress" {
		t.Fatalf("attempt projection = %+v", got)
	}
}

func TestUnifiedDispatchRetryAppliesFailedTraceToRequestLog(t *testing.T) {
	registry, err := provideradapters.NewRegistry(provideradapters.Bundle{ID: "fake", Invoker: unifiedRetryInvoker{}})
	if err != nil {
		t.Fatal(err)
	}
	attempts := []dispatch.CandidateAttempt{
		{
			Order: 1, Group: 0, LogicalModelID: "logical", UpstreamModelID: "upstream-first",
			ProviderID: "first", ProviderName: "First", AdapterID: "fake",
			CredentialID: "credential:first", EgressID: "egress:first",
		},
		{
			Order: 2, Group: 1, LogicalModelID: "logical", UpstreamModelID: "upstream-second",
			ProviderID: "second", ProviderName: "Second", AdapterID: "fake",
			CredentialID: "credential:second", EgressID: "egress:second",
		},
	}
	executor := &dispatch.Executor{Registry: registry, Resolver: unifiedRetryResolver{}}
	execution, first, err := executor.Execute(context.Background(), dispatch.Request{}, attempts)
	if err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	first.Response.Body.Close()
	reqLog := &domain.RequestLog{Model: "logical"}
	call := &unifiedDispatchCall{
		mode: config.DispatchModeEnforce, execution: execution,
		params: map[string]any{"model": "logical"}, reqLog: reqLog,
	}
	call.apply(first)
	if _, _, err := call.Retry(context.Background(), "empty_stream"); err == nil {
		t.Fatal("retry unexpectedly succeeded")
	}
	if len(reqLog.Attempts) != 2 || reqLog.FallbackCount != 2 {
		t.Fatalf("failed retry attribution = %+v", reqLog)
	}
	second := reqLog.Attempts[1]
	if second.UpstreamModelID != "upstream-second" || second.Group != 1 || second.CredentialID != "credential:second" || second.EgressID != "egress:second" {
		t.Fatalf("failed attempt routing identity = %+v", second)
	}
	if reqLog.ProviderID != "first" || reqLog.ProviderName != "First" {
		t.Fatalf("failed retry replaced serving attribution: %+v", reqLog)
	}
}
