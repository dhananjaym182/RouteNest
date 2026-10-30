package egress

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"cline-go-proxy/internal/config"
	"cline-go-proxy/internal/dispatch"
)

// ConfigProvider supplies the live root proxy policy. The root composition
// replaces the default once; the default keeps this leaf package testable.
var ConfigProvider = config.DefaultProxy

// APIError is an upstream API failure with its HTTP status and original
// response message preserved for routing and policy decisions.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API %d: %s", e.StatusCode, e.Message)
}

// NewAPIError constructs an upstream API error.
func NewAPIError(status int, message string) *APIError {
	return &APIError{StatusCode: status, Message: message}
}

// ErrorStatus extracts an API status through any wrapping layers.
func ErrorStatus(err error) int {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode
	}
	return 0
}

// requestErrorStop marks a fallback failure that a stop rule declared
// terminal. Chain loops abort on it and the outermost router unwraps it before
// reporting the underlying API error to the client.
type requestErrorStop struct{ err error }

func (e *requestErrorStop) Error() string { return e.err.Error() }
func (e *requestErrorStop) Unwrap() error { return e.err }

// WrapStop marks err as terminal for provider and model fallback loops.
func WrapStop(err error) error {
	return &requestErrorStop{err: err}
}

// StopCause extracts the original failure through any wrapping layers.
func StopCause(err error) (error, bool) {
	var stopErr *requestErrorStop
	if !errors.As(err, &stopErr) {
		return nil, false
	}
	return stopErr.err, true
}

// RuleDecision evaluates configured rules against err. Non-API errors and
// unmatched failures report matched=false.
func RuleDecision(err error) (stop, cooldown, matched bool) {
	if _, already := err.(*requestErrorStop); already {
		return false, false, false
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false, false, false
	}
	action, ok := ConfigProvider().MatchRequestError(apiErr.StatusCode, apiErr.Message)
	if !ok {
		return false, false, false
	}
	switch action {
	case config.ErrorActionStop:
		return true, false, true
	case config.ErrorActionStopAndCooldown:
		return true, true, true
	case config.ErrorActionContinueAndCooldown:
		return false, true, true
	default:
		return false, false, true
	}
}

// CooldownUntil classifies an upstream failure into a provider cooldown
// window. Transient failures (408/5xx) honor Retry.TransientCooldownSeconds:
// >0 overrides the built-in window, -1 disables it, and 0 keeps the default.
func CooldownUntil(status int, retryAfterHeader, body string) time.Time {
	until := dispatch.ProviderCooldownUntil(status, retryAfterHeader, body)
	t := ConfigProvider().Retry.TransientCooldownSeconds
	if t == 0 || !isTransientUpstreamStatus(status) {
		return until
	}
	if t < 0 {
		return time.Time{}
	}
	return time.Now().Add(time.Duration(t) * time.Second)
}

func isTransientUpstreamStatus(status int) bool {
	return status == http.StatusRequestTimeout || (status >= 500 && status < 600)
}

// RuleCooldownUntil reuses the provider failure classification when it has an
// opinion about the status; otherwise it applies a short two-minute penalty.
func RuleCooldownUntil(status int) time.Time {
	if until := CooldownUntil(status, "", ""); !until.IsZero() {
		return until
	}
	return time.Now().Add(2 * time.Minute)
}

// LogRuleHit keeps rule-driven decisions distinguishable from built-in ones.
func LogRuleHit(action string, status int, subject string) {
	log.Printf("  error rule: action=%s status=%d subject=%s", action, status, subject)
}
