package dispatch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"routenest/internal/provideradapters"
)

type resolverFunc func(context.Context, Request, CandidateAttempt) (ResolvedAttempt, error)

func (fn resolverFunc) Resolve(ctx context.Context, request Request, attempt CandidateAttempt) (ResolvedAttempt, error) {
	return fn(ctx, request, attempt)
}

func TestExecutorIdentifiesEveryRefreshRetryAndFallbackInvocation(t *testing.T) {
	now := time.Date(2026, 11, 7, 1, 2, 3, 0, time.UTC)
	var events []InvocationEvent
	var outcomes []AttemptOutcome
	calls, acquired, released, generated := 0, 0, 0, 0
	invoker := fakeInvoker(func(_ context.Context, invocation provideradapters.Invocation) (*http.Response, error) {
		calls++
		if len(events) != calls || acquired != calls {
			t.Fatalf("Invoke occurred before acquire/admission: calls=%d events=%d acquired=%d", calls, len(events), acquired)
		}
		if calls == 1 {
			return response(http.StatusUnauthorized, "expired"), nil
		}
		if invocation.ProviderID == "first" {
			if invocation.APIKey != "fresh" {
				t.Fatal("refresh invocation lost its credential")
			}
			return response(http.StatusServiceUnavailable, "busy"), nil
		}
		return response(http.StatusOK, "ok"), nil
	})
	resolver := resolverFunc(func(_ context.Context, request Request, candidate CandidateAttempt) (ResolvedAttempt, error) {
		if request.RequestID != "server-request" {
			t.Fatalf("request identity = %q", request.RequestID)
		}
		resolved, _ := (fakeResolver{refresh: true}).Resolve(context.Background(), request, candidate)
		resolved.Acquire = func(context.Context) (func(), error) {
			acquired++
			return func() { released++ }, nil
		}
		resolved.Admit = func(ctx context.Context, event InvocationEvent) (string, error) {
			if ctx.Err() != nil || acquired != len(events)+1 || released != len(events) {
				t.Fatal("admission did not occur inside the acquired capacity reservation")
			}
			if event.RequestID != request.RequestID || event.Attempt != candidate || !event.At.Equal(now) {
				t.Fatalf("event = %+v", event)
			}
			events = append(events, event)
			return "receipt-" + event.ID, nil
		}
		refresh := resolved.Refresh
		resolved.Refresh = func(ctx context.Context, outcome AttemptOutcome) (provideradapters.Invocation, bool, error) {
			outcomes = append(outcomes, outcome)
			return refresh(ctx, outcome)
		}
		resolved.Observe = func(outcome AttemptOutcome) { outcomes = append(outcomes, outcome) }
		resolved.Retry = func(outcome AttemptOutcome, failureIndex int) (time.Duration, bool) {
			return 0, failureIndex == 1
		}
		return resolved, nil
	})
	executor := &Executor{
		Registry: fakeRegistry(t, invoker, nil), Resolver: resolver,
		Now:   func() time.Time { return now },
		NewID: func() (string, error) { generated++; return fmt.Sprintf("invocation-%d", generated), nil },
	}
	_, result, err := executor.Execute(context.Background(), Request{RequestID: "server-request"}, attempts("first", "second"))
	if err != nil {
		t.Fatal(err)
	}
	defer result.Response.Body.Close()
	if calls != 4 || generated != calls || released != calls || len(events) != calls {
		t.Fatalf("calls=%d generated=%d released=%d events=%d", calls, generated, released, len(events))
	}
	if result.Serving.ProviderID != "second" || result.RequestID != "server-request" ||
		result.InvocationID != "invocation-4" || result.AttemptID != "receipt-invocation-4" {
		t.Fatalf("result = %+v", result)
	}
	seen := make(map[string]bool)
	for _, event := range events {
		if event.ID == "" || seen[event.ID] {
			t.Fatalf("invocation identity reused: %q", event.ID)
		}
		seen[event.ID] = true
	}
	if len(outcomes) != calls {
		t.Fatalf("outcomes=%d calls=%d", len(outcomes), calls)
	}
	for _, outcome := range outcomes {
		if !seen[outcome.InvocationID] || outcome.AttemptID != "receipt-"+outcome.InvocationID {
			t.Fatalf("outcome lost admission identity: %+v", outcome)
		}
	}
}

