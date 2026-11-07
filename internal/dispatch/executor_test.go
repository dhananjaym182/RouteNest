package dispatch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"routenest/internal/domain"
	"routenest/internal/provideradapters"
)

type fakeInvoker func(context.Context, provideradapters.Invocation) (*http.Response, error)

func (fn fakeInvoker) Invoke(ctx context.Context, invocation provideradapters.Invocation) (*http.Response, error) {
	return fn(ctx, invocation)
}

type fakeResolver struct {
	refresh bool
	acquire AcquireAttempt
	retry   RetryAttempt
}

func (r fakeResolver) Resolve(_ context.Context, _ Request, attempt CandidateAttempt) (ResolvedAttempt, error) {
	resolved := ResolvedAttempt{Invocation: provideradapters.Invocation{
		ProviderID: attempt.ProviderID, ModelID: attempt.UpstreamModelID,
		Credential: provideradapters.Credential{ID: attempt.CredentialID},
	}, Acquire: r.acquire, Retry: r.retry}
	if r.refresh {
		resolved.Refresh = func(_ context.Context, outcome AttemptOutcome) (provideradapters.Invocation, bool, error) {
			if outcome.StatusCode != http.StatusUnauthorized {
				return provideradapters.Invocation{}, false, nil
			}
			invocation := resolved.Invocation
			invocation.APIKey = "fresh"
			return invocation, true, nil
		}
	}
	return resolved, nil
}

func fakeRegistry(t *testing.T, invoker provideradapters.Invoker, classifier provideradapters.FailureClassifier) *provideradapters.Registry {
	t.Helper()
	registry, err := provideradapters.NewRegistry(provideradapters.Bundle{ID: "fake", Invoker: invoker, FailureClassifier: classifier})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func attempts(ids ...string) []CandidateAttempt {
	out := make([]CandidateAttempt, len(ids))
	for i, id := range ids {
		out[i] = CandidateAttempt{
			Order: i + 1, Group: i, ProviderID: domain.ProviderInstanceID(id), ProviderName: id,
			AdapterID: "fake", LogicalModelID: "logical", UpstreamModelID: "upstream-" + id,
		}
	}
	return out
}

func TestExecutorRetriesRetryableStatuses(t *testing.T) {
	for _, status := range []int{401, 402, 403, 429, 500, 502, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			invoker := fakeInvoker(func(_ context.Context, invocation provideradapters.Invocation) (*http.Response, error) {
				if invocation.ProviderID == "first" {
					return response(status, "retry"), nil
				}
				return response(http.StatusOK, "ok"), nil
			})
			executor := &Executor{Registry: fakeRegistry(t, invoker, nil), Resolver: fakeResolver{}}
			_, result, err := executor.Execute(context.Background(), Request{}, attempts("first", "second"))
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if result.Serving.ProviderID != "second" || len(result.Trace) != 2 || !result.Trace[0].Retryable {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestExecutorStopsOnDeterministicRequestError(t *testing.T) {
	calls := 0
	invoker := fakeInvoker(func(context.Context, provideradapters.Invocation) (*http.Response, error) {
		calls++
		return response(http.StatusBadRequest, "invalid payload"), nil
	})
	executor := &Executor{Registry: fakeRegistry(t, invoker, nil), Resolver: fakeResolver{}}
	_, result, err := executor.Execute(context.Background(), Request{}, attempts("first", "second"))
	if err == nil || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	if len(result.Trace) != 1 || result.Trace[0].Retryable {
		t.Fatalf("trace = %+v", result.Trace)
	}
}

func TestExecutorRefreshesCredentialOnceAfter401(t *testing.T) {
	calls := 0
	invoker := fakeInvoker(func(_ context.Context, invocation provideradapters.Invocation) (*http.Response, error) {
		calls++
		if invocation.APIKey != "fresh" {
			return response(http.StatusUnauthorized, "expired"), nil
		}
		return response(http.StatusOK, "ok"), nil
	})
	executor := &Executor{Registry: fakeRegistry(t, invoker, nil), Resolver: fakeResolver{refresh: true}}
	_, result, err := executor.Execute(context.Background(), Request{}, attempts("first"))
	if err != nil || calls != 2 || result.Serving.ProviderID != "first" {
		t.Fatalf("err=%v calls=%d result=%+v", err, calls, result)
	}
	if len(result.Trace) != 2 || result.Trace[0].Outcome != "credential_refresh" || result.Trace[1].Outcome != "selected" {
		t.Fatalf("trace = %+v", result.Trace)
	}
}

func TestExecutorAppliesProviderConcurrencyAndSameAttemptRetry(t *testing.T) {
	calls, acquired, released := 0, 0, 0
	invoker := fakeInvoker(func(context.Context, provideradapters.Invocation) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(http.StatusServiceUnavailable, "busy"), nil
		}
		return response(http.StatusOK, "ok"), nil
	})
	resolver := fakeResolver{
		acquire: func(context.Context) (func(), error) {
			acquired++
			return func() { released++ }, nil
		},
		retry: func(AttemptOutcome, int) (time.Duration, bool) { return 0, calls == 1 },
	}
	executor := &Executor{Registry: fakeRegistry(t, invoker, nil), Resolver: resolver}
	_, result, err := executor.Execute(context.Background(), Request{}, attempts("first"))
	if err != nil || calls != 2 || acquired != 2 || released != 2 {
		t.Fatalf("err=%v calls=%d acquired=%d released=%d result=%+v", err, calls, acquired, released, result)
	}
	if len(result.Trace) != 2 || result.Trace[0].Outcome != "attempt_retry" || result.Trace[1].Outcome != "selected" {
		t.Fatalf("trace = %+v", result.Trace)
	}
}

type modelGoneClassifier struct{}

func (modelGoneClassifier) ClassifyFailure(in provideradapters.FailureInput) []domain.AvailabilityObservation {
	return []domain.AvailabilityObservation{{
		Kind: domain.ObservationModelGone, Scope: in.Scope,
		Source: "test", Confidence: domain.ConfidenceExplicit,
	}}
}

func TestExecutorRetriesModelGoneEvenWhenStatusIs400(t *testing.T) {
	invoker := fakeInvoker(func(_ context.Context, invocation provideradapters.Invocation) (*http.Response, error) {
		if invocation.ProviderID == "first" {
			return response(http.StatusBadRequest, "model not found"), nil
		}
		return response(http.StatusOK, "ok"), nil
	})
	observed := 0
	executor := &Executor{
		Registry: fakeRegistry(t, invoker, modelGoneClassifier{}), Resolver: fakeResolver{},
		Observe: func(domain.AvailabilityObservation) error { observed++; return nil },
	}
	_, result, err := executor.Execute(context.Background(), Request{}, attempts("first", "second"))
	if err != nil || result.Serving.ProviderID != "second" || observed != 1 {
		t.Fatalf("err=%v result=%+v observed=%d", err, result, observed)
	}
}

func TestExecutorRetriesTransportTimeoutButStopsOnCancellation(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		invoker := fakeInvoker(func(_ context.Context, invocation provideradapters.Invocation) (*http.Response, error) {
			if invocation.ProviderID == "first" {
				return nil, context.DeadlineExceeded
			}
			return response(http.StatusOK, "ok"), nil
		})
		executor := &Executor{Registry: fakeRegistry(t, invoker, nil), Resolver: fakeResolver{}}
		_, result, err := executor.Execute(context.Background(), Request{}, attempts("first", "second"))
		if err != nil || result.Serving.ProviderID != "second" {
			t.Fatalf("err=%v result=%+v", err, result)
		}
	})

	t.Run("cancel", func(t *testing.T) {
		calls := 0
		invoker := fakeInvoker(func(ctx context.Context, _ provideradapters.Invocation) (*http.Response, error) {
			calls++
			<-ctx.Done()
			return nil, ctx.Err()
		})
		executor := &Executor{Registry: fakeRegistry(t, invoker, nil), Resolver: fakeResolver{}}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := executor.Execute(ctx, Request{}, attempts("first", "second"))
		if !errors.Is(err, context.Canceled) || calls != 0 {
			t.Fatalf("err=%v calls=%d", err, calls)
		}
	})
}

