package app

import (
	"testing"

	"routenest/internal/dispatch"
	"routenest/internal/egress"
)

func TestDispatchEgressSpecPreservesFallbackTransport(t *testing.T) {
	tests := []struct {
		name    string
		attempt dispatch.CandidateAttempt
		want    string
	}{
		{
			name: "fallback direct inherits global transport",
			attempt: dispatch.CandidateAttempt{
				EgressMode:   string(egress.ModeDirect),
				EgressSource: "fallback-direct",
			},
		},
		{
			name: "explicit provider direct uses isolated transport",
			attempt: dispatch.CandidateAttempt{
				EgressMode:   string(egress.ModeDirect),
				EgressSource: "provider-direct",
			},
			want: "direct",
		},
		{
			name: "resolved proxy is retained",
			attempt: dispatch.CandidateAttempt{
				EgressMode:     string(egress.ModeProfile),
				EgressSource:   "provider-override",
				EgressProxyURL: "http://127.0.0.1:8080",
			},
			want: "http://127.0.0.1:8080",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := dispatchEgressSpec(test.attempt); got != test.want {
				t.Fatalf("dispatchEgressSpec() = %q, want %q", got, test.want)
			}
		})
	}
}
