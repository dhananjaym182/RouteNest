package desktopauth

import (
	"context"
	"sync"
)

type keyringExecutor struct {
	gate       chan struct{}
	flight     *keyringFlight
	mu         sync.Mutex
	unresolved map[credentialSlot]struct{}
}

type keyringFlight struct {
	done  chan struct{}
	value string
	err   error
}

func newKeyringExecutor() *keyringExecutor {
	executor := &keyringExecutor{
		gate:       make(chan struct{}, 1),
		unresolved: make(map[credentialSlot]struct{}),
	}
	executor.gate <- struct{}{}
	return executor
}

func (executor *keyringExecutor) acquire(ctx context.Context) bool {
	select {
	case <-executor.gate:
	case <-ctx.Done():
		return false
	}
	if ctx.Err() != nil {
		executor.release()
		return false
	}
	if executor.flight != nil {
		select {
		case <-executor.flight.done:
			executor.flight = nil
		default:
			executor.release()
			return false
		}
	}
	return true
}

func (executor *keyringExecutor) release() {
	executor.gate <- struct{}{}
}

func (executor *keyringExecutor) call(ctx context.Context, run func() (string, error)) (string, error) {
	if ctx.Err() != nil || executor.flight != nil {
		return "", ErrUnavailable
	}
	flight := &keyringFlight{done: make(chan struct{})}
	executor.flight = flight
	go func() {
		defer func() {
			if recover() != nil {
				flight.value, flight.err = "", ErrUnavailable
			}
			close(flight.done)
		}()
		if ctx.Err() != nil {
			flight.err = ErrUnavailable
			return
		}
		value, err := run()
		if ctx.Err() != nil {
			value, err = "", ErrUnavailable
		}
		flight.value, flight.err = value, err
	}()
	select {
	case <-flight.done:
		executor.flight = nil
		if ctx.Err() != nil {
			return "", ErrUnavailable
		}
		return flight.value, flight.err
	case <-ctx.Done():
		return "", ErrUnavailable
	}
}

func (executor *keyringExecutor) needsReconciliation(slot credentialSlot) bool {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	_, unresolved := executor.unresolved[slot]
	return unresolved
}

func (executor *keyringExecutor) requireReconciliation(slot credentialSlot) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.unresolved[slot] = struct{}{}
}

func (executor *keyringExecutor) reconciled(slot credentialSlot) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	delete(executor.unresolved, slot)
}
