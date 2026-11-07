// Package budget owns provider invocation admission and final token accounting.
// Backend implementations serialize commands and commit their effects before
// returning success; callers never read or write storage keys.
package budget

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"routenest/internal/domain"
)

var (
	ErrExhausted   = errors.New("provider budget exhausted")
	ErrUnavailable = errors.New("budget backend unavailable")
	ErrIdempotency = errors.New("budget event conflicts with its receipt")
)

type Policy struct {
	RPMLimit    int    `json:"rpmLimit,omitempty"`
	DailyLimit  int    `json:"dailyLimit,omitempty"`
	TokenLimit  int64  `json:"tokenLimit,omitempty"`
	TokenWindow string `json:"tokenWindow"`
}

type PolicySource func(providerID string) Policy

func (p Policy) HardLimited() bool {
	return p.RPMLimit > 0 || p.DailyLimit > 0 || p.TokenLimit > 0
}

func (p Policy) window() string {
	if p.TokenWindow == "daily" {
		return "daily"
	}
	return "monthly"
}

func (p Policy) normalized() Policy {
	p.RPMLimit = max(0, p.RPMLimit)
	p.DailyLimit = max(0, p.DailyLimit)
	p.TokenLimit = max(0, p.TokenLimit)
	p.TokenWindow = p.window()
	return p
}

type ExhaustionError struct {
	ProviderID string
	ModelID    string
	Kind       string
	Limit      int64
	Usage      int64
	ResetAt    time.Time
}

func (e *ExhaustionError) Error() string { return "provider budget exhausted: " + e.Kind }
func (e *ExhaustionError) Unwrap() error { return ErrExhausted }

// UnavailableError deliberately omits backend errors, which can contain paths,
// addresses, credentials, and proxy configuration.
type UnavailableError struct{}

func (*UnavailableError) Error() string { return ErrUnavailable.Error() }
func (*UnavailableError) Unwrap() error { return ErrUnavailable }

type IdempotencyError struct {
	EventID string
	Reason  string
}

func (e *IdempotencyError) Error() string { return "budget idempotency conflict: " + e.Reason }
func (e *IdempotencyError) Unwrap() error { return ErrIdempotency }

// Admission is the durable receipt of one actual upstream invocation. Day and
// Month capture admission time, so completion after rollover cannot spend the
// next window. Replay and BestEffort are response metadata, not durable state.
type Admission struct {
	ProviderID  string           `json:"providerId"`
	ModelID     string           `json:"modelId"`
	AttemptID   string           `json:"attemptId"`
	At          time.Time        `json:"at"`
	Day         string           `json:"day"`
	Month       string           `json:"month"`
	TokenWindow string           `json:"tokenWindow"`
	Policy      Policy           `json:"policy"`
	Allowed     bool             `json:"allowed"`
	Exhaustion  *ExhaustionError `json:"exhaustion,omitempty"`
	Replay      bool             `json:"-"`
	BestEffort  bool             `json:"-"`
}

func (a Admission) Clone() Admission {
	if a.Exhaustion != nil {
		exhaustion := *a.Exhaustion
		a.Exhaustion = &exhaustion
	}
	return a
}

type AdmissionCommand struct {
	ProviderID string
	ModelID    string
	AttemptID  string
	At         time.Time
	Policy     Policy
}

type TokenEvent struct {
	ProviderID string `json:"providerId"`
	AttemptID  string `json:"attemptId"`
	EventID    string `json:"eventId"`
	Tokens     int64  `json:"tokens"`
}

// State is one provider's durable accounting partition. Receipts survive window
// rollover to reject delayed replay; counters and RPM entries can be purged.
type State struct {
	RPM           map[string][]time.Time `json:"rpm,omitempty"`
	DailyRequests map[string]int64       `json:"dailyRequests,omitempty"`
	Tokens        map[string]int64       `json:"tokens,omitempty"`
	Attempts      map[string]Admission   `json:"attempts,omitempty"`
	TokenEvents   map[string]TokenEvent  `json:"tokenEvents,omitempty"`
}

// Backend is an atomic semantic accounting contract. Snapshot returns detached
// state and never consumes capacity. Commands must check idempotency and limits
// under the same transaction/lock as durable commit, rolling back failed writes.
// CheckAndRecord commits both allowed and exhausted receipts, returning the
// persisted receipt and ErrExhausted for a denial. A replay returns that same
// result even after its quota window expires; conflicts never mutate state.
// Event IDs are unique within a provider partition. Health contains only fixed,
// redacted labels; a local file backend must report MultiReplicaSupported false.
type Backend interface {
	Snapshot(context.Context, string) (State, error)
	CheckAndRecord(context.Context, AdmissionCommand) (Admission, error)
	RecordTokens(context.Context, TokenEvent) error
	Health() domain.RuntimeStateHealth
}

func (s State) Clone() State {
	out := State{}
	if s.RPM != nil {
		out.RPM = make(map[string][]time.Time, len(s.RPM))
		for model, at := range s.RPM {
			out.RPM[model] = append([]time.Time{}, at...)
		}
	}
	out.DailyRequests = cloneMap(s.DailyRequests)
	out.Tokens = cloneMap(s.Tokens)
	out.Attempts = cloneMap(s.Attempts)
	for id, admission := range out.Attempts {
		out.Attempts[id] = admission.Clone()
	}
	out.TokenEvents = cloneMap(s.TokenEvents)
	return out
}

