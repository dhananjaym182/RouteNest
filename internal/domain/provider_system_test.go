package domain

import (
	"testing"
	"time"
)

func TestProviderSystemIdentifiers(t *testing.T) {
	tests := []struct {
		name string
		id   interface{ Valid() bool }
		want bool
	}{
		{name: "provider definition", id: ProviderDefinitionID("opencode-zen"), want: true},
		{name: "provider instance", id: ProviderInstanceID("prov_custom.1"), want: true},
		{name: "credential", id: CredentialID("cline:account-1"), want: true},
		{name: "empty definition", id: ProviderDefinitionID(""), want: false},
		{name: "surrounding whitespace", id: ProviderInstanceID(" provider "), want: false},
		{name: "embedded whitespace", id: CredentialID("account one"), want: false},
		{name: "control character", id: ProviderDefinitionID("bad\nprovider"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.id.Valid(); got != tt.want {
				t.Fatalf("Valid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCapabilityStateValidation(t *testing.T) {
	for _, state := range []CapabilityState{CapabilitySupported, CapabilityUnsupported, CapabilityUnknown} {
		if !state.Valid() {
			t.Errorf("state %q must be valid", state)
		}
	}
	if CapabilityState("enabled").Valid() {
		t.Fatal("arbitrary capability state must be invalid")
	}
}

func TestOfferingKeyIsScopedByProvider(t *testing.T) {
	cline := OfferingKey{ProviderID: "cline", ModelID: "shared/model"}
	zen := OfferingKey{ProviderID: "opencode", ModelID: "shared/model"}
	if !cline.Valid() || !zen.Valid() {
		t.Fatalf("valid offering keys rejected: %+v %+v", cline, zen)
	}
	if cline == zen {
		t.Fatal("the same model ID from two providers must be different offerings")
	}
	if (OfferingKey{ProviderID: "cline"}).Valid() {
		t.Fatal("offering without a model ID must be invalid")
	}
}

func TestAvailabilityScopeAndObservationValidation(t *testing.T) {
	scope := AvailabilityScope{
		ProviderID:   "cline",
		CredentialID: "cline:account-1",
		ModelID:      "cline-free/model",
	}
	if !scope.Valid() {
		t.Fatalf("scope rejected: %+v", scope)
	}
	if (AvailabilityScope{ModelID: "model"}).Valid() {
		t.Fatal("scope without provider must be invalid")
	}

	obs := AvailabilityObservation{
		Kind:       ObservationQuotaExhausted,
		Scope:      scope,
		Until:      time.Date(2026, 11, 4, 1, 0, 0, 0, time.UTC),
		ResetAt:    time.Date(2026, 11, 4, 1, 0, 0, 0, time.UTC),
		Source:     "body",
		Confidence: ConfidenceExplicit,
	}
	if !obs.Valid() {
		t.Fatalf("observation rejected: %+v", obs)
	}
	obs.Confidence = EvidenceConfidence("certain")
	if obs.Valid() {
		t.Fatal("observation with an invalid confidence must be rejected")
	}
}

func TestAvailabilityObservationKinds(t *testing.T) {
	for _, kind := range []ObservationKind{
		ObservationRateLimited,
		ObservationQuotaExhausted,
		ObservationQuotaReset,
		ObservationAuthExpired,
		ObservationModelGone,
		ObservationTransientFailure,
		ObservationRecovered,
	} {
		if !kind.Valid() {
			t.Errorf("kind %q must be valid", kind)
		}
	}
	if ObservationKind("busy-ish").Valid() {
		t.Fatal("arbitrary observation kind must be invalid")
	}
}
