package domain

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ModelRemovalRef identifies a synchronized offering, rather than an ID-wide
// logical model. Provider names are accepted only when they resolve uniquely.
type ModelRemovalRef struct {
	ID         string `json:"id"`
	Source     string `json:"source"`
	ProviderID string `json:"providerId,omitempty"`
	Provider   string `json:"provider,omitempty"`
}

type ModelRemovalError struct {
	Model ModelRemovalRef `json:"model"`
	Error string          `json:"error"`
}

type ModelRemovalResult struct {
	Removed []ModelRemovalRef   `json:"removed"`
	Skipped []ModelRemovalRef   `json:"skipped"`
	Errors  []ModelRemovalError `json:"errors"`
}

func (ref ModelRemovalRef) Normalize() (ModelRemovalRef, error) {
	ref.ID = strings.TrimSpace(ref.ID)
	ref.Source = strings.TrimSpace(ref.Source)
	ref.ProviderID = strings.TrimSpace(ref.ProviderID)
	ref.Provider = strings.TrimSpace(ref.Provider)
	if ref.Source == "cline" {
		ref.Source = "remote"
	}
	invalid := func(value string, limit int) bool {
		return len(value) > limit || !utf8.ValidString(value) || strings.ContainsFunc(value, unicode.IsControl)
	}
	if ref.ID == "" || invalid(ref.ID, 2048) || invalid(ref.Provider, 256) || invalid(ref.ProviderID, 128) {
		return ref, errors.New("invalid model or provider identity")
	}
	switch ref.Source {
	case "remote":
		if ref.ProviderID != "" && ref.ProviderID != "cline" || ref.Provider != "" {
			return ref, errors.New("Cline removal must use source remote and providerId cline or no providerId")
		}
	case "zen":
		if ref.ProviderID != "" && ref.ProviderID != "opencode" || ref.Provider != "" {
			return ref, errors.New("Zen removal must use source zen and providerId opencode or no providerId")
		}
	case "provider":
		if ref.ProviderID == "" && ref.Provider == "" || ref.ProviderID == "cline" || ref.ProviderID == "opencode" || ref.ProviderID != "" && !ProviderInstanceID(ref.ProviderID).Valid() {
			return ref, errors.New("custom provider removal requires a valid providerId or a unique provider name")
		}
	default:
		return ref, errors.New("source must be remote, zen, or provider")
	}
	return ref, nil
}
