package routing

import (
	"testing"
	"time"
)

func TestCharacterizationClineQuotaResetBody(t *testing.T) {
	tests := []struct {
		name string
		body string
		want time.Duration
	}{
		{name: "hours and minutes", body: `{"error":"quota","message":"Try again in 1h 1m"}`, want: 61 * time.Minute},
		{name: "minutes after zero hours", body: `Try again in 0h 15m`, want: 15 * time.Minute},
		{name: "unparseable falls back", body: `quota exceeded`, want: time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := time.Now()
			got := parseCooldownUntil(tt.body)
			after := time.Now()
			if got.Before(before.Add(tt.want)) || got.After(after.Add(tt.want)) {
				t.Fatalf("reset = %s, want within [%s, %s]", got, before.Add(tt.want), after.Add(tt.want))
			}
		})
	}
}
