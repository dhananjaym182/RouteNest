//go:build desktop

package main

import (
	"errors"
	"testing"

	"routenest/internal/admin"
	"routenest/internal/adminauth"
)

func TestDesktopBootstrapCompletionRequiresRestartForUncertainOrMissingHandoff(t *testing.T) {
	for _, err := range []error{adminauth.ErrMutationPrepared, admin.ErrDesktopHandoffUnavailable} {
		result, completionErr := desktopBootstrapCompletion("http://127.0.0.1:3457/admin/", "recovery", "", err)
		if completionErr != nil || !result.RestartRequired || result.RecoveryCode != "recovery" || result.HandoffToken != "" {
			t.Fatalf("completion for %v = %+v, %v", err, result, completionErr)
		}
	}
	if _, err := desktopBootstrapCompletion("", "", "", errors.New("pre-bootstrap failure")); err == nil {
		t.Fatal("ordinary setup failure was reported as restart completion")
	}
}

func TestDesktopBootstrapCompletionReturnsNormalHandoff(t *testing.T) {
	result, err := desktopBootstrapCompletion("http://127.0.0.1:3457/admin/", "recovery", "handoff", nil)
	if err != nil || result.RestartRequired || result.HandoffToken != "handoff" || result.RecoveryCode != "recovery" {
		t.Fatalf("normal completion = %+v, %v", result, err)
	}
}

func TestDesktopRuntimeUsesExactLoopbackHostForAdminURL(t *testing.T) {
	runtime := &desktopRuntime{port: 3457}
	for host, want := range map[string]string{
		"127.0.0.1": "http://127.0.0.1:3457/admin/",
		"localhost": "http://localhost:3457/admin/",
		"::1":       "http://[::1]:3457/admin/",
		"0.0.0.0":   "http://127.0.0.1:3457/admin/",
	} {
		runtime.setHost(host)
		if runtime.adminURL != want {
			t.Fatalf("admin URL for %q = %q, want %q", host, runtime.adminURL, want)
		}
	}
}
