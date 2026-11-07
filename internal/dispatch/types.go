package dispatch

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"routenest/internal/domain"
	"routenest/internal/provideradapters"
)

var (
	ErrExhausted       = errors.New("dispatch candidates exhausted")
	ErrStreamCommitted = errors.New("dispatch fallback blocked after semantic stream output")
)

// Request is the provider-neutral input shared by every ingress protocol.
// Protocol conversion and downstream response formatting remain with callers.
type Request struct {
	// RequestID is assigned by the server and retained across new executions of
	// the same request, including context-truncation retries.
	RequestID      string
	RequestedModel string
	Params         map[string]any
	Stream         bool
	SessionID      string
}

// CandidateAttempt is one flattened provider/credential attempt produced from
// a routing plan. Logical and upstream model IDs are intentionally distinct.
type CandidateAttempt struct {
	Order           int
	Group           int
	LogicalModelID  string
	UpstreamModelID string
	ProviderID      domain.ProviderInstanceID
	ProviderName    string
	AdapterID       string
	CredentialID    domain.CredentialID
	EgressID        string
	EgressMode      string
	EgressSource    string
	EgressProxyURL  string
	Pinned          bool
}

// AttemptOutcome is delivered to the compatibility resolver after every real
// upstream request so legacy accounting/state can be updated in one place.
type AttemptOutcome struct {
	InvocationID string
	AttemptID    string
	Attempt      CandidateAttempt
	StartedAt    time.Time
	Duration     time.Duration
	StatusCode   int
	Header       http.Header
	Body         []byte
	Err          error
	Success      bool
}

// RefreshAttempt may prepare one same-credential replay (for example a Cline
// token refresh after 401). It returns retry=false when no replay is available.
type RefreshAttempt func(context.Context, AttemptOutcome) (invocation provideradapters.Invocation, retry bool, err error)

// AcquireAttempt reserves provider-local capacity for one real invocation and
// returns a release function. It is used for compatibility concurrency limits
// without moving provider state into the neutral executor.
type AcquireAttempt func(context.Context) (release func(), err error)

// IDGenerator creates server-owned request and invocation identities. A custom
// generator must return unique, nonempty IDs and be safe for concurrent callers.
type IDGenerator func() (string, error)

// InvocationEvent identifies one invocation immediately before upstream I/O.
// IDs are distinct for credential refreshes, retries, and fallback attempts.
type InvocationEvent struct {
	ID        string
	RequestID string
	Attempt   CandidateAttempt
	At        time.Time
}

// AdmitAttempt atomically authorizes and records one invocation after capacity
// acquisition and cancellation checks. Its opaque attempt ID is carried into
// outcomes and the successful result for final usage attribution. An error
// prevents upstream I/O and is classified by the executor's failure policy.
type AdmitAttempt func(context.Context, InvocationEvent) (attemptID string, err error)

// RetryAttempt decides whether the same resolved attempt should be replayed
// after a real failure. failureIndex is zero for the first failed invocation.
type RetryAttempt func(AttemptOutcome, int) (delay time.Duration, retry bool)

// ResolvedAttempt contains secret-bearing invocation material. It is created
// by the application composition root and never stored in planner snapshots.
type ResolvedAttempt struct {
	Invocation        provideradapters.Invocation
	Account           *domain.Account
	ConnectionID      string
	Acquire           AcquireAttempt
	Admit             AdmitAttempt
	Refresh           RefreshAttempt
	Retry             RetryAttempt
	TransformResponse func(*http.Response) (*http.Response, error)
	Observe           func(AttemptOutcome)
	TagResponse       func(*http.Response)
}

type AttemptResolver interface {
	Resolve(context.Context, Request, CandidateAttempt) (ResolvedAttempt, error)
}

type FailureClass string

const (
	FailureRetryable     FailureClass = "retryable"
	FailureDeterministic FailureClass = "deterministic"
	FailureCancelled     FailureClass = "cancelled"
)

type Failure struct {
	Attempt      CandidateAttempt
	StatusCode   int
	Body         []byte
	Err          error
	Observations []domain.AvailabilityObservation
}

type FailurePolicy func(Failure) FailureClass

type TraceRecord struct {
	Order           int                       `json:"order"`
	Group           int                       `json:"group"`
	ModelID         string                    `json:"modelId"`
	UpstreamModelID string                    `json:"upstreamModelId"`
	ProviderID      domain.ProviderInstanceID `json:"providerId"`
	ProviderName    string                    `json:"providerName,omitempty"`
	CredentialID    domain.CredentialID       `json:"credentialId,omitempty"`
	EgressID        string                    `json:"egressId,omitempty"`
	StatusCode      int                       `json:"statusCode,omitempty"`
	Outcome         string                    `json:"outcome"`
	Reason          string                    `json:"reason,omitempty"`
	DurationMS      int64                     `json:"durationMs"`
	Retryable       bool                      `json:"retryable,omitempty"`
}

type Result struct {
	RequestID     string
	InvocationID  string
	AttemptID     string
	Response      *http.Response
	Serving       CandidateAttempt
	Account       *domain.Account
	ConnectionID  string
	Trace         []TraceRecord
	FallbackCount int
}

// StreamCommitState tracks semantic output, not HTTP headers, SSE comments, or
// protocol envelopes. Existing transparent pre-content retries rely on this.
type StreamCommitState struct{ committed atomic.Bool }

func (s *StreamCommitState) MarkCommitted()  { s.committed.Store(true) }
func (s *StreamCommitState) Committed() bool { return s != nil && s.committed.Load() }