func TestExecutorDoesNotAdmitBeforeInvocationPrerequisites(t *testing.T) {
	for _, scenario := range []string{"resolve error", "acquire error", "already cancelled", "cancel during acquire"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "already cancelled" {
				cancel()
			}
			generated, admitted, invoked, observed, acquired, released := 0, 0, 0, 0, 0, 0
			resolver := resolverFunc(func(context.Context, Request, CandidateAttempt) (ResolvedAttempt, error) {
				if scenario == "resolve error" {
					return ResolvedAttempt{}, errors.New("credential unavailable")
				}
				return ResolvedAttempt{
					Acquire: func(context.Context) (func(), error) {
						acquired++
						if scenario == "acquire error" {
							return nil, errors.New("capacity unavailable")
						}
						cancel()
						return func() { released++ }, nil
					},
					Admit:   func(context.Context, InvocationEvent) (string, error) { admitted++; return "receipt", nil },
					Observe: func(AttemptOutcome) { observed++ },
				}, nil
			})
			invoker := fakeInvoker(func(context.Context, provideradapters.Invocation) (*http.Response, error) {
				invoked++
				return response(http.StatusOK, "unexpected"), nil
			})
			executor := &Executor{
				Registry: fakeRegistry(t, invoker, nil), Resolver: resolver,
				NewID: func() (string, error) { generated++; return "unexpected", nil },
			}
			_, _, err := executor.Execute(ctx, Request{RequestID: "server-request"}, attempts("first"))
			if err == nil || generated != 0 || admitted != 0 || invoked != 0 || observed != 0 {
				t.Fatalf("err=%v generated=%d admitted=%d invoked=%d observed=%d", err, generated, admitted, invoked, observed)
			}
			if scenario == "cancel during acquire" && (acquired != 1 || released != 1) {
				t.Fatalf("acquired=%d released=%d", acquired, released)
			}
			if scenario == "already cancelled" && acquired != 0 {
				t.Fatalf("cancelled request acquired capacity: %d", acquired)
			}
		})
	}
}

func TestExecutorAdmissionErrorsUseFailurePolicyBeforeFallback(t *testing.T) {
	for _, class := range []FailureClass{FailureRetryable, FailureDeterministic} {
		t.Run(string(class), func(t *testing.T) {
			admissionErr := errors.New("budget unavailable")
			admitted, invoked, released, classified, observed := 0, 0, 0, 0, 0
			invoker := fakeInvoker(func(_ context.Context, invocation provideradapters.Invocation) (*http.Response, error) {
				invoked++
				if invocation.ProviderID != "second" {
					t.Fatal("rejected admission reached the upstream")
				}
				return response(http.StatusOK, "ok"), nil
			})
			resolver := resolverFunc(func(ctx context.Context, request Request, candidate CandidateAttempt) (ResolvedAttempt, error) {
				resolved, _ := (fakeResolver{}).Resolve(ctx, request, candidate)
				resolved.Acquire = func(context.Context) (func(), error) { return func() { released++ }, nil }
				resolved.Admit = func(context.Context, InvocationEvent) (string, error) {
					admitted++
					if candidate.ProviderID == "first" {
						return "", admissionErr
					}
					return "accepted", nil
				}
				resolved.Observe = func(AttemptOutcome) { observed++ }
				return resolved, nil
			})
			executor := &Executor{
				Registry: fakeRegistry(t, invoker, nil), Resolver: resolver,
				Policy: func(failure Failure) FailureClass {
					classified++
					if !errors.Is(failure.Err, admissionErr) || failure.Attempt.ProviderID != "first" {
						t.Fatalf("failure = %+v", failure)
					}
					return class
				},
			}
			_, result, err := executor.Execute(context.Background(), Request{}, attempts("first", "second"))
			if classified != 1 {
				t.Fatalf("classified=%d", classified)
			}
			if class == FailureDeterministic {
				if !errors.Is(err, admissionErr) || invoked != 0 || admitted != 1 || released != 1 || observed != 0 {
					t.Fatalf("err=%v invoked=%d admitted=%d released=%d observed=%d", err, invoked, admitted, released, observed)
				}
				return
			}
			if err != nil || invoked != 1 || admitted != 2 || released != 2 || observed != 1 || result.AttemptID != "accepted" {
				t.Fatalf("err=%v invoked=%d admitted=%d released=%d observed=%d result=%+v", err, invoked, admitted, released, observed, result)
			}
			result.Response.Body.Close()
		})
	}
}

