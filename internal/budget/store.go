package budget

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"routenest/internal/domain"
)

const (
	FailClosed = "fail_closed"
	FailOpen   = "fail_open"
)

type Options struct {
	Now                  func() time.Time
	FailurePolicy        string
	FailOpenAcknowledged bool
	FailurePolicySource  func() (policy string, acknowledged bool)
}

var defaultStore atomic.Pointer[Store]

func Publish(store *Store) error {
	if store == nil {
		return fmt.Errorf("budget: default store is nil")
	}
	defaultStore.Store(store)
	return nil
}

func Default() *Store {
	store := defaultStore.Load()
	if store == nil {
		panic("budget: app.Configure() was not called (store unset)")
	}
	return store
}

func HasDefault() bool { return defaultStore.Load() != nil }

// Store owns policy and outage decisions, while the backend owns all atomic
// accounting. Its cached snapshots are diagnostics only and never authorize a
// hard-limited attempt during an outage.
type Store struct {
	backend    Backend
	policies   PolicySource
	now        func() time.Time
	failOpen   func() bool
	mu         sync.Mutex
	cached     map[string]State
	volatile   map[string]State
	failed     bool
	bestEffort bool
	lastRead   time.Time
	lastWrite  time.Time
}

func NewStore(backend Backend, policies PolicySource, options Options) *Store {
	if options.Now == nil {
		options.Now = time.Now
	}
	if policies == nil {
		policies = func(string) Policy { return Policy{} }
	}
	failurePolicySource := options.FailurePolicySource
	if failurePolicySource == nil {
		policy, acknowledged := options.FailurePolicy, options.FailOpenAcknowledged
		failurePolicySource = func() (string, bool) { return policy, acknowledged }
	}
	return &Store{
		backend: backend, policies: policies, now: options.Now,
		failOpen: func() bool {
			policy, acknowledged := failurePolicySource()
			return policy == FailOpen && acknowledged
		},
		cached: map[string]State{}, volatile: map[string]State{},
	}
}

func (s *Store) markFailed(bestEffort bool) {
	s.mu.Lock()
	s.failed = true
	s.bestEffort = s.bestEffort || bestEffort
	s.mu.Unlock()
}

func (s *Store) markSuccess(write bool) {
	s.mu.Lock()
	s.failed = false
	if write {
		s.lastWrite = s.now().UTC()
	} else {
		s.lastRead = s.now().UTC()
	}
	s.mu.Unlock()
}

// CheckAndRecord generates the admission timestamp for a single-use legacy
// invocation. Accounting retries must call CheckAndRecordCommand with stable At.
func (s *Store) CheckAndRecord(ctx context.Context, providerID, modelID, attemptID string) (Admission, error) {
	return s.CheckAndRecordCommand(ctx, AdmissionCommand{ProviderID: providerID, ModelID: modelID, AttemptID: attemptID, At: s.now()})
}

// CheckAndRecordCommand takes the stable server-owned invocation time and ID.
// Policy always comes from the configured source. Reusing an ID with a different
// timestamp, scope, or current normalized policy is an idempotency conflict.
func (s *Store) CheckAndRecordCommand(ctx context.Context, command AdmissionCommand) (Admission, error) {
	command.Policy = s.policies(command.ProviderID)
	return s.checkAndRecordCommand(ctx, command)
}

// CheckAndRecordWithPolicy is the compatibility seam for callers that already
// hold an immutable provider definition under another lock. It avoids a policy
// callback that could invert that lock order; new callers should prefer
// CheckAndRecordCommand.
func (s *Store) CheckAndRecordWithPolicy(ctx context.Context, command AdmissionCommand) (Admission, error) {
	return s.checkAndRecordCommand(ctx, command)
}

