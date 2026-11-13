package providerstate

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"routenest/internal/domain"
)

var defaultStore atomic.Pointer[Store]

func Publish(store *Store) error {
	if store == nil {
		return fmt.Errorf("providerstate: default store is nil")
	}
	defaultStore.Store(store)
	return nil
}

func Default() *Store {
	store := defaultStore.Load()
	if store == nil {
		panic("providerstate: app.Configure() was not called (store unset)")
	}
	return store
}

func HasDefault() bool { return defaultStore.Load() != nil }

// Store caches normalized observations. The backend, rather than this cache,
// owns mutation ordering and durable state across Store instances.
type Store struct {
	mu          sync.Mutex
	now         func() time.Time
	backend     Backend
	state       BackendSnapshot
	lastErr     error
	unavailable bool
}

// NewStore retains the process-local constructor for compatibility.
func NewStore(now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{now: now, backend: &memoryBackend{}}
}

// NewDurableStore hydrates and durably removes expired observations before the
// store can be published. A failed read or expiry write fails construction.
func NewDurableStore(backend Backend, now func() time.Time) (*Store, error) {
	if backend == nil {
		return nil, fmt.Errorf("providerstate: backend is nil")
	}
	if now == nil {
		now = time.Now
	}
	s := &Store{now: now, backend: backend}
	if err := s.refreshLocked(context.Background(), true); err != nil {
		return nil, err
	}
	return s, nil
}

func expiry(observation domain.AvailabilityObservation) time.Time {
	if observation.Kind == domain.ObservationQuotaExhausted && !observation.ResetAt.IsZero() {
		return observation.ResetAt
	}
	return observation.Until
}

// Active reports whether an observation may still block a candidate at now.
// Recovery/reset observations are clearing events, not blockers.
func Active(observation domain.AvailabilityObservation, now time.Time) bool {
	if observation.Kind == domain.ObservationRecovered || observation.Kind == domain.ObservationQuotaReset {
		return false
	}
	until := expiry(observation)
	return until.IsZero() || now.Before(until)
}

// ScopeMatches reports whether a blocking scope applies to a candidate scope.
// A blocker's unset dimension is broader than the candidate's.
func ScopeMatches(blocker, candidate domain.AvailabilityScope) bool {
	return blocker.ProviderID == candidate.ProviderID &&
		(blocker.CredentialID == "" || blocker.CredentialID == candidate.CredentialID) &&
		(blocker.ModelID == "" || blocker.ModelID == candidate.ModelID) &&
		(blocker.EgressID == "" || blocker.EgressID == candidate.EgressID)
}

// Blocks returns the active observations that apply to scope, in input order.
func Blocks(observations []domain.AvailabilityObservation, scope domain.AvailabilityScope, now time.Time) []domain.AvailabilityObservation {
	var out []domain.AvailabilityObservation
	for _, observation := range observations {
		if ScopeMatches(observation.Scope, scope) && Active(observation, now) {
			out = append(out, observation)
		}
	}
	return out
}

func scopeIndex(state *BackendSnapshot, scope domain.AvailabilityScope) int {
	for i := range state.Scopes {
		if state.Scopes[i].Scope == scope {
			return i
		}
	}
	return -1
}

func ensureScope(state *BackendSnapshot, scope domain.AvailabilityScope) *ScopeState {
	i := scopeIndex(state, scope)
	if i < 0 {
		state.Scopes = append(state.Scopes, ScopeState{Scope: scope})
		i = len(state.Scopes) - 1
	}
	return &state.Scopes[i]
}

func purgeExpired(state *BackendSnapshot, now time.Time) bool {
	changed := false
	for i := range state.Scopes {
		entry := &state.Scopes[i]
		kept := entry.Observations[:0]
		removed := false
		for _, observation := range entry.Observations {
			if until := expiry(observation); !until.IsZero() && !now.Before(until) {
				removed = true
				continue
			}
			kept = append(kept, observation)
		}
		entry.Observations = kept
		if removed {
			entry.Revision++
			changed = true
		}
	}
	return changed
}

func validateSnapshot(state BackendSnapshot) error {
	scopes := make(map[domain.AvailabilityScope]bool, len(state.Scopes))
	for _, entry := range state.Scopes {
		if !entry.Scope.Valid() || scopes[entry.Scope] {
			return fmt.Errorf("providerstate: invalid backend scope")
		}
		scopes[entry.Scope] = true
		kinds := make(map[domain.ObservationKind]bool, len(entry.Observations))
		for _, observation := range entry.Observations {
			if !observation.Valid() || observation.Scope != entry.Scope || observation.Kind == domain.ObservationRecovered || observation.Kind == domain.ObservationQuotaReset || kinds[observation.Kind] {
				return fmt.Errorf("providerstate: invalid backend observation")
			}
			kinds[observation.Kind] = true
		}
	}
	events := make(map[string]bool, len(state.Events))
	for _, receipt := range state.Events {
		if receipt.EventID == "" || receipt.EventID != strings.TrimSpace(receipt.EventID) || !receipt.Observation.Valid() || events[receipt.EventID] {
			return fmt.Errorf("providerstate: invalid backend event receipt")
		}
		events[receipt.EventID] = true
	}
	return nil
}

