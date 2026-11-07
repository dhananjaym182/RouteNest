package dispatch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"routenest/internal/domain"
	"routenest/internal/provideradapters"
)

const maxFailureBody = 64 << 10

type Executor struct {
	Registry *provideradapters.Registry
	Resolver AttemptResolver
	Observe  func(domain.AvailabilityObservation) error
	Policy   FailurePolicy
	Now      func() time.Time
	NewID    IDGenerator
}

type Execution struct {
	executor *Executor
	request  Request
	attempts []CandidateAttempt

	mu            sync.Mutex
	next          int
	trace         []TraceRecord
	lastSelected  int
	fallbackCount int
	commit        StreamCommitState
}

func (e *Executor) NewExecution(request Request, attempts []CandidateAttempt) (*Execution, error) {
	if e == nil || e.Registry == nil || e.Resolver == nil {
		return nil, fmt.Errorf("dispatch executor is not configured")
	}
	if request.RequestID == "" {
		var err error
		request.RequestID, err = e.newID()
		if err != nil {
			return nil, fmt.Errorf("dispatch request identity: %w", err)
		}
	}
	copyAttempts := append([]CandidateAttempt(nil), attempts...)
	return &Execution{executor: e, request: request, attempts: copyAttempts, lastSelected: -1}, nil
}

// GenerateID uses cryptographic randomness so independent processes and
// replicas can create identities without sharing a counter or local clock.
func GenerateID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", fmt.Errorf("dispatch identity generation failed: %w", err)
	}
	return hex.EncodeToString(id[:]), nil
}

func (e *Executor) newID() (string, error) {
	generator := e.NewID
	if generator == nil {
		generator = GenerateID
	}
	id, err := generator()
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("dispatch identity generator returned an empty ID")
	}
	return id, nil
}

func (e *Executor) Execute(ctx context.Context, request Request, attempts []CandidateAttempt) (*Execution, Result, error) {
	execution, err := e.NewExecution(request, attempts)
	if err != nil {
		return nil, Result{}, err
	}
	result, err := execution.Next(ctx, "")
	return execution, result, err
}

func (e *Executor) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// DefaultFailureClass applies the provider-neutral retry baseline. Composition
// roots may wrap it with live operator policy without duplicating the status
// and availability-observation rules.
func DefaultFailureClass(failure Failure) FailureClass {
	if failure.Err != nil {
		if failure.Err == context.Canceled {
			return FailureCancelled
		}
		return FailureRetryable
	}
	for _, observation := range failure.Observations {
		if observation.Kind == domain.ObservationModelGone || observation.Kind == domain.ObservationAuthExpired ||
			observation.Kind == domain.ObservationQuotaExhausted || observation.Kind == domain.ObservationRateLimited ||
			observation.Kind == domain.ObservationTransientFailure {
			return FailureRetryable
		}
	}
	switch failure.StatusCode {
	case http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden,
		http.StatusNotFound, http.StatusRequestTimeout, http.StatusConflict,
		http.StatusTooEarly, http.StatusTooManyRequests:
		return FailureRetryable
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return FailureDeterministic
	}
	if failure.StatusCode >= 500 {
		return FailureRetryable
	}
	if failure.StatusCode >= 400 {
		return FailureDeterministic
	}
	return FailureRetryable
}

func (e *Executor) classify(failure Failure) FailureClass {
	if e.Policy != nil {
		return e.Policy(failure)
	}
	return DefaultFailureClass(failure)
}

func cloneHeader(header http.Header) http.Header {
	if header == nil {
		return nil
	}
	return header.Clone()
}

func boundedFailure(response *http.Response) ([]byte, error) {
	if response == nil || response.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxFailureBody))
	response.Body.Close()
	return body, err
}

func attemptScope(attempt CandidateAttempt) domain.AvailabilityScope {
	return domain.AvailabilityScope{
		ProviderID: attempt.ProviderID, CredentialID: attempt.CredentialID,
		ModelID: attempt.LogicalModelID, EgressID: attempt.EgressID,
	}
}

func (x *Execution) observations(bundle provideradapters.Bundle, attempt CandidateAttempt, response *http.Response, body []byte) []domain.AvailabilityObservation {
	if bundle.FailureClassifier == nil || response == nil {
		return nil
	}
	observations := bundle.FailureClassifier.ClassifyFailure(provideradapters.FailureInput{
		Scope: attemptScope(attempt), StatusCode: response.StatusCode,
		Header: response.Header, Body: body,
	})
	if x.executor.Observe != nil {
		for _, observation := range observations {
			_ = x.executor.Observe(observation)
		}
	}
	return observations
}