func TestExecutorInvocationIdentitiesRemainUniqueAcrossExecutions(t *testing.T) {
	const executions = 64
	var mu sync.Mutex
	seen := make(map[string]bool)
	resolver := resolverFunc(func(ctx context.Context, request Request, candidate CandidateAttempt) (ResolvedAttempt, error) {
		resolved, _ := (fakeResolver{}).Resolve(ctx, request, candidate)
		resolved.Admit = func(_ context.Context, event InvocationEvent) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			if event.RequestID != "one-server-request" || event.ID == "" || seen[event.ID] {
				return "", fmt.Errorf("invalid or reused identity: %+v", event)
			}
			seen[event.ID] = true
			return event.ID, nil
		}
		return resolved, nil
	})
	invoker := fakeInvoker(func(context.Context, provideradapters.Invocation) (*http.Response, error) {
		return response(http.StatusOK, "ok"), nil
	})
	executor := &Executor{Registry: fakeRegistry(t, invoker, nil), Resolver: resolver}
	errorsByExecution := make(chan error, executions)
	for range executions {
		go func() {
			_, result, err := executor.Execute(context.Background(), Request{RequestID: "one-server-request"}, attempts("first"))
			if err == nil {
				result.Response.Body.Close()
				if result.AttemptID != result.InvocationID {
					err = fmt.Errorf("receipt identity lost: %+v", result)
				}
			}
			errorsByExecution <- err
		}()
	}
	for range executions {
		if err := <-errorsByExecution; err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != executions {
		t.Fatalf("distinct identities=%d executions=%d", len(seen), executions)
	}
}

func TestExecutionCreatesNewIdentityForEmptyStreamFallback(t *testing.T) {
	invoker := fakeInvoker(func(context.Context, provideradapters.Invocation) (*http.Response, error) {
		return response(http.StatusOK, ""), nil
	})
	executor := &Executor{Registry: fakeRegistry(t, invoker, nil), Resolver: fakeResolver{}}
	execution, first, err := executor.Execute(context.Background(), Request{Stream: true}, attempts("first", "second"))
	if err != nil {
		t.Fatal(err)
	}
	first.Response.Body.Close()
	second, err := execution.Next(context.Background(), "empty_stream")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Response.Body.Close()
	if first.RequestID == "" || second.RequestID != first.RequestID || first.InvocationID == "" ||
		second.InvocationID == first.InvocationID || first.AttemptID != first.InvocationID || second.AttemptID != second.InvocationID {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

func TestExecutorGeneratesRequestIdentityBeforeResolution(t *testing.T) {
	var received Request
	resolver := resolverFunc(func(ctx context.Context, request Request, candidate CandidateAttempt) (ResolvedAttempt, error) {
		received = request
		return (fakeResolver{}).Resolve(ctx, request, candidate)
	})
	invoker := fakeInvoker(func(context.Context, provideradapters.Invocation) (*http.Response, error) {
		return response(http.StatusOK, "ok"), nil
	})
	generated := 0
	executor := &Executor{
		Registry: fakeRegistry(t, invoker, nil), Resolver: resolver,
		NewID: func() (string, error) { generated++; return fmt.Sprintf("server-%d", generated), nil },
	}
	_, result, err := executor.Execute(context.Background(), Request{}, attempts("first"))
	if err != nil {
		t.Fatal(err)
	}
	defer result.Response.Body.Close()
	if received.RequestID != "server-1" || result.RequestID != received.RequestID || result.InvocationID != "server-2" || generated != 2 {
		t.Fatalf("received=%+v result=%+v generated=%d", received, result, generated)
	}
}
