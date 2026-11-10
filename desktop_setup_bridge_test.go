package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

func TestDesktopSetupBridgeValidatesAndBecomesInert(t *testing.T) {
	var calls atomic.Int32
	bridge := newDesktopSetupBridge(desktopSetupState{Mode: "setup", AdminURL: "http://127.0.0.1:3457/admin/"}, func(context.Context, string, bool) (desktopSetupResult, error) {
		calls.Add(1)
		return desktopSetupResult{RecoveryCode: "recovery", HandoffToken: "handoff", AdminURL: "http://127.0.0.1:3457/admin/"}, nil
	})
	if _, err := bridge.Complete("short", "short", true); err == nil {
		t.Fatal("short password accepted")
	}
	if _, err := bridge.Complete("valid password", "different password", true); err == nil {
		t.Fatal("mismatched password accepted")
	}
	result, err := bridge.Complete("valid password", "valid password", true)
	if err != nil || result.RecoveryCode == "" || calls.Load() != 1 {
		t.Fatalf("completion = %+v, %v, calls=%d", result, err, calls.Load())
	}
	if _, err := bridge.Complete("another password", "another password", true); err == nil || calls.Load() != 1 {
		t.Fatalf("repeat completion = %v, calls=%d", err, calls.Load())
	}
}

func TestDesktopSetupBridgePreservesRetryAfterFailure(t *testing.T) {
	var calls atomic.Int32
	bridge := newDesktopSetupBridge(desktopSetupState{Mode: "setup"}, func(context.Context, string, bool) (desktopSetupResult, error) {
		if calls.Add(1) == 1 {
			return desktopSetupResult{}, errors.New("temporary failure")
		}
		return desktopSetupResult{RecoveryCode: "recovery"}, nil
	})
	if _, err := bridge.Complete("valid password", "valid password", true); err == nil {
		t.Fatal("temporary failure hidden")
	}
	if _, err := bridge.Complete("valid password", "valid password", true); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
}

func TestDesktopBridgeOperationsAreExplicitAndRestricted(t *testing.T) {
	bridge := newDesktopSetupBridge(desktopSetupState{Mode: "ready"}, nil)
	if _, err := bridge.SignIn(map[string]any{"password": "secret"}); err == nil {
		t.Fatal("unregistered desktop operation succeeded")
	}
	bridge.setOperation("sign-in", func(_ context.Context, request map[string]any) (map[string]any, error) {
		if request["password"] != "secret" {
			return nil, errors.New("bad password")
		}
		return map[string]any{"handoffToken": "handoff"}, nil
	})
	result, err := bridge.SignIn(map[string]any{"password": "secret"})
	if err != nil || result["handoffToken"] != "handoff" {
		t.Fatalf("registered operation = %#v, %v", result, err)
	}
	if _, err := bridge.invoke("unknown", nil); err == nil {
		t.Fatal("unknown operation succeeded")
	}
}

