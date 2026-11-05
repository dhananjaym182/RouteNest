// Package providerstate normalizes provider availability, quota and recovery
// evidence. It owns no provider protocol, credential store, or routing policy.
package providerstate

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"routenest/internal/domain"
)

type HTTPFailure struct {
	Scope      domain.AvailabilityScope
	StatusCode int
	Header     http.Header
	Body       string
}

func ParseRetryAfterAt(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		if duration := at.Sub(now); duration > 0 {
			return duration
		}
	}
	return 0
}

func parseResetHeaderAt(header http.Header, now time.Time) (time.Time, bool) {
	for _, name := range []string{"X-Ratelimit-Reset", "X-RateLimit-Reset", "RateLimit-Reset"} {
		value := strings.TrimSpace(header.Get(name))
		if value == "" {
			continue
		}
		if raw, err := strconv.ParseInt(value, 10, 64); err == nil {
			var at time.Time
			if raw >= 1_000_000_000_000 {
				at = time.UnixMilli(raw)
			} else if raw >= 1_000_000_000 {
				at = time.Unix(raw, 0)
			}
			if at.After(now) {
				return at, true
			}
		}
		if at, err := http.ParseTime(value); err == nil && at.After(now) {
			return at, true
		}
	}
	return time.Time{}, false
}

func containsAny(value string, terms ...string) bool {
	value = strings.ToLower(value)
	for _, term := range terms {
		if strings.Contains(value, term) {
			return true
		}
	}
	return false
}

func quotaBody(body string) bool {
	return containsAny(body, "quota", "insufficient", "billing", "payment", "resourceexhausted", "free usage limit")
}

func rateLimitBody(body string) bool {
	return quotaBody(body) || containsAny(body, "rate limit", "too many requests", "limit reached", "overloaded", "busy")
}

var modelGonePattern = regexp.MustCompile(`(?i)model[\s_-]*(not[\s_-]*found|does\s+not\s+exist|no\s+such|unknown|invalid)|(invalid|unknown|no\s+such)[\s_-]*model`)

func observation(kind domain.ObservationKind, failure HTTPFailure, until, resetAt time.Time, source string, confidence domain.EvidenceConfidence) domain.AvailabilityObservation {
	return domain.AvailabilityObservation{
		Kind: kind, Scope: failure.Scope, Until: until, ResetAt: resetAt,
		Source: source, Confidence: confidence,
	}
}

func DetectStandardFailure(failure HTTPFailure) []domain.AvailabilityObservation {
	return DetectStandardFailureAt(failure, time.Now())
}

func DetectStandardFailureAt(failure HTTPFailure, now time.Time) []domain.AvailabilityObservation {
	if (failure.StatusCode == http.StatusBadRequest || failure.StatusCode == http.StatusNotFound) && modelGonePattern.MatchString(failure.Body) {
		return []domain.AvailabilityObservation{observation(domain.ObservationModelGone, failure, time.Time{}, time.Time{}, "body", domain.ConfidenceExplicit)}
	}

	limited := failure.StatusCode == http.StatusTooManyRequests ||
		((failure.StatusCode == http.StatusServiceUnavailable || failure.StatusCode == http.StatusBadGateway || failure.StatusCode == http.StatusForbidden || failure.StatusCode == http.StatusMisdirectedRequest) && rateLimitBody(failure.Body))
	if limited {
		kind := domain.ObservationRateLimited
		if quotaBody(failure.Body) {
			kind = domain.ObservationQuotaExhausted
		}
		until := time.Time{}
		resetAt := time.Time{}
		source := "fallback"
		confidence := domain.ConfidenceFallback
		if reset, ok := parseResetHeaderAt(failure.Header, now); ok {
			until, source, confidence = reset, "reset-header", domain.ConfidenceExplicit
			if kind == domain.ObservationQuotaExhausted {
				resetAt = reset
			}
		} else if retry := ParseRetryAfterAt(failure.Header.Get("Retry-After"), now); retry > 0 {
			if retry > 30*time.Minute {
				retry = 30 * time.Minute
			}
			until, source, confidence = now.Add(retry), "retry-after", domain.ConfidenceExplicit
			if kind == domain.ObservationQuotaExhausted {
				resetAt = until
			}
		} else {
			duration := 30 * time.Second
			switch failure.StatusCode {
			case http.StatusTooManyRequests:
				duration = 2 * time.Minute
			case http.StatusForbidden:
				duration = 30 * time.Minute
			}
			until = now.Add(duration)
		}
		return []domain.AvailabilityObservation{observation(kind, failure, until, resetAt, source, confidence)}
	}

	if failure.StatusCode == http.StatusUnauthorized || failure.StatusCode == http.StatusForbidden {
		return []domain.AvailabilityObservation{observation(domain.ObservationAuthExpired, failure, time.Time{}, time.Time{}, "status", domain.ConfidenceExplicit)}
	}
	if failure.StatusCode >= 500 && failure.StatusCode < 600 {
		return []domain.AvailabilityObservation{observation(domain.ObservationTransientFailure, failure, now.Add(30*time.Second), time.Time{}, "status", domain.ConfidenceInferred)}
	}
	return nil
}