// mutateLocked retries from authoritative snapshots. No cache change is
// published until a backend write succeeds or an idempotent replay is proven.
func (s *Store) mutateLocked(ctx context.Context, mutate func(*BackendSnapshot) (bool, error)) error {
	for attempt := 0; attempt < 64; attempt++ {
		current, err := s.backend.Snapshot(ctx)
		if err == nil {
			err = validateSnapshot(current)
		}
		if err != nil {
			s.lastErr = err
			s.unavailable = true
			return err
		}
		next := CloneSnapshot(current)
		changed, err := mutate(&next)
		if err != nil {
			s.state = CloneSnapshot(current)
			s.lastErr = err
			s.unavailable = false
			return err
		}
		if !changed {
			s.state = CloneSnapshot(current)
			s.lastErr = nil
			s.unavailable = false
			return nil
		}
		next.Revision = current.Revision + 1
		committed, err := s.backend.CompareAndSwap(ctx, current.Revision, next)
		if err != nil {
			s.lastErr = err
			s.unavailable = true
			return err
		}
		if committed {
			s.state = CloneSnapshot(next)
			s.lastErr = nil
			s.unavailable = false
			return nil
		}
	}
	s.lastErr = ErrConflict
	return ErrConflict
}

func (s *Store) refreshLocked(ctx context.Context, purge bool) error {
	return s.mutateLocked(ctx, func(state *BackendSnapshot) (bool, error) {
		if purge {
			return purgeExpired(state, s.now()), nil
		}
		return false, nil
	})
}

func (s *Store) Apply(observation domain.AvailabilityObservation) error {
	return s.apply("", observation)
}

// ApplyEvent durably deduplicates a stable upstream event ID. Replaying an ID
// with different evidence is rejected instead of silently dropping new evidence.
func (s *Store) ApplyEvent(eventID string, observation domain.AvailabilityObservation) error {
	if eventID == "" || eventID != strings.TrimSpace(eventID) {
		return fmt.Errorf("providerstate: invalid event ID")
	}
	return s.apply(eventID, observation)
}