func TestDesktopBridgeNativeTransitionContract(t *testing.T) {
	bridge := newDesktopSetupBridge(desktopSetupState{Mode: "setup", AdminURL: "/admin/"}, func(context.Context, string, bool) (desktopSetupResult, error) {
		return desktopSetupResult{RecoveryCode: "recovery", HandoffToken: "early-handoff", AdminURL: "/admin/"}, nil
	})
	var finishes atomic.Int32
	bridge.setOperation("finish-setup", func(context.Context, map[string]any) (map[string]any, error) {
		finishes.Add(1)
		return map[string]any{"handoffToken": "fresh-handoff", "adminURL": "/admin/"}, nil
	})
	if _, err := bridge.FinishSetup(); err == nil {
		t.Fatal("setup handoff issued before completion and acknowledgement")
	}
	result, err := bridge.Complete("valid password", "valid password", false)
	if err != nil || result.HandoffToken != "" {
		t.Fatalf("completion exposed an early handoff: %+v, %v", result, err)
	}
	finish, err := bridge.FinishSetup()
	if err != nil || finish["handoffToken"] != "fresh-handoff" || finishes.Load() != 1 {
		t.Fatalf("finish = %#v, %v, calls=%d", finish, err, finishes.Load())
	}
	if _, err := bridge.FinishSetup(); err == nil || finishes.Load() != 1 {
		t.Fatal("setup acknowledgement reused")
	}
	for _, method := range []string{"sign-out", "lock", "automatic-sign-in"} {
		bridge.setOperation(method, func(_ context.Context, request map[string]any) (map[string]any, error) {
			if request != nil {
				t.Fatal("zero-argument operation supplied a payload")
			}
			return map[string]any{"success": true}, nil
		})
	}
	if _, err := bridge.SignOutDesktop(); err != nil {
		t.Fatal(err)
	}
	if _, err := bridge.LockDesktop(); err != nil {
		t.Fatal(err)
	}
	if _, err := bridge.AutomaticSignIn(); err == nil {
		t.Fatal("completed setup allowed automatic reauthentication")
	}
	automatic := newDesktopSetupBridge(desktopSetupState{Mode: "login"}, nil)
	automatic.setOperation("automatic-sign-in", func(_ context.Context, request map[string]any) (map[string]any, error) {
		if request != nil {
			t.Fatal("automatic sign-in received a payload")
		}
		return map[string]any{"success": true}, nil
	})
	if _, err := automatic.AutomaticSignIn(); err != nil {
		t.Fatal(err)
	}
	if _, err := automatic.AutomaticSignIn(); err == nil {
		t.Fatal("automatic sign-in ran twice during one launch")
	}
}

func TestDesktopAutomaticSignInCannotFollowManualSessionTransitions(t *testing.T) {
	for _, transition := range []string{"sign-in", "sign-out", "lock", "recover-password"} {
		t.Run(transition, func(t *testing.T) {
			bridge := newDesktopSetupBridge(desktopSetupState{Mode: "login"}, nil)
			bridge.setOperation(transition, func(context.Context, map[string]any) (map[string]any, error) {
				return map[string]any{"success": true}, nil
			})
			bridge.setOperation("automatic-sign-in", func(context.Context, map[string]any) (map[string]any, error) {
				t.Fatal("automatic sign-in ran after a manual session transition")
				return nil, nil
			})
			if _, err := bridge.invoke(transition, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := bridge.AutomaticSignIn(); err == nil {
				t.Fatal("automatic sign-in was available after manual session transition")
			}
		})
	}
}

func TestDesktopBridgeCleanupRequiresExplicitConfirmation(t *testing.T) {
	bridge := newDesktopSetupBridge(desktopSetupState{Mode: "login"}, nil)
	var removals atomic.Int32
	bridge.setOperation("remove-local", func(_ context.Context, request map[string]any) (map[string]any, error) {
		removals.Add(1)
		return map[string]any{"success": true}, nil
	})
	for _, request := range []map[string]any{nil, {}, {"confirmRemoval": false}, {"confirmRemoval": "true"}} {
		if _, err := bridge.RemoveLocalDesktopCredential(request); err == nil {
			t.Fatal("unconfirmed removal accepted")
		}
	}
	if _, err := bridge.RemoveLocalDesktopCredential(map[string]any{"confirmRemoval": true}); err != nil || removals.Load() != 1 {
		t.Fatalf("confirmed removal = %v, calls=%d", err, removals.Load())
	}
}

func TestDesktopSetupBridgeBecomesRestartOnlyAfterPreparedMutation(t *testing.T) {
	bridge := newDesktopSetupBridge(desktopSetupState{Mode: "setup"}, func(context.Context, string, bool) (desktopSetupResult, error) {
		return desktopSetupResult{RecoveryCode: "recovery", RestartRequired: true}, nil
	})
	result, err := bridge.Complete("valid password", "valid password", true)
	if err != nil || !result.RestartRequired || result.RecoveryCode == "" {
		t.Fatalf("completion = %+v, %v", result, err)
	}
	if state := bridge.State(); state.Mode != "restart" {
		t.Fatalf("state mode = %q", state.Mode)
	}
	if _, err := bridge.Complete("another password", "another password", true); err == nil {
		t.Fatal("prepared setup accepted a second completion")
	}
}