func TestExecutionRetriesEmptyStreamBeforeCommit(t *testing.T) {
	invoker := fakeInvoker(func(_ context.Context, invocation provideradapters.Invocation) (*http.Response, error) {
		return response(http.StatusOK, string(invocation.ProviderID)), nil
	})
	executor := &Executor{Registry: fakeRegistry(t, invoker, nil), Resolver: fakeResolver{}}
	execution, first, err := executor.Execute(context.Background(), Request{Stream: true}, attempts("first", "second"))
	if err != nil || first.Serving.ProviderID != "first" {
		t.Fatalf("first err=%v result=%+v", err, first)
	}
	first.Response.Body.Close()
	second, err := execution.Next(context.Background(), "empty_stream")
	if err != nil || second.Serving.ProviderID != "second" || second.FallbackCount != 1 {
		t.Fatalf("second err=%v result=%+v", err, second)
	}
	if second.Trace[0].Outcome != "empty_stream" {
		t.Fatalf("trace = %+v", second.Trace)
	}
}

func TestExecutionNeverFallsBackAfterSemanticCommit(t *testing.T) {
	invoker := fakeInvoker(func(_ context.Context, invocation provideradapters.Invocation) (*http.Response, error) {
		return response(http.StatusOK, string(invocation.ProviderID)), nil
	})
	executor := &Executor{Registry: fakeRegistry(t, invoker, nil), Resolver: fakeResolver{}}
	execution, first, err := executor.Execute(context.Background(), Request{Stream: true}, attempts("first", "second"))
	if err != nil {
		t.Fatal(err)
	}
	first.Response.Body.Close()
	execution.MarkCommitted()
	if _, err := execution.Next(context.Background(), "empty_stream"); !errors.Is(err, ErrStreamCommitted) {
		t.Fatalf("err = %v", err)
	}
}

func TestExecutorReturnsExhaustedAfterRetryableFailures(t *testing.T) {
	invoker := fakeInvoker(func(context.Context, provideradapters.Invocation) (*http.Response, error) {
		return nil, errors.New("network down")
	})
	executor := &Executor{Registry: fakeRegistry(t, invoker, nil), Resolver: fakeResolver{}, Now: func() time.Time { return time.Unix(1, 0) }}
	_, result, err := executor.Execute(context.Background(), Request{}, attempts("first", "second"))
	if !errors.Is(err, ErrExhausted) || len(result.Trace) != 2 {
		t.Fatalf("err=%v result=%+v", err, result)
	}
}
