package providerstate

import (
	"context"
	"time"

	"routenest/internal/domain"
)

type RecoveryProbe func(context.Context, domain.AvailabilityScope) bool

// RecoverDue runs one deterministic recovery pass. Failed probes are deferred;
// successful probes clear only their exact scope.
func RecoverDue(ctx context.Context, now time.Time, store *Store, retryDelay time.Duration, probe RecoveryProbe) int {
	if store == nil || probe == nil {
		return 0
	}
	if retryDelay <= 0 {
		retryDelay = 30 * time.Second
	}
	recovered := 0
	for _, scope := range store.DueScopes(now) {
		if ctx.Err() != nil {
			return recovered
		}
		if probe(ctx, scope) {
			_ = store.Recover(scope)
			recovered++
			continue
		}
		_ = store.Defer(scope, now.Add(retryDelay))
	}
	return recovered
}

// RunRecovery consumes externally owned ticks so tests and production can use
// deterministic scheduling without hidden goroutines in providerstate.
func RunRecovery(ctx context.Context, ticks <-chan time.Time, store *Store, retryDelay time.Duration, probe RecoveryProbe) {
	for {
		select {
		case <-ctx.Done():
			return
		case now, ok := <-ticks:
			if !ok {
				return
			}
			RecoverDue(ctx, now, store, retryDelay, probe)
		}
	}
}