var tryAgainPattern = regexp.MustCompile(`(?i)try\s+again\s+in\s+([^"}\n]+)`)
var hoursPattern = regexp.MustCompile(`(?i)(\d+)\s*h`)
var minutesPattern = regexp.MustCompile(`(?i)(\d+)\s*m`)

func parseClineResetAt(body string, now time.Time) (time.Time, bool) {
	match := tryAgainPattern.FindStringSubmatch(body)
	if len(match) < 2 {
		return time.Time{}, false
	}
	var duration time.Duration
	if hours := hoursPattern.FindStringSubmatch(match[1]); len(hours) == 2 {
		value, _ := strconv.Atoi(hours[1])
		duration += time.Duration(value) * time.Hour
	}
	if minutes := minutesPattern.FindStringSubmatch(match[1]); len(minutes) == 2 {
		value, _ := strconv.Atoi(minutes[1])
		duration += time.Duration(value) * time.Minute
	}
	if duration <= 0 {
		return time.Time{}, false
	}
	return now.Add(duration), true
}

func DetectClineFailure(failure HTTPFailure) []domain.AvailabilityObservation {
	return DetectClineFailureAt(failure, time.Now())
}

func DetectClineFailureAt(failure HTTPFailure, now time.Time) []domain.AvailabilityObservation {
	if failure.StatusCode == http.StatusTooManyRequests {
		if reset, ok := parseClineResetAt(failure.Body, now); ok {
			return []domain.AvailabilityObservation{observation(domain.ObservationQuotaExhausted, failure, reset, reset, "body", domain.ConfidenceExplicit)}
		}
		return []domain.AvailabilityObservation{observation(domain.ObservationQuotaExhausted, failure, now.Add(time.Hour), time.Time{}, "fallback", domain.ConfidenceFallback)}
	}
	if failure.StatusCode == http.StatusPaymentRequired {
		return []domain.AvailabilityObservation{observation(domain.ObservationQuotaExhausted, failure, now.Add(30*time.Minute), time.Time{}, "fallback", domain.ConfidenceFallback)}
	}
	return DetectStandardFailureAt(failure, now)
}

func DetectZenFailure(failure HTTPFailure) []domain.AvailabilityObservation {
	return DetectZenFailureAt(failure, time.Now())
}

func DetectZenFailureAt(failure HTTPFailure, now time.Time) []domain.AvailabilityObservation {
	// OpenCode Zen uses 503 as a direct free-tier overload signal even when the
	// response body carries no portable rate-limit wording.
	if failure.StatusCode == http.StatusServiceUnavailable && !rateLimitBody(failure.Body) {
		failure.Body = failure.Body + " rate limit"
	}
	return DetectStandardFailureAt(failure, now)
}
