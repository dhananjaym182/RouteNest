package desktopauth

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestExecutorDeadlineIsSharedAcrossCalls(t *testing.T) {
	executor := newKeyringExecutor()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if !executor.acquire(ctx) {
		t.Fatal("could not acquire transaction gate")
	}
	defer executor.release()
	var calls atomic.Int32
	_, err := executor.call(ctx, func() (string, error) {
		calls.Add(1)
		cancel()
		return "late secret", nil
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("cancelled call = %v", err)
	}
	_, err = executor.call(ctx, func() (string, error) {
		calls.Add(1)
		return "", nil
	})
	if !errors.Is(err, ErrUnavailable) || calls.Load() != 1 {
		t.Fatal("a follow-up OS operation started after the transaction deadline")
	}
}

func TestExecutorDiscardsLateSecretAndBackendError(t *testing.T) {
	executor := newKeyringExecutor()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if !executor.acquire(ctx) {
		t.Fatal("could not acquire transaction gate")
	}
	defer executor.release()
	unblock := make(chan struct{})
	defer close(unblock)
	_, err := executor.call(ctx, func() (string, error) {
		<-unblock
		return "late secret", errors.New("secret-bearing backend error")
	})
	if !errors.Is(err, ErrUnavailable) || executor.flight == nil {
		t.Fatal("timed out operation did not retain its single-flight guard")
	}
	flight := executor.flight
	unblock <- struct{}{}
	awaitSignal(t, flight.done)
	if flight.value != "" || !errors.Is(flight.err, ErrUnavailable) {
		t.Fatal("completed late operation retained a secret-bearing result")
	}
}

func TestExecutorPanicIsSanitizedAndDoesNotStrandGate(t *testing.T) {
	executor := newKeyringExecutor()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !executor.acquire(ctx) {
		t.Fatal("could not acquire transaction gate")
	}
	defer executor.release()
	_, err := executor.call(ctx, func() (string, error) {
		panic("keyring panic contains secret material")
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("backend panic = %v", err)
	}
	if _, err := executor.call(ctx, func() (string, error) { return "", nil }); err != nil {
		t.Fatal("backend panic stranded the executor")
	}
}