func (s *Store) checkAndRecordCommand(ctx context.Context, command AdmissionCommand) (Admission, error) {
	if command.ProviderID == "" || command.ModelID == "" || command.AttemptID == "" || command.At.IsZero() {
		return Admission{}, fmt.Errorf("budget: invalid admission identity")
	}
	if err := ctx.Err(); err != nil {
		return Admission{}, err
	}
	providerID, modelID, attemptID := command.ProviderID, command.ModelID, command.AttemptID
	policy := command.Policy.normalized()
	command.Policy, command.At = policy, command.At.UTC()
	// A replay of a best-effort invocation must not acquire new durable capacity
	// after recovery; its final tokens remain explicitly best effort as well.
	s.mu.Lock()
	if prior, exists := s.volatile[providerID].Attempts[attemptID]; exists {
		s.mu.Unlock()
		if prior.ProviderID != providerID || prior.ModelID != modelID || !prior.At.Equal(command.At) || prior.Policy != policy {
			return Admission{}, &IdempotencyError{attemptID, "attempt payload changed"}
		}
		prior.BestEffort, prior.Replay = true, true
		return prior, nil
	}
	s.mu.Unlock()
	var receipt Admission
	var err error
	if s.backend == nil {
		err = &UnavailableError{}
	} else {
		receipt, err = s.backend.CheckAndRecord(ctx, command)
	}
	if err == nil {
		s.markSuccess(true)
		return receipt, nil
	}
	if errors.Is(err, ErrExhausted) || errors.Is(err, ErrIdempotency) {
		// A semantic rejection proves the backend is reachable and authoritative.
		s.markSuccess(errors.Is(err, ErrExhausted))
		return receipt, err
	}
	if ctx.Err() != nil {
		return Admission{}, ctx.Err()
	}
	bestEffort := s.failOpen() || !policy.HardLimited()
	s.markFailed(bestEffort)
	if !bestEffort {
		return Admission{}, &UnavailableError{}
	}
	// No hard guarantee is made for these volatile receipts. They exist solely
	// to preserve local retry/token identity while degradation is observable.
	s.mu.Lock()
	state := s.volatile[providerID]
	command.Policy = Policy{}
	receipt, err = Admit(&state, command)
	if err == nil {
		receipt.TokenWindow = policy.window()
		receipt.Policy = policy
		state.Attempts[attemptID] = receipt
		s.volatile[providerID] = state
		receipt.BestEffort = true
	}
	s.mu.Unlock()
	return receipt, err
}

