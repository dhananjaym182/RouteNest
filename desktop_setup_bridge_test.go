package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

func TestDesktopSetupBridgeValidatesAndBecomesInert(t *testing.T) {
	var calls atomic.Int32
	bridge := newDesktopSetupBridge(desktopSetupState{Mode: "setup", AdminURL: "http://127.0.0.1:3457/admin/"}, func(context.Context, string) (desktopSetupResult, error) {
		calls.Add(1)
		return desktopSetupResult{RecoveryCode: "recovery", HandoffToken: "handoff", AdminURL: "http://127.0.0.1:3457/admin/"}, nil
	})
	if _, err := bridge.Complete("short", "short"); err == nil {
		t.Fatal("short password accepted")
	}
	if _, err := bridge.Complete("valid password", "different password"); err == nil {
		t.Fatal("mismatched password accepted")
	}
	result, err := bridge.Complete("valid password", "valid password")
	if err != nil || result.RecoveryCode == "" || calls.Load() != 1 {
		t.Fatalf("completion = %+v, %v, calls=%d", result, err, calls.Load())
	}
	if _, err := bridge.Complete("another password", "another password"); err == nil || calls.Load() != 1 {
		t.Fatalf("repeat completion = %v, calls=%d", err, calls.Load())
	}
}

func TestDesktopSetupBridgePreservesRetryAfterFailure(t *testing.T) {
	var calls atomic.Int32
	bridge := newDesktopSetupBridge(desktopSetupState{Mode: "setup"}, func(context.Context, string) (desktopSetupResult, error) {
		if calls.Add(1) == 1 {
			return desktopSetupResult{}, errors.New("temporary failure")
		}
		return desktopSetupResult{RecoveryCode: "recovery"}, nil
	})
	if _, err := bridge.Complete("valid password", "valid password"); err == nil {
		t.Fatal("temporary failure hidden")
	}
	if _, err := bridge.Complete("valid password", "valid password"); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
}

func TestDesktopSetupBridgeBecomesRestartOnlyAfterPreparedMutation(t *testing.T) {
	bridge := newDesktopSetupBridge(desktopSetupState{Mode: "setup"}, func(context.Context, string) (desktopSetupResult, error) {
		return desktopSetupResult{RecoveryCode: "recovery", RestartRequired: true}, nil
	})
	result, err := bridge.Complete("valid password", "valid password")
	if err != nil || !result.RestartRequired || result.RecoveryCode == "" {
		t.Fatalf("completion = %+v, %v", result, err)
	}
	if state := bridge.State(); state.Mode != "restart" {
		t.Fatalf("state mode = %q", state.Mode)
	}
	if _, err := bridge.Complete("another password", "another password"); err == nil {
		t.Fatal("prepared setup accepted a second completion")
	}
}
