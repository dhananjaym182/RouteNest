package providerstate

import (
	"context"
	"errors"
	"sync"

	"routenest/internal/domain"
)

// ErrConflict means that newer exact-scope evidence invalidated a recovery
// guard, or that concurrent writers prevented a mutation from committing.
var ErrConflict = errors.New("providerstate: revision conflict")

// Backend is the atomic persistence boundary. Snapshots must be independent
// copies. CompareAndSwap commits the entire next snapshot only when the current
// revision equals expectedRevision; failed writes must leave durable state
// unchanged. A shared adapter must perform this comparison across all clients.
// Health carries only redacted status, never addresses, paths or raw errors.
type Backend interface {
	Snapshot(context.Context) (BackendSnapshot, error)
	CompareAndSwap(context.Context, uint64, BackendSnapshot) (bool, error)
	Health() domain.RuntimeStateHealth
}

// BackendSnapshot is the availability facet of the runtime document. Adapters
// preserve other runtime facets when committing it. Empty scope tombstones and
// event receipts must survive restart to prevent stale recovery and event replay.
type BackendSnapshot struct {
	Revision uint64         `json:"revision,omitempty"`
	Scopes   []ScopeState   `json:"scopes,omitempty"`
	Events   []EventReceipt `json:"events,omitempty"`
}

type ScopeState struct {
	Scope        domain.AvailabilityScope         `json:"scope"`
	Revision     uint64                           `json:"revision,omitempty"`
	Observations []domain.AvailabilityObservation `json:"observations,omitempty"`
}

type EventReceipt struct {
	EventID     string                         `json:"eventId"`
	Observation domain.AvailabilityObservation `json:"observation"`
}

// ScopeGuard is captured before probing one exact scope. Broader or narrower
// scopes do not share its revision.
type ScopeGuard struct {
	Scope    domain.AvailabilityScope
	Revision uint64
}

// CloneSnapshot lets adapters retain and return independent values.
func CloneSnapshot(state BackendSnapshot) BackendSnapshot {
	out := state
	out.Scopes = append([]ScopeState(nil), state.Scopes...)
	for i := range out.Scopes {
		out.Scopes[i].Observations = append([]domain.AvailabilityObservation(nil), state.Scopes[i].Observations...)
	}
	out.Events = append([]EventReceipt(nil), state.Events...)
	return out
}

// memoryBackend keeps NewStore's process-local compatibility behavior while
// exercising the same mutation boundary as durable adapters.
type memoryBackend struct {
	mu    sync.Mutex
	state BackendSnapshot
}

func (b *memoryBackend) Snapshot(ctx context.Context) (BackendSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return BackendSnapshot{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return CloneSnapshot(b.state), nil
}

func (b *memoryBackend) CompareAndSwap(ctx context.Context, revision uint64, next BackendSnapshot) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state.Revision != revision {
		return false, nil
	}
	b.state = CloneSnapshot(next)
	return true, nil
}

func (b *memoryBackend) Health() domain.RuntimeStateHealth {
	return domain.RuntimeStateHealth{Backend: "memory", Ready: true, SingleNodeReady: true, Enforcement: "process_local"}
}
