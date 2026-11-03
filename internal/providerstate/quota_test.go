package providerstate

import (
	"testing"
	"time"

	"routenest/internal/domain"
)

func TestConfiguredQuotaWindowReset(t *testing.T) {
	now := time.Date(2026, 11, 4, 23, 30, 0, 0, time.FixedZone("local", 5*60*60))
	if got := ConfiguredQuotaResetAt(now, "daily"); !got.Equal(time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("daily reset = %s", got)
	}
	if got := ConfiguredQuotaResetAt(now, "monthly"); !got.Equal(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("monthly reset = %s", got)
	}
	if got := ConfiguredQuotaResetAt(now, "unknown"); !got.IsZero() {
		t.Fatalf("unknown reset = %s, want zero", got)
	}
}

func TestConfiguredQuotaSnapshot(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	snapshot := ConfiguredQuotaSnapshot(now, "daily", 1000, 250)
	if snapshot.Capability != domain.CapabilitySupported || snapshot.Limit == nil || *snapshot.Limit != 1000 || snapshot.Used == nil || *snapshot.Used != 250 || snapshot.Remaining == nil || *snapshot.Remaining != 750 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if snapshot.ResetAt.IsZero() || snapshot.Source != "configured_window" || snapshot.Confidence != domain.ConfidenceExplicit {
		t.Fatalf("reset evidence = %+v", snapshot)
	}

	unknown := UnknownQuotaSnapshot()
	if unknown.Capability != domain.CapabilityUnknown || !unknown.ResetAt.IsZero() || unknown.Limit != nil || unknown.Remaining != nil {
		t.Fatalf("unknown snapshot fabricated data: %+v", unknown)
	}
}
