package providerstate

import (
	"net/http"
	"testing"
	"time"

	"routenest/internal/domain"
)

func testScope() domain.AvailabilityScope {
	return domain.AvailabilityScope{ProviderID: "provider", CredentialID: "credential", ModelID: "model"}
}

func TestParseRetryAfterAt(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	if got := ParseRetryAfterAt("90", now); got != 90*time.Second {
		t.Fatalf("seconds = %s", got)
	}
	if got := ParseRetryAfterAt(now.Add(45*time.Second).Format(http.TimeFormat), now); got != 45*time.Second {
		t.Fatalf("HTTP date = %s", got)
	}
	for _, value := range []string{"", "0", "garbage", now.Add(-time.Minute).Format(http.TimeFormat)} {
		if got := ParseRetryAfterAt(value, now); got != 0 {
			t.Errorf("ParseRetryAfterAt(%q) = %s, want 0", value, got)
		}
	}
}

func TestDetectStandardFailureAt(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	reset := now.Add(10 * time.Minute)
	tests := []struct {
		name       string
		failure    HTTPFailure
		wantKind   domain.ObservationKind
		wantUntil  time.Time
		wantReset  time.Time
		wantSource string
	}{
		{
			name: "rate limit retry after",
			failure: HTTPFailure{Scope: testScope(), StatusCode: http.StatusTooManyRequests,
				Header: http.Header{"Retry-After": []string{"90"}}, Body: "rate limit"},
			wantKind: domain.ObservationRateLimited, wantUntil: now.Add(90 * time.Second), wantSource: "retry-after",
		},
		{
			name: "quota with explicit reset header",
			failure: HTTPFailure{Scope: testScope(), StatusCode: http.StatusTooManyRequests,
				Header: http.Header{"X-Ratelimit-Reset": []string{"1793794200"}}, Body: "quota exceeded"},
			wantKind: domain.ObservationQuotaExhausted, wantUntil: reset, wantReset: reset, wantSource: "reset-header",
		},
		{
			name: "model gone",
			failure: HTTPFailure{Scope: testScope(), StatusCode: http.StatusNotFound,
				Header: http.Header{}, Body: "no such model"},
			wantKind: domain.ObservationModelGone, wantSource: "body",
		},
		{
			name: "credential expired",
			failure: HTTPFailure{Scope: testScope(), StatusCode: http.StatusUnauthorized,
				Header: http.Header{}, Body: "invalid token"},
			wantKind: domain.ObservationAuthExpired, wantSource: "status",
		},
		{
			name: "transient server failure",
			failure: HTTPFailure{Scope: testScope(), StatusCode: http.StatusServiceUnavailable,
				Header: http.Header{}, Body: "unavailable"},
			wantKind: domain.ObservationTransientFailure, wantUntil: now.Add(30 * time.Second), wantSource: "status",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectStandardFailureAt(tt.failure, now)
			if len(got) != 1 {
				t.Fatalf("observations = %+v, want one", got)
			}
			obs := got[0]
			if obs.Kind != tt.wantKind || !obs.Until.Equal(tt.wantUntil) || !obs.ResetAt.Equal(tt.wantReset) || obs.Source != tt.wantSource {
				t.Fatalf("observation = %+v, want kind=%s until=%s reset=%s source=%s", obs, tt.wantKind, tt.wantUntil, tt.wantReset, tt.wantSource)
			}
			if !obs.Valid() {
				t.Fatalf("detector produced invalid observation: %+v", obs)
			}
		})
	}
}

func TestDetectStandardFailureIgnoresUnclassified421(t *testing.T) {
	got := DetectStandardFailureAt(HTTPFailure{Scope: testScope(), StatusCode: http.StatusMisdirectedRequest, Body: "wrong edge"}, time.Now())
	if len(got) != 0 {
		t.Fatalf("observations = %+v, want none", got)
	}
}

func TestDetectClineFailureAt(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		status     int
		body       string
		wantUntil  time.Time
		wantReset  time.Time
		confidence domain.EvidenceConfidence
	}{
		{name: "body reset", status: 429, body: `{"message":"Try again in 1h 1m"}`, wantUntil: now.Add(61 * time.Minute), wantReset: now.Add(61 * time.Minute), confidence: domain.ConfidenceExplicit},
		{name: "unknown reset fallback", status: 429, body: `{"error":"quota"}`, wantUntil: now.Add(time.Hour), confidence: domain.ConfidenceFallback},
		{name: "insufficient credits", status: 402, body: `insufficient credits`, wantUntil: now.Add(30 * time.Minute), confidence: domain.ConfidenceFallback},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectClineFailureAt(HTTPFailure{Scope: testScope(), StatusCode: tt.status, Body: tt.body}, now)
			if len(got) != 1 {
				t.Fatalf("observations = %+v, want one", got)
			}
			obs := got[0]
			if obs.Kind != domain.ObservationQuotaExhausted || !obs.Until.Equal(tt.wantUntil) || !obs.ResetAt.Equal(tt.wantReset) || obs.Confidence != tt.confidence {
				t.Fatalf("observation = %+v", obs)
			}
		})
	}
}
