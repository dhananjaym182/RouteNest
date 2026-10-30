package egress

import "time"

// Retry policy helpers: administrator-configured fallback breadth.
// Zero values always mean "historic built-in behavior", so an unconfigured
// instance routes exactly like before this feature.

// FailoverModelCap bounds model attempts in the cline→zen failover loop.
func FailoverModelCap() int {
	if n := ConfigProvider().Retry.MaxFailoverModels; n > 0 {
		return n
	}
	return 3
}

// RetryRoundSettings returns the fallback-chain pass count (>=1), the account
// attempt cap per model per pass (0 = unlimited), and the wait between passes.
func RetryRoundSettings() (rounds, credCap int, interval time.Duration) {
	r := ConfigProvider().Retry
	rounds = r.MaxRounds
	if rounds < 1 {
		rounds = 1
	}
	if r.MaxCredentialsPerRound > 0 {
		credCap = r.MaxCredentialsPerRound
	}
	if r.MaxIntervalSeconds > 0 {
		interval = time.Duration(r.MaxIntervalSeconds) * time.Second
	}
	return rounds, credCap, interval
}
