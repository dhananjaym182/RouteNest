package domain

import (
	"strings"
	"time"
	"unicode"
)

// ProviderDefinitionID identifies immutable provider metadata and adapter
// capabilities. It is not a runtime instance ID.
type ProviderDefinitionID string

// ProviderInstanceID identifies one configured, routable upstream.
type ProviderInstanceID string

// CredentialID identifies one provider credential/account/session globally.
type CredentialID string

func validSystemID(value string) bool {
	if value == "" || value != strings.TrimSpace(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (id ProviderDefinitionID) Valid() bool { return validSystemID(string(id)) }
func (id ProviderInstanceID) Valid() bool   { return validSystemID(string(id)) }
func (id CredentialID) Valid() bool         { return validSystemID(string(id)) }

// CapabilityState distinguishes a capability that is known not to exist from
// one whose support has not yet been determined.
type CapabilityState string

const (
	CapabilitySupported   CapabilityState = "supported"
	CapabilityUnsupported CapabilityState = "unsupported"
	CapabilityUnknown     CapabilityState = "unknown"
)

func (state CapabilityState) Valid() bool {
	switch state {
	case CapabilitySupported, CapabilityUnsupported, CapabilityUnknown:
		return true
	default:
		return false
	}
}

// OfferingKey identifies one provider instance's offering of a logical model.
// The same ModelID can therefore exist independently on multiple providers.
type OfferingKey struct {
	ProviderID ProviderInstanceID `json:"providerId"`
	ModelID    string             `json:"modelId"`
}

func (key OfferingKey) Valid() bool {
	return key.ProviderID.Valid() && key.ModelID != "" && key.ModelID == strings.TrimSpace(key.ModelID)
}

// AvailabilityScope identifies the exact subject of a runtime observation.
// ProviderID is required; credential, model and egress scopes are optional and
// may be combined (for example, one model on one credential).
type AvailabilityScope struct {
	ProviderID   ProviderInstanceID `json:"providerId"`
	CredentialID CredentialID       `json:"credentialId,omitempty"`
	ModelID      string             `json:"modelId,omitempty"`
	EgressID     string             `json:"egressId,omitempty"`
}

func (scope AvailabilityScope) Valid() bool {
	if !scope.ProviderID.Valid() {
		return false
	}
	if scope.CredentialID != "" && !scope.CredentialID.Valid() {
		return false
	}
	if scope.ModelID != strings.TrimSpace(scope.ModelID) {
		return false
	}
	if scope.EgressID != "" && !validSystemID(scope.EgressID) {
		return false
	}
	return true
}

// ObservationKind is a normalized provider-runtime fact. Adapters produce
// observations; providerstate decides how they affect eligibility and recovery.
type ObservationKind string

const (
	ObservationRateLimited      ObservationKind = "rate_limited"
	ObservationQuotaExhausted   ObservationKind = "quota_exhausted"
	ObservationQuotaReset       ObservationKind = "quota_reset"
	ObservationAuthExpired      ObservationKind = "auth_expired"
	ObservationModelGone        ObservationKind = "model_gone"
	ObservationTransientFailure ObservationKind = "transient_failure"
	ObservationRecovered        ObservationKind = "recovered"
)

func (kind ObservationKind) Valid() bool {
	switch kind {
	case ObservationRateLimited, ObservationQuotaExhausted, ObservationQuotaReset,
		ObservationAuthExpired, ObservationModelGone, ObservationTransientFailure,
		ObservationRecovered:
		return true
	default:
		return false
	}
}

// EvidenceConfidence records whether an availability fact came directly from
// an upstream signal, was inferred, or uses a compatibility fallback.
type EvidenceConfidence string

const (
	ConfidenceExplicit EvidenceConfidence = "explicit"
	ConfidenceInferred EvidenceConfidence = "inferred"
	ConfidenceFallback EvidenceConfidence = "fallback"
)

func (confidence EvidenceConfidence) Valid() bool {
	switch confidence {
	case ConfidenceExplicit, ConfidenceInferred, ConfidenceFallback:
		return true
	default:
		return false
	}
}

// AvailabilityObservation is a normalized fact learned from an upstream
// response, catalogue, configured quota window, or health probe. Until is an
// eligibility cooldown; ResetAt is a known quota reset. They are deliberately
// separate because a fallback cooldown is not proof of an exact quota reset.
type AvailabilityObservation struct {
	Kind       ObservationKind    `json:"kind"`
	Scope      AvailabilityScope  `json:"scope"`
	Until      time.Time          `json:"until,omitempty"`
	ResetAt    time.Time          `json:"resetAt,omitempty"`
	Source     string             `json:"source"`
	Confidence EvidenceConfidence `json:"confidence"`
}

func (observation AvailabilityObservation) Valid() bool {
	return observation.Kind.Valid() && observation.Scope.Valid() &&
		observation.Confidence.Valid() && observation.Source != "" &&
		observation.Source == strings.TrimSpace(observation.Source)
}
