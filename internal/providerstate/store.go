package providerstate

import (
	"fmt"
	"sort"
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

// Store owns normalized runtime availability observations. It is independent
// of provider protocols and legacy persistence adapters.
type Store struct {
	mu     sync.Mutex
	now    func() time.Time
	states map[domain.AvailabilityScope]map[domain.ObservationKind]domain.AvailabilityObservation
}

func NewStore(now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{
		now:    now,
		states: map[domain.AvailabilityScope]map[domain.ObservationKind]domain.AvailabilityObservation{},
	}
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
	if blocker.ProviderID != candidate.ProviderID {
		return false
	}
	if blocker.CredentialID != "" && blocker.CredentialID != candidate.CredentialID {
		return false
	}
	if blocker.ModelID != "" && blocker.ModelID != candidate.ModelID {
		return false
	}
	if blocker.EgressID != "" && blocker.EgressID != candidate.EgressID {
		return false
	}
	return true
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

func (s *Store) expireLocked(now time.Time) {
	for scope, byKind := range s.states {
		for kind, observation := range byKind {
			until := expiry(observation)
			if !until.IsZero() && !now.Before(until) {
				delete(byKind, kind)
			}
		}
		if len(byKind) == 0 {
			delete(s.states, scope)
		}
	}
}

func (s *Store) Apply(observation domain.AvailabilityObservation) error {
	if s == nil {
		return fmt.Errorf("providerstate: store is nil")
	}
	if !observation.Valid() {
		return fmt.Errorf("providerstate: invalid observation")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.expireLocked(now)
	if observation.Kind == domain.ObservationRecovered {
		delete(s.states, observation.Scope)
		return nil
	}
	if observation.Kind == domain.ObservationQuotaReset {
		if byKind := s.states[observation.Scope]; byKind != nil {
			delete(byKind, domain.ObservationQuotaExhausted)
			if len(byKind) == 0 {
				delete(s.states, observation.Scope)
			}
		}
		return nil
	}
	if until := expiry(observation); !until.IsZero() && !now.Before(until) {
		return nil
	}
	byKind := s.states[observation.Scope]
	if byKind == nil {
		byKind = map[domain.ObservationKind]domain.AvailabilityObservation{}
		s.states[observation.Scope] = byKind
	}
	byKind[observation.Kind] = observation
	return nil
}

func (s *Store) Recover(scope domain.AvailabilityScope) error {
	if s == nil {
		return fmt.Errorf("providerstate: store is nil")
	}
	if !scope.Valid() {
		return fmt.Errorf("providerstate: invalid recovery scope")
	}
	s.mu.Lock()
	delete(s.states, scope)
	s.mu.Unlock()
	return nil
}

func scopeMatches(blocker, candidate domain.AvailabilityScope) bool {
	return ScopeMatches(blocker, candidate)
}

func observationLess(a, b domain.AvailabilityObservation) bool {
	if a.Scope.ProviderID != b.Scope.ProviderID {
		return a.Scope.ProviderID < b.Scope.ProviderID
	}
	if a.Scope.CredentialID != b.Scope.CredentialID {
		return a.Scope.CredentialID < b.Scope.CredentialID
	}
	if a.Scope.ModelID != b.Scope.ModelID {
		return a.Scope.ModelID < b.Scope.ModelID
	}
	if a.Scope.EgressID != b.Scope.EgressID {
		return a.Scope.EgressID < b.Scope.EgressID
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
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(s.now())
	var out []domain.AvailabilityObservation
	for scope, byKind := range s.states {
		if !scopeMatches(scope, candidate) {
			continue
		}
		for _, observation := range byKind {
			out = append(out, observation)
		}
	}
	sort.Slice(out, func(i, j int) bool { return observationLess(out[i], out[j]) })
	return out
}

func (s *Store) Snapshot(providerID domain.ProviderInstanceID) []domain.AvailabilityObservation {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(s.now())
	var out []domain.AvailabilityObservation
	for scope, byKind := range s.states {
		if providerID != "" && scope.ProviderID != providerID {
			continue
		}
		for _, observation := range byKind {
			out = append(out, observation)
		}
	}
	sort.Slice(out, func(i, j int) bool { return observationLess(out[i], out[j]) })
	return out
}

// DueScopes returns unique scopes whose timed state is ready for a recovery
// probe. It does not mutate state; failed probes can therefore defer it.
func (s *Store) DueScopes(now time.Time) []domain.AvailabilityScope {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[domain.AvailabilityScope]bool{}
	for scope, byKind := range s.states {
		for _, observation := range byKind {
			until := expiry(observation)
			if !until.IsZero() && !now.Before(until) {
				seen[scope] = true
				break
			}
		}
	}
	out := make([]domain.AvailabilityScope, 0, len(seen))
	for scope := range seen {
		out = append(out, scope)
	}
	sort.Slice(out, func(i, j int) bool { return scopeLess(out[i], out[j]) })
	return out
}

// Defer moves expired timed observations at one scope to a later probe time.
// A confirmed reset that already passed is retained as evidence only in logs;
// ResetAt is cleared so it cannot override the new fallback retry time.
func (s *Store) Defer(scope domain.AvailabilityScope, until time.Time) error {
	if s == nil || !scope.Valid() || until.IsZero() {
		return fmt.Errorf("providerstate: invalid deferred recovery")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if !until.After(now) {
		return fmt.Errorf("providerstate: deferred recovery must be in the future")
	}
	for kind, observation := range s.states[scope] {
		if current := expiry(observation); current.IsZero() || now.Before(current) {
			continue
		}
		observation.Until = until
		if !observation.ResetAt.IsZero() && !now.Before(observation.ResetAt) {
			observation.ResetAt = time.Time{}
		}
		observation.Source = "recovery_probe"
		observation.Confidence = domain.ConfidenceFallback
		s.states[scope][kind] = observation
	}
	return nil
}