func (s *Store) RecordTokens(ctx context.Context, providerID, attemptID, eventID string, tokens int64) error {
	if providerID == "" || attemptID == "" || eventID == "" || tokens < 0 {
		return fmt.Errorf("budget: invalid token event")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	event := TokenEvent{ProviderID: providerID, AttemptID: attemptID, EventID: eventID, Tokens: tokens}
	s.mu.Lock()
	state := s.volatile[providerID]
	_, bestEffort := state.Attempts[attemptID]
	if bestEffort {
		err := Account(&state, event)
		if err == nil {
			s.volatile[providerID] = state
		}
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	var err error
	if s.backend == nil {
		err = &UnavailableError{}
	} else {
		err = s.backend.RecordTokens(ctx, event)
	}
	if err == nil {
		s.markSuccess(true)
		return nil
	}
	if errors.Is(err, ErrIdempotency) {
		s.markSuccess(false)
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s.markFailed(s.failOpen())
	return &UnavailableError{}
}

// Exhausted is a planner-only snapshot. During an outage it may return cached
// diagnostic exhaustion together with ErrUnavailable; dispatch always performs
// a fresh atomic CheckAndRecord rather than trusting this result.
func (s *Store) Exhausted(ctx context.Context, providerID, modelID string) (bool, error) {
	return s.ExhaustedWithPolicy(ctx, providerID, modelID, s.policies(providerID))
}

// Snapshot returns one detached provider partition for diagnostics and
// compatibility projections. It never authorizes dispatch.
func (s *Store) Snapshot(ctx context.Context, providerID string) (State, error) {
	if providerID == "" {
		return State{}, fmt.Errorf("budget: invalid snapshot provider")
	}
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	if s.backend != nil {
		state, err := s.backend.Snapshot(ctx, providerID)
		if err == nil {
			s.mu.Lock()
			s.cached[providerID] = state.Clone()
			s.mu.Unlock()
			s.markSuccess(false)
			return state, nil
		}
		if ctx.Err() != nil {
			return State{}, ctx.Err()
		}
	}
	s.markFailed(false)
	s.mu.Lock()
	state := s.cached[providerID].Clone()
	s.mu.Unlock()
	return state, &UnavailableError{}
}

// ExhaustedWithPolicy is the lock-order-safe compatibility form used while
// provider selection still owns immutable provider definitions.
func (s *Store) ExhaustedWithPolicy(ctx context.Context, providerID, modelID string, policy Policy) (bool, error) {
	exhaustion, err := s.ExhaustionWithPolicy(ctx, providerID, modelID, policy)
	return exhaustion != nil, err
}

// ExhaustionWithPolicy returns the exact configured-window evidence used by
// compatibility projections without consuming capacity.
func (s *Store) ExhaustionWithPolicy(ctx context.Context, providerID, modelID string, policy Policy) (*ExhaustionError, error) {
	if providerID == "" || modelID == "" {
		return nil, fmt.Errorf("budget: invalid exhaustion scope")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	policy = policy.normalized()
	var state State
	var err error
	if s.backend == nil {
		err = &UnavailableError{}
	} else {
		state, err = s.backend.Snapshot(ctx, providerID)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		s.markFailed(false)
		s.mu.Lock()
		state = s.cached[providerID].Clone()
		s.mu.Unlock()
		return Exhaustion(state, providerID, modelID, policy, s.now()), &UnavailableError{}
	}
	s.mu.Lock()
	s.cached[providerID] = state.Clone()
	s.mu.Unlock()
	s.markSuccess(false)
	return Exhaustion(state, providerID, modelID, policy, s.now()), nil
}

// Health returns fixed labels and timestamps only, even when an adapter returns
// an error containing a secret or address. Past best-effort invocations remain
// degraded until restart because their missing durable accounting is uncertain.
func (s *Store) Health() domain.RuntimeStateHealth {
	health := domain.RuntimeStateHealth{Backend: "unconfigured", Degraded: true, Enforcement: FailClosed, Reason: "backend_unavailable"}
	if s.backend != nil {
		health = s.backend.Health()
	}
	if !safeLabel(health.Backend) {
		health.Backend = "runtime_state"
	}
	s.mu.Lock()
	failed, bestEffort := s.failed, s.bestEffort
	lastRead, lastWrite := s.lastRead, s.lastWrite
	s.mu.Unlock()
	if lastRead.After(health.LastSuccessfulRead) {
		health.LastSuccessfulRead = lastRead
	}
	if lastWrite.After(health.LastSuccessfulWrite) {
		health.LastSuccessfulWrite = lastWrite
	}
	if failed {
		health.Ready, health.SingleNodeReady = false, false
		health.Degraded, health.Stale = true, true
		health.Reason = "backend_unavailable"
	}
	if bestEffort {
		health.Degraded = true
		health.Enforcement = "best_effort"
		if !failed {
			health.Reason = "best_effort_accounting"
		}
	} else {
		health.Enforcement = FailClosed
	}
	if !safeReason(health.Reason) {
		health.Reason = "backend_unavailable"
	}
	return health
}

func safeLabel(label string) bool {
	if label == "" || len(label) > 64 {
		return false
	}
	for _, c := range label {
		if !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

func safeReason(reason string) bool {
	switch strings.TrimSpace(reason) {
	case "", "backend_unavailable", "backend_not_ready", "multi_replica_unsupported", "initializing", "best_effort_accounting", "persistence_failed", "state_corrupt", "state_unreadable":
		return true
	default:
		return false
	}
}