func cloneMap[K comparable, V any](in map[K]V) map[K]V {
	if in == nil {
		return nil
	}
	out := make(map[K]V, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

// Purge removes expired counters without removing the receipts needed to make
// retries idempotent. Backends call it on load and within successful mutations.
func (s *State) Purge(now time.Time) {
	cut := now.Add(-time.Minute)
	for model, entries := range s.RPM {
		var kept []time.Time
		for _, at := range entries {
			if at.After(cut) {
				kept = append(kept, at)
			}
		}
		if len(kept) == 0 {
			delete(s.RPM, model)
		} else {
			s.RPM[model] = kept
		}
	}
	day, month := utcKeys(now)
	for key := range s.DailyRequests {
		if key < day {
			delete(s.DailyRequests, key)
		}
	}
	for key := range s.Tokens {
		if (len(key) == len(day) && key < day) || (len(key) == len(month) && key < month) {
			delete(s.Tokens, key)
		}
	}
}

func utcKeys(at time.Time) (day, month string) {
	return at.UTC().Format("2006-01-02"), at.UTC().Format("2006-01")
}

func utcReset(at time.Time, monthly bool) time.Time {
	utc := at.UTC()
	if monthly {
		return time.Date(utc.Year(), utc.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	}
	return time.Date(utc.Year(), utc.Month(), utc.Day()+1, 0, 0, 0, 0, time.UTC)
}

// Exhaustion inspects a partition without mutating it or consuming capacity.
func Exhaustion(s State, providerID, modelID string, policy Policy, now time.Time) *ExhaustionError {
	if policy.RPMLimit > 0 {
		var count int64
		var oldest time.Time
		for _, at := range s.RPM[modelID] {
			if !at.After(now.Add(-time.Minute)) {
				continue
			}
			count++
			if oldest.IsZero() || at.Before(oldest) {
				oldest = at
			}
		}
		if count >= int64(policy.RPMLimit) {
			return &ExhaustionError{providerID, modelID, "rpm", int64(policy.RPMLimit), count, oldest.Add(time.Minute)}
		}
	}
	day, month := utcKeys(now)
	if policy.DailyLimit > 0 && s.DailyRequests[day] >= int64(policy.DailyLimit) {
		return &ExhaustionError{providerID, modelID, "daily_requests", int64(policy.DailyLimit), s.DailyRequests[day], utcReset(now, false)}
	}
	key, monthly := day, false
	if policy.window() == "monthly" {
		key, monthly = month, true
	}
	if policy.TokenLimit > 0 && s.Tokens[key] >= policy.TokenLimit {
		return &ExhaustionError{providerID, modelID, policy.window() + "_tokens", policy.TokenLimit, s.Tokens[key], utcReset(now, monthly)}
	}
	return nil
}

// Admit applies the atomic command to a transaction-local State. A backend must
// publish this state only after its durable write succeeds.
func Admit(s *State, command AdmissionCommand) (Admission, error) {
	if command.ProviderID == "" || command.ModelID == "" || command.AttemptID == "" || command.At.IsZero() {
		return Admission{}, fmt.Errorf("budget: invalid admission command")
	}
	command.Policy = command.Policy.normalized()
	if previous, exists := s.Attempts[command.AttemptID]; exists {
		if previous.ProviderID != command.ProviderID || previous.ModelID != command.ModelID || !previous.At.Equal(command.At) || previous.Policy != command.Policy {
			return Admission{}, &IdempotencyError{command.AttemptID, "attempt payload changed"}
		}
		previous = previous.Clone()
		previous.Replay = true
		if !previous.Allowed {
			return previous, previous.Exhaustion
		}
		return previous, nil
	}
	exhausted := Exhaustion(*s, command.ProviderID, command.ModelID, command.Policy, command.At)
	s.Purge(command.At)
	if s.RPM == nil {
		s.RPM = map[string][]time.Time{}
	}
	if s.DailyRequests == nil {
		s.DailyRequests = map[string]int64{}
	}
	if s.Attempts == nil {
		s.Attempts = map[string]Admission{}
	}
	day, month := utcKeys(command.At)
	receipt := Admission{ProviderID: command.ProviderID, ModelID: command.ModelID, AttemptID: command.AttemptID, At: command.At.UTC(), Day: day, Month: month, TokenWindow: command.Policy.window(), Policy: command.Policy, Allowed: exhausted == nil, Exhaustion: exhausted}
	s.Attempts[command.AttemptID] = receipt.Clone()
	if exhausted != nil {
		return receipt, exhausted
	}
	s.RPM[command.ModelID] = append(s.RPM[command.ModelID], command.At.UTC())
	s.DailyRequests[day] = saturatedAdd(s.DailyRequests[day], 1)
	return receipt, nil
}

// Account ties a final token event to its admitted attempt and charges both UTC
// windows captured at admission. Observed usage can exceed a limit because this
// is final accounting, not a predictive reservation of in-flight tokens.
func Account(s *State, event TokenEvent) error {
	if event.ProviderID == "" || event.AttemptID == "" || event.EventID == "" || event.Tokens < 0 {
		return fmt.Errorf("budget: invalid token event")
	}
	if previous, exists := s.TokenEvents[event.EventID]; exists {
		if previous != event {
			return &IdempotencyError{event.EventID, "token event changed"}
		}
		return nil
	}
	attempt, exists := s.Attempts[event.AttemptID]
	if !exists || !attempt.Allowed || attempt.ProviderID != event.ProviderID {
		return &IdempotencyError{event.EventID, "attempt was not admitted"}
	}
	if s.Tokens == nil {
		s.Tokens = map[string]int64{}
	}
	if s.TokenEvents == nil {
		s.TokenEvents = map[string]TokenEvent{}
	}
	s.Tokens[attempt.Day] = saturatedAdd(s.Tokens[attempt.Day], event.Tokens)
	s.Tokens[attempt.Month] = saturatedAdd(s.Tokens[attempt.Month], event.Tokens)
	s.TokenEvents[event.EventID] = event
	return nil
}

func saturatedAdd(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}
