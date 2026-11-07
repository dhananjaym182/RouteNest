package syncstate

import (
	"time"

	"routenest/internal/domain"
)

// RuntimeSchemaVersion is the first additive provider runtime document version.
const RuntimeSchemaVersion = 1

// RuntimeState is the durable runtime partition for one provider. It contains
// storage DTOs only; budget policy and observation recovery belong to their
// caller modules. A nil Runtime on State identifies a legacy document.
//
// Collection fields use omitzero so absent collections stay absent while an
// explicitly empty collection remains authoritative across save/load cycles.
type RuntimeState struct {
	SchemaVersion       int                                `json:"schemaVersion,omitempty"`
	Budget              *BudgetState                       `json:"budget,omitempty"`
	ObservationRevision uint64                             `json:"observationRevision,omitempty"`
	Observations        []VersionedObservation             `json:"observations,omitzero"`
	Tombstones          []ObservationTombstone             `json:"tombstones,omitzero"`
	ObservationEvents   map[string]ObservationEventReceipt `json:"observationEvents,omitzero"`
}

// BudgetState keeps rolling RPM samples per logical model and UTC counters.
// Daily keys use YYYY-MM-DD; Tokens may contain both YYYY-MM-DD and YYYY-MM.
// Receipt maps are keyed by the stable attempt/event IDs chosen by callers.
type BudgetState struct {
	RPM         map[string][]time.Time       `json:"rpm,omitzero"`
	Daily       map[string]int64             `json:"daily,omitzero"`
	Tokens      map[string]int64             `json:"tokens,omitzero"`
	Admissions  map[string]AdmissionReceipt  `json:"admissions,omitzero"`
	TokenEvents map[string]TokenEventReceipt `json:"tokenEvents,omitzero"`
}

// BudgetLimits records the policy associated with an admission when an adapter
// needs to compare the payload of a replay with the original command.
type BudgetLimits struct {
	RPM         int64  `json:"rpm,omitempty"`
	Daily       int64  `json:"daily,omitempty"`
	Tokens      int64  `json:"tokens,omitempty"`
	TokenWindow string `json:"tokenWindow,omitempty"`
}

// AdmissionReceipt retains original attribution and accounting windows so
// delayed usage and replay cannot move an attempt to a different provider,
// logical model, or UTC window. Optional outcome/fingerprint fields support
// adapters that persist both admitted and denied commands.
type AdmissionReceipt struct {
	ProviderID  string       `json:"providerId,omitempty"`
	ModelID     string       `json:"modelId,omitempty"`
	AttemptID   string       `json:"attemptId,omitempty"`
	At          time.Time    `json:"at,omitzero"`
	Day         string       `json:"day,omitempty"`
	Month       string       `json:"month,omitempty"`
	TokenWindow string       `json:"tokenWindow,omitempty"`
	Limits      BudgetLimits `json:"limits,omitzero"`
	Allowed     *bool        `json:"allowed,omitempty"`
	Reason      string       `json:"reason,omitempty"`
	Usage       int64        `json:"usage,omitempty"`
	ResetAt     time.Time    `json:"resetAt,omitzero"`
	Fingerprint string       `json:"fingerprint,omitempty"`
}

// TokenEventReceipt represents one final usage event already accounted for.
type TokenEventReceipt struct {
	ProviderID  string    `json:"providerId,omitempty"`
	AttemptID   string    `json:"attemptId,omitempty"`
	EventID     string    `json:"eventId,omitempty"`
	Tokens      int64     `json:"tokens"`
	At          time.Time `json:"at,omitzero"`
	Fingerprint string    `json:"fingerprint,omitempty"`
}

// VersionedObservation stores normalized evidence and its exact-scope revision.
type VersionedObservation struct {
	Observation domain.AvailabilityObservation `json:"observation"`
	Revision    uint64                         `json:"revision"`
}

// ObservationTombstone retains revisions after an exact scope or kind clears.
// An empty Kind identifies a scope-wide revision rather than a single kind.
type ObservationTombstone struct {
	Scope    domain.AvailabilityScope `json:"scope"`
	Kind     domain.ObservationKind   `json:"kind,omitempty"`
	Revision uint64                   `json:"revision"`
	At       time.Time                `json:"at,omitzero"`
}

// ObservationEventReceipt preserves replay identity independently of whether
// its observation is active, expired, or cleared.
type ObservationEventReceipt struct {
	EventID     string                         `json:"eventId,omitempty"`
	Observation domain.AvailabilityObservation `json:"observation,omitzero"`
	Scope       domain.AvailabilityScope       `json:"scope"`
	Kind        domain.ObservationKind         `json:"kind,omitempty"`
	Revision    uint64                         `json:"revision,omitempty"`
	At          time.Time                      `json:"at,omitzero"`
	Fingerprint string                         `json:"fingerprint,omitempty"`
}

// Clone returns a detached runtime partition, preserving nil versus empty.
func (state RuntimeState) Clone() RuntimeState {
	if state.Budget != nil {
		budget := state.Budget.Clone()
		state.Budget = &budget
	}
	state.Observations = cloneSlice(state.Observations)
	state.Tombstones = cloneSlice(state.Tombstones)
	state.ObservationEvents = cloneMap(state.ObservationEvents)
	return state
}

// Clone returns detached rolling samples, counters, and receipt maps.
func (state BudgetState) Clone() BudgetState {
	if state.RPM != nil {
		rpm := make(map[string][]time.Time, len(state.RPM))
		for modelID, samples := range state.RPM {
			rpm[modelID] = cloneSlice(samples)
		}
		state.RPM = rpm
	}
	state.Daily = cloneMap(state.Daily)
	state.Tokens = cloneMap(state.Tokens)
	if state.Admissions != nil {
		admissions := make(map[string]AdmissionReceipt, len(state.Admissions))
		for attemptID, receipt := range state.Admissions {
			if receipt.Allowed != nil {
				allowed := *receipt.Allowed
				receipt.Allowed = &allowed
			}
			admissions[attemptID] = receipt
		}
		state.Admissions = admissions
	}
	state.TokenEvents = cloneMap(state.TokenEvents)
	return state
}

func cloneSlice[T any](in []T) []T {
	if in == nil {
		return nil
	}
	return append(make([]T, 0, len(in)), in...)
}

func cloneMap[T any](in map[string]T) map[string]T {
	if in == nil {
		return nil
	}
	out := make(map[string]T, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
