package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"unicode/utf8"
)

type desktopSetupState struct {
	Mode     string `json:"mode"`
	Message  string `json:"message,omitempty"`
	AdminURL string `json:"adminURL,omitempty"`
}

type desktopSetupResult struct {
	RecoveryCode    string `json:"recoveryCode"`
	HandoffToken    string `json:"handoffToken,omitempty"`
	AdminURL        string `json:"adminURL"`
	RestartRequired bool   `json:"restartRequired,omitempty"`
}

type desktopSetupComplete func(context.Context, string) (desktopSetupResult, error)

// DesktopSetupBridge is bound only to the Wails asset-origin setup page. The
// completion callback rechecks durable state and becomes inert after success.
type DesktopSetupBridge struct {
	mu       sync.Mutex
	state    desktopSetupState
	complete desktopSetupComplete
	done     bool
}

func newDesktopSetupBridge(state desktopSetupState, complete desktopSetupComplete) *DesktopSetupBridge {
	return &DesktopSetupBridge{state: state, complete: complete}
}

func (bridge *DesktopSetupBridge) State() desktopSetupState {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	return bridge.state
}

func (bridge *DesktopSetupBridge) Complete(password, confirmation string) (desktopSetupResult, error) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if bridge.done || bridge.complete == nil || bridge.state.Mode != "setup" {
		return desktopSetupResult{}, errors.New("desktop setup is unavailable")
	}
	if password != confirmation {
		return desktopSetupResult{}, errors.New("password confirmation does not match")
	}
	if utf8.RuneCountInString(password) < 8 || len(password) > 1024 || strings.TrimSpace(password) == "" {
		return desktopSetupResult{}, errors.New("password must contain at least 8 characters and at most 1024 bytes")
	}
	result, err := bridge.complete(context.Background(), password)
	if err != nil {
		return desktopSetupResult{}, err
	}
	bridge.done = true
	bridge.complete = nil
	mode := "ready"
	if result.RestartRequired {
		mode = "restart"
	}
	bridge.state = desktopSetupState{Mode: mode, AdminURL: result.AdminURL}
	return result, nil
}
