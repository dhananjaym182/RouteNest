package providerstate

import (
	"context"
	"errors"
	"time"

	"routenest/internal/domain"
)

type RecoveryProbe func(context.Context, domain.AvailabilityScope) bool

// RecoverDue retains the historical count signature. Only durable successful
// recovery contributes to the count; errors remain available via LastError.
// New callers should use RecoverDueWithError to receive failures directly.
func RecoverDue(ctx context.Context, now time.Time, store *Store, retryDelay time.Duration, probe RecoveryProbe) int {
	recovered, err := RecoverDueWithError(ctx, now, store, retryDelay, probe)
	store.recordError(err)
	return recovered
}

// RecoverDueWithError runs one deterministic recovery pass. Both successful and
// failed probes commit against the revision captured with the due evidence.
func RecoverDueWithError(ctx context.Context, now time.Time, store *Store, retryDelay time.Duration, probe RecoveryProbe) (int, error) {
	if store == nil || probe == nil {
		return 0, nil
	}
	if retryDelay <= 0 {
		retryDelay = 30 * time.Second
	}
	guards, err := store.DueGuards(ctx, now)
	if err != nil {
		return 0, err
	}
	recovered := 0
	var failures error
	for _, guard := range guards {
		if err := ctx.Err(); err != nil {
			return recovered, errors.Join(failures, err)
		}
		success := probe(ctx, guard.Scope)
		if err := ctx.Err(); err != nil {
			return recovered, errors.Join(failures, err)
		}
		if success {
			if err := store.RecoverGuard(guard); err != nil {
				failures = errors.Join(failures, err)
			} else {
				recovered++
			}
			continue
		}
		if err := store.DeferGuard(guard, now.Add(retryDelay)); err != nil {
			failures = errors.Join(failures, err)
		}
	}
	store.recordError(failures)
	return recovered, failures
}

// RunRecovery consumes externally owned ticks without creating a goroutine.
// Persistence failures remain visible through LastError and Health.
func RunRecovery(ctx context.Context, ticks <-chan time.Time, store *Store, retryDelay time.Duration, probe RecoveryProbe) {
	for {
		select {
		case <-ctx.Done():
			return
		case now, ok := <-ticks:
			if !ok {
				return
			}
			if _, err := RecoverDueWithError(ctx, now, store, retryDelay, probe); err != nil {
				store.recordError(err)
			}
		}
	}
}

// RunRecoveryWithError exposes a failed durable recovery to the scheduler owner.
func RunRecoveryWithError(ctx context.Context, ticks <-chan time.Time, store *Store, retryDelay time.Duration, probe RecoveryProbe) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now, ok := <-ticks:
			if !ok {
				return nil
			}
			if _, err := RecoverDueWithError(ctx, now, store, retryDelay, probe); err != nil {
				return err
			}
		}
	}
}