func (s *Store) apply(eventID string, observation domain.AvailabilityObservation) error {
	if s == nil {
		return fmt.Errorf("providerstate: store is nil")
	}
	if !observation.Valid() {
		return fmt.Errorf("providerstate: invalid observation")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	return s.mutateLocked(context.Background(), func(state *BackendSnapshot) (bool, error) {
		if eventID != "" {
			for _, receipt := range state.Events {
				if receipt.EventID != eventID {
					continue
				}
				if !sameObservation(receipt.Observation, observation) {
					return false, fmt.Errorf("providerstate: event ID reused with different evidence")
				}
				return false, nil
			}
		}
		changed := purgeExpired(state, now)
		if eventID != "" {
			state.Events = append(state.Events, EventReceipt{EventID: eventID, Observation: observation})
			changed = true
		}
		entry := ensureScope(state, observation.Scope)
		switch observation.Kind {
		case domain.ObservationRecovered:
			entry.Observations = nil
		case domain.ObservationQuotaReset:
			kept := entry.Observations[:0]
			for _, old := range entry.Observations {
				if old.Kind != domain.ObservationQuotaExhausted {
					kept = append(kept, old)
				}
			}
			entry.Observations = kept
		default:
			if !Active(observation, now) {
				return changed, nil
			}
			replaced := false
			for i := range entry.Observations {
				if entry.Observations[i].Kind == observation.Kind {
					entry.Observations[i] = observation
					replaced = true
					break
				}
			}
			if !replaced {
				entry.Observations = append(entry.Observations, observation)
			}
		}
		entry.Revision++
		return true, nil
	})
}

func sameObservation(a, b domain.AvailabilityObservation) bool {
	return a.Kind == b.Kind && a.Scope == b.Scope && a.Until.Equal(b.Until) &&
		a.ResetAt.Equal(b.ResetAt) && a.Source == b.Source && a.Confidence == b.Confidence
}

// Guard captures the authoritative revision without expiring due state, so a
// recovery probe can operate on that evidence and then conditionally clear it.
func (s *Store) Guard(scope domain.AvailabilityScope) (ScopeGuard, error) {
	if s == nil || !scope.Valid() {
		return ScopeGuard{}, fmt.Errorf("providerstate: invalid recovery scope")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(context.Background(), false); err != nil {
		return ScopeGuard{}, err
	}
	guard := ScopeGuard{Scope: scope}
	if i := scopeIndex(&s.state, scope); i >= 0 {
		guard.Revision = s.state.Scopes[i].Revision
	}
	return guard, nil
}

// RecoverGuard clears only the exact scope if no newer evidence changed it.
func (s *Store) RecoverGuard(guard ScopeGuard) error {
	return s.recover(&guard, guard.Scope)
}

// Recover retains unconditional exact-scope clearing for compatibility. Probe
// callers should capture Guard before I/O and use RecoverGuard afterward.
func (s *Store) Recover(scope domain.AvailabilityScope) error { return s.recover(nil, scope) }

func (s *Store) recover(guard *ScopeGuard, scope domain.AvailabilityScope) error {
	if s == nil || !scope.Valid() {
		return fmt.Errorf("providerstate: invalid recovery scope")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutateLocked(context.Background(), func(state *BackendSnapshot) (bool, error) {
		i := scopeIndex(state, scope)
		revision := uint64(0)
		if i >= 0 {
			revision = state.Scopes[i].Revision
		}
		if guard != nil && guard.Revision != revision {
			return false, ErrConflict
		}
		if i < 0 || len(state.Scopes[i].Observations) == 0 {
			return false, nil
		}
		state.Scopes[i].Observations = nil
		state.Scopes[i].Revision++
		return true, nil
	})
}

// ClearCooldowns removes active, resettable runtime evidence for one provider.
// A model-specific reset also includes broader scopes (an empty ModelID), since
// those scopes block that model too. Durable configured-budget evidence and
// permanent auth/model availability evidence are deliberately retained.
func (s *Store) ClearCooldowns(providerID domain.ProviderInstanceID, modelID string) (int, error) {
	if s == nil || !providerID.Valid() || modelID != strings.TrimSpace(modelID) {
		return 0, fmt.Errorf("providerstate: invalid cooldown reset")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	removed := 0
	err := s.mutateLocked(context.Background(), func(state *BackendSnapshot) (bool, error) {
		removed = 0 // mutateLocked may retry after a compare-and-swap conflict.
		changed := purgeExpired(state, now)
		for i := range state.Scopes {
			entry := &state.Scopes[i]
			if entry.Scope.ProviderID != providerID || modelID != "" && entry.Scope.ModelID != "" && entry.Scope.ModelID != modelID {
				continue
			}
			kept := entry.Observations[:0]
			entryRemoved := 0
			for _, observation := range entry.Observations {
				if resettableCooldown(observation) {
					entryRemoved++
					continue
				}
				kept = append(kept, observation)
			}
			if entryRemoved == 0 {
				continue
			}
			entry.Observations = kept
			entry.Revision++
			removed += entryRemoved
			changed = true
		}
		return changed, nil
	})
	if err != nil {
		return 0, err
	}
	return removed, nil
}

func resettableCooldown(observation domain.AvailabilityObservation) bool {
	if observation.Source == "configured_window" {
		return false
	}
	switch observation.Kind {
	case domain.ObservationRateLimited, domain.ObservationQuotaExhausted, domain.ObservationTransientFailure:
		return true
	default:
		return false
	}
}

func observationLess(a, b domain.AvailabilityObservation) bool {
	if a.Scope != b.Scope {
		return scopeLess(a.Scope, b.Scope)
	}
	return a.Kind < b.Kind
}

func scopeLess(a, b domain.AvailabilityScope) bool {
	if a.ProviderID != b.ProviderID {
		return a.ProviderID < b.ProviderID
	}
	if a.CredentialID != b.CredentialID {
		return a.CredentialID < b.CredentialID
	}
	if a.ModelID != b.ModelID {
		return a.ModelID < b.ModelID
	}
	return a.EgressID < b.EgressID
}

func (s *Store) Blocking(candidate domain.AvailabilityScope) []domain.AvailabilityObservation {
	if s == nil || !candidate.Valid() {
		return nil
	}
	return Blocks(s.Snapshot(candidate.ProviderID), candidate, s.now())
}

// Snapshot preserves the read-only compatibility API. Backend failure retains
// last-good diagnostics and is visible through LastError and Health.
func (s *Store) Snapshot(providerID domain.ProviderInstanceID) []domain.AvailabilityObservation {
	observations, _ := s.SnapshotWithError(context.Background(), providerID)
	return observations
}

// SnapshotWithError refreshes authoritative state and persists lazy expiry.
func (s *Store) SnapshotWithError(ctx context.Context, providerID domain.ProviderInstanceID) ([]domain.AvailabilityObservation, error) {
	if s == nil {
		return nil, fmt.Errorf("providerstate: store is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.refreshLocked(ctx, true)
	var out []domain.AvailabilityObservation
	now := s.now()
	for _, entry := range s.state.Scopes {
		if providerID != "" && entry.Scope.ProviderID != providerID {
			continue
		}
		for _, observation := range entry.Observations {
			if Active(observation, now) {
				out = append(out, observation)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return observationLess(out[i], out[j]) })
	return out, err
}

// DueScopes returns unique scopes ready for recovery without expiring evidence.
func (s *Store) DueScopes(now time.Time) []domain.AvailabilityScope {
	scopes, _ := s.DueScopesWithError(context.Background(), now)
	return scopes
}

func (s *Store) DueScopesWithError(ctx context.Context, now time.Time) ([]domain.AvailabilityScope, error) {
	guards, err := s.DueGuards(ctx, now)
	if err != nil {
		return nil, err
	}
	var scopes []domain.AvailabilityScope
	for _, guard := range guards {
		scopes = append(scopes, guard.Scope)
	}
	return scopes, nil
}

// DueGuards captures due evidence and its revisions in one authoritative read.
// Recovery must not capture a fresh guard after selecting older due evidence.
func (s *Store) DueGuards(ctx context.Context, now time.Time) ([]ScopeGuard, error) {
	if s == nil {
		return nil, fmt.Errorf("providerstate: store is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(ctx, false); err != nil {
		return nil, err
	}
	var out []ScopeGuard
	for _, entry := range s.state.Scopes {
		for _, observation := range entry.Observations {
			if until := expiry(observation); !until.IsZero() && !now.Before(until) {
				out = append(out, ScopeGuard{Scope: entry.Scope, Revision: entry.Revision})
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return scopeLess(out[i].Scope, out[j].Scope) })
	return out, nil
}

// Defer moves expired timed observations at one scope to a later probe time.
func (s *Store) Defer(scope domain.AvailabilityScope, until time.Time) error {
	return s.deferScope(nil, scope, until)
}

// DeferGuard prevents a failed slow probe from rewriting newer exact-scope evidence.
func (s *Store) DeferGuard(guard ScopeGuard, until time.Time) error {
	return s.deferScope(&guard, guard.Scope, until)
}

func (s *Store) deferScope(guard *ScopeGuard, scope domain.AvailabilityScope, until time.Time) error {
	if s == nil || !scope.Valid() || until.IsZero() {
		return fmt.Errorf("providerstate: invalid deferred recovery")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if !until.After(now) {
		return fmt.Errorf("providerstate: deferred recovery must be in the future")
	}
	return s.mutateLocked(context.Background(), func(state *BackendSnapshot) (bool, error) {
		i := scopeIndex(state, scope)
		revision := uint64(0)
		if i >= 0 {
			revision = state.Scopes[i].Revision
		}
		if guard != nil && guard.Revision != revision {
			return false, ErrConflict
		}
		if i < 0 {
			return false, nil
		}
		changed := false
		for j, observation := range state.Scopes[i].Observations {
			if current := expiry(observation); current.IsZero() || now.Before(current) {
				continue
			}
			observation.Until = until
			if !observation.ResetAt.IsZero() && !now.Before(observation.ResetAt) {
				observation.ResetAt = time.Time{}
			}
			observation.Source = "recovery_probe"
			observation.Confidence = domain.ConfidenceFallback
			state.Scopes[i].Observations[j] = observation
			changed = true
		}
		if changed {
			state.Scopes[i].Revision++
		}
		return changed, nil
	})
}

// LastError makes persistence errors visible to compatibility callers whose
// historical signatures cannot return an error.
func (s *Store) LastError() error {
	if s == nil {
		return fmt.Errorf("providerstate: store is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

func (s *Store) recordError(err error) {
	if s == nil || err == nil {
		return
	}
	s.mu.Lock()
	s.lastErr = err
	s.mu.Unlock()
}

func (s *Store) Health() domain.RuntimeStateHealth {
	if s == nil {
		return domain.RuntimeStateHealth{Degraded: true, Stale: true, Reason: "backend_unavailable"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	health := s.backend.Health()
	if s.unavailable {
		health.Ready = false
		health.SingleNodeReady = false
		health.Degraded = true
		health.Stale = true
		health.Reason = "backend_unavailable"
	}
	return health
}
