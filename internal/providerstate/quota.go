package providerstate

import (
	"time"

	"routenest/internal/domain"
)

func ConfiguredQuotaResetAt(now time.Time, window string) time.Time {
	now = now.UTC()
	switch window {
	case "daily":
		year, month, day := now.Date()
		return time.Date(year, month, day+1, 0, 0, 0, 0, time.UTC)
	case "monthly":
		year, month, _ := now.Date()
		return time.Date(year, month+1, 1, 0, 0, 0, 0, time.UTC)
	default:
		return time.Time{}
	}
}

func UnknownQuotaSnapshot() domain.QuotaSnapshot {
	return domain.QuotaSnapshot{Capability: domain.CapabilityUnknown}
}

func ConfiguredQuotaSnapshot(now time.Time, window string, limit, used int64) domain.QuotaSnapshot {
	resetAt := ConfiguredQuotaResetAt(now, window)
	if limit <= 0 || resetAt.IsZero() {
		return UnknownQuotaSnapshot()
	}
	remaining := limit - used
	if remaining < 0 {
		remaining = 0
	}
	return domain.QuotaSnapshot{
		Capability: domain.CapabilitySupported,
		Limit:      &limit, Used: &used, Remaining: &remaining,
		ResetAt: resetAt, Window: window, Source: "configured_window",
		Confidence: domain.ConfidenceExplicit,
	}
}