func traceReason(err error, body []byte) string {
	if err != nil {
		return err.Error()
	}
	return strings.TrimSpace(string(body))
}

func (x *Execution) appendTrace(attempt CandidateAttempt, outcome AttemptOutcome, result string, retryable bool) {
	x.trace = append(x.trace, TraceRecord{
		Order: attempt.Order, Group: attempt.Group,
		ModelID: attempt.LogicalModelID, UpstreamModelID: attempt.UpstreamModelID,
		ProviderID: attempt.ProviderID, ProviderName: attempt.ProviderName,
		CredentialID: attempt.CredentialID, EgressID: attempt.EgressID,
		StatusCode: outcome.StatusCode, Outcome: result,
		Reason: traceReason(outcome.Err, outcome.Body), DurationMS: outcome.Duration.Milliseconds(),
		Retryable: retryable,
	})
}

func (x *Execution) invoke(ctx context.Context, bundle provideradapters.Bundle, resolved ResolvedAttempt, attempt CandidateAttempt, invocation provideradapters.Invocation) (*http.Response, AttemptOutcome) {
	started := x.executor.now()
	outcome := AttemptOutcome{Attempt: attempt, StartedAt: started}
	fail := func(err error) (*http.Response, AttemptOutcome) {
		outcome.Duration = x.executor.now().Sub(started)
		outcome.Err = err
		return nil, outcome
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	release := func() {}
	if resolved.Acquire != nil {
		var err error
		release, err = resolved.Acquire(ctx)
		if err != nil {
			return fail(err)
		}
		if release == nil {
			release = func() {}
		}
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	id, err := x.executor.newID()
	if err != nil {
		return fail(err)
	}
	outcome.InvocationID = id
	attemptID := id
	if resolved.Admit != nil {
		attemptID, err = resolved.Admit(ctx, InvocationEvent{
			ID: id, RequestID: x.request.RequestID, Attempt: attempt, At: x.executor.now(),
		})
		if err != nil {
			return fail(err)
		}
	}
	outcome.AttemptID = attemptID
	response, err := bundle.Invoker.Invoke(ctx, invocation)
	outcome.Duration = x.executor.now().Sub(started)
	outcome.Err = err
	if response != nil {
		outcome.StatusCode = response.StatusCode
		outcome.Header = cloneHeader(response.Header)
	}
	if err == nil && response != nil && response.StatusCode == http.StatusOK {
		outcome.Success = true
	}
	if resolved.Observe != nil && outcome.Err != nil {
		resolved.Observe(outcome)
	}
	return response, outcome
}

func waitRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (x *Execution) attempt(ctx context.Context, attempt CandidateAttempt) (Result, FailureClass, error) {
	bundle, ok := x.executor.Registry.Get(attempt.AdapterID)
	if !ok {
		return Result{}, FailureDeterministic, fmt.Errorf("provider %q references unavailable adapter %q", attempt.ProviderID, attempt.AdapterID)
	}
	resolved, err := x.executor.Resolver.Resolve(ctx, x.request, attempt)
	if err != nil {
		outcome := AttemptOutcome{Attempt: attempt, StartedAt: x.executor.now(), Err: err}
		class := x.executor.classify(Failure{Attempt: attempt, Err: err})
		if ctx.Err() != nil {
			class = FailureCancelled
		}
		x.appendTrace(attempt, outcome, "resolve_error", class == FailureRetryable)
		return Result{}, class, err
	}
	invocation := resolved.Invocation
	for replay := 0; ; replay++ {
		response, outcome := x.invoke(ctx, bundle, resolved, attempt, invocation)
		if outcome.Success {
			if resolved.TransformResponse != nil {
				transformed, transformErr := resolved.TransformResponse(response)
				if transformErr != nil {
					if response != nil && response.Body != nil {
						response.Body.Close()
					}
					outcome.Success = false
					outcome.Err = transformErr
					if resolved.Observe != nil {
						resolved.Observe(outcome)
					}
					class := x.executor.classify(Failure{Attempt: attempt, Err: transformErr})
					x.appendTrace(attempt, outcome, "response_transform_error", class == FailureRetryable)
					return Result{}, class, transformErr
				}
				response = transformed
			}
			if resolved.TagResponse != nil {
				resolved.TagResponse(response)
			}
			if resolved.Observe != nil {
				resolved.Observe(outcome)
			}
			x.appendTrace(attempt, outcome, "selected", false)
			x.lastSelected = len(x.trace) - 1
			return Result{
				RequestID: x.request.RequestID, InvocationID: outcome.InvocationID, AttemptID: outcome.AttemptID,
				Response: response, Serving: attempt, Account: resolved.Account,
				ConnectionID: resolved.ConnectionID,
			}, "", nil
		}
		if outcome.Err != nil {
			class := x.executor.classify(Failure{Attempt: attempt, Err: outcome.Err})
			if ctx.Err() != nil {
				class = FailureCancelled
			}
			if class == FailureRetryable && resolved.Retry != nil {
				if delay, retry := resolved.Retry(outcome, replay); retry {
					x.appendTrace(attempt, outcome, "attempt_retry", true)
					if err := waitRetry(ctx, delay); err != nil {
						return Result{}, FailureCancelled, err
					}
					continue
				}
			}
			x.appendTrace(attempt, outcome, "transport_error", class == FailureRetryable)
			return Result{}, class, outcome.Err
		}
		body, readErr := boundedFailure(response)
		outcome.Body = body
		if readErr != nil {
			outcome.Err = readErr
		}
		if replay == 0 && resolved.Refresh != nil {
			refreshed, retry, refreshErr := resolved.Refresh(ctx, outcome)
			if refreshErr != nil {
				outcome.Err = refreshErr
				x.appendTrace(attempt, outcome, "refresh_error", true)
				return Result{}, FailureRetryable, refreshErr
			}
			if retry {
				x.appendTrace(attempt, outcome, "credential_refresh", true)
				invocation = refreshed
				continue
			}
		}
		observations := x.observations(bundle, attempt, response, body)
		if resolved.Observe != nil {
			resolved.Observe(outcome)
		}
		failure := Failure{Attempt: attempt, StatusCode: outcome.StatusCode, Body: body, Err: outcome.Err, Observations: observations}
		class := x.executor.classify(failure)
		if class == FailureRetryable && resolved.Retry != nil {
			if delay, retry := resolved.Retry(outcome, replay); retry {
				x.appendTrace(attempt, outcome, "attempt_retry", true)
				if err := waitRetry(ctx, delay); err != nil {
					return Result{}, FailureCancelled, err
				}
				continue
			}
		}
		x.appendTrace(attempt, outcome, "upstream_error", class == FailureRetryable)
		return Result{}, class, fmt.Errorf("upstream %s returned status %d: %s", attempt.ProviderID, outcome.StatusCode, strings.TrimSpace(string(body)))
	}
}

func (x *Execution) Next(ctx context.Context, priorOutcome string) (Result, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.commit.Committed() {
		return Result{}, ErrStreamCommitted
	}
	if priorOutcome != "" && x.lastSelected >= 0 && x.lastSelected < len(x.trace) {
		x.trace[x.lastSelected].Outcome = priorOutcome
		x.trace[x.lastSelected].Retryable = true
		x.lastSelected = -1
		x.fallbackCount++
	}
	var lastErr error
	for x.next < len(x.attempts) {
		attempt := x.attempts[x.next]
		x.next++
		result, class, err := x.attempt(ctx, attempt)
		if err == nil {
			result.Trace = append([]TraceRecord(nil), x.trace...)
			result.FallbackCount = x.fallbackCount
			return result, nil
		}
		lastErr = err
		if class == FailureCancelled || class == FailureDeterministic {
			return Result{Trace: append([]TraceRecord(nil), x.trace...), FallbackCount: x.fallbackCount}, err
		}
		x.fallbackCount++
	}
	if lastErr == nil {
		lastErr = ErrExhausted
	} else {
		lastErr = fmt.Errorf("%w: %v", ErrExhausted, lastErr)
	}
	return Result{Trace: append([]TraceRecord(nil), x.trace...), FallbackCount: x.fallbackCount}, lastErr
}

func (x *Execution) MarkCommitted()  { x.commit.MarkCommitted() }
func (x *Execution) Committed() bool { return x.commit.Committed() }

func (x *Execution) Trace() []TraceRecord {
	x.mu.Lock()
	defer x.mu.Unlock()
	return append([]TraceRecord(nil), x.trace...)
}
