package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"unicode/utf8"

	"routenest/internal/adminauth"
	"routenest/internal/desktopauth"
)

type desktopSetupState struct {
	Mode              string                                   `json:"mode"`
	Message           string                                   `json:"message,omitempty"`
	AdminURL          string                                   `json:"adminURL,omitempty"`
	GatewayURL        string                                   `json:"gatewayURL,omitempty"`
	DeviceStatus      string                                   `json:"deviceStatus,omitempty"`
	CredentialStorage adminauth.DesktopCredentialStorageStatus `json:"credentialStorage"`
}

type desktopSetupResult struct {
	RecoveryCode      string                                   `json:"recoveryCode"`
	HandoffToken      string                                   `json:"handoffToken,omitempty"`
	AdminURL          string                                   `json:"adminURL"`
	RestartRequired   bool                                     `json:"restartRequired,omitempty"`
	DeviceTrustStatus string                                   `json:"deviceTrustStatus,omitempty"`
	CredentialStorage adminauth.DesktopCredentialStorageStatus `json:"credentialStorage"`
}

type desktopSetupComplete func(context.Context, string, bool) (desktopSetupResult, error)
type desktopBridgeOperation func(context.Context, map[string]any) (map[string]any, error)

type DesktopSetupBridge struct {
	mu                 sync.Mutex
	operationMu        sync.Mutex
	state              desktopSetupState
	stateSnapshot      func(desktopSetupState) desktopSetupState
	complete           desktopSetupComplete
	operations         map[string]desktopBridgeOperation
	done               bool
	setupPending       bool
	automaticAttempted bool
}

func newDesktopSetupBridge(state desktopSetupState, complete desktopSetupComplete) *DesktopSetupBridge {
	return &DesktopSetupBridge{state: state, complete: complete, operations: map[string]desktopBridgeOperation{}}
}

func (bridge *DesktopSetupBridge) setOperation(name string, operation desktopBridgeOperation) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.operations[name] = operation
}

func (bridge *DesktopSetupBridge) invokeLocked(name string, request map[string]any) (map[string]any, error) {
	bridge.mu.Lock()
	operation := bridge.operations[name]
	mode := bridge.state.Mode
	switch name {
	case "sign-in", "sign-out", "lock", "recover-password", "admin-request", "trust", "auto-sign-in", "forget", "remove-local":
		bridge.automaticAttempted = true
	}
	bridge.mu.Unlock()
	if operation == nil || mode == "error" || mode == "restart" {
		return nil, errors.New("security_unavailable")
	}
	result, err := operation(context.Background(), request)
	return result, desktopBridgeError(err)
}

func (bridge *DesktopSetupBridge) invoke(name string, request map[string]any) (map[string]any, error) {
	bridge.operationMu.Lock()
	defer bridge.operationMu.Unlock()
	return bridge.invokeLocked(name, request)
}

func (bridge *DesktopSetupBridge) DesktopStatus() (map[string]any, error) {
	return bridge.invoke("status", nil)
}

func (bridge *DesktopSetupBridge) AdminRequest(request map[string]any) (map[string]any, error) {
	if err := desktopRequestFields(request, []string{"method", "path", "body", "csrf"}, nil); err != nil {
		return nil, err
	}
	return bridge.invoke("admin-request", request)
}

func (bridge *DesktopSetupBridge) SignIn(request map[string]any) (map[string]any, error) {
	if err := desktopRequestFields(request, []string{"username", "password"}, []string{"trustDevice"}); err != nil {
		return nil, err
	}
	return bridge.invoke("sign-in", request)
}

func (bridge *DesktopSetupBridge) RecoverPassword(request map[string]any) (map[string]any, error) {
	if err := desktopRequestFields(request, []string{"recoveryCode", "newPassword", "confirmation"}, nil); err != nil {
		return nil, err
	}
	password, _ := request["newPassword"].(string)
	confirmation, _ := request["confirmation"].(string)
	if err := validateDesktopPassword(password, confirmation); err != nil {
		return nil, err
	}
	return bridge.invoke("recover-password", request)
}

func (bridge *DesktopSetupBridge) TrustThisDevice(request map[string]any) (map[string]any, error) {
	if err := desktopRequestFields(request, []string{"currentPassword"}, nil); err != nil {
		return nil, err
	}
	return bridge.invoke("trust", request)
}

func (bridge *DesktopSetupBridge) SetDesktopAutoSignIn(request map[string]any) (map[string]any, error) {
	if err := desktopRequestFields(request, []string{"currentPassword"}, []string{"enabled"}); err != nil {
		return nil, err
	}
	if _, ok := request["enabled"].(bool); !ok {
		return nil, errors.New("invalid_request")
	}
	return bridge.invoke("auto-sign-in", request)
}

func (bridge *DesktopSetupBridge) ForgetThisDevice(request map[string]any) (map[string]any, error) {
	if err := desktopRequestFields(request, []string{"currentPassword"}, []string{"confirmRemoval"}); err != nil {
		return nil, err
	}
	if confirmed, _ := request["confirmRemoval"].(bool); !confirmed {
		return nil, errors.New("confirmation_required")
	}
	return bridge.invoke("forget", request)
}

func (bridge *DesktopSetupBridge) RemoveLocalDesktopCredential(request map[string]any) (map[string]any, error) {
	if err := desktopRequestFields(request, nil, []string{"confirmRemoval"}); err != nil {
		return nil, err
	}
	if confirmed, _ := request["confirmRemoval"].(bool); !confirmed {
		return nil, errors.New("confirmation_required")
	}
	return bridge.invoke("remove-local", request)
}

func (bridge *DesktopSetupBridge) SignOutDesktop() (map[string]any, error) {
	return bridge.invoke("sign-out", nil)
}

func (bridge *DesktopSetupBridge) LockDesktop() (map[string]any, error) {
	return bridge.invoke("lock", nil)
}

func (bridge *DesktopSetupBridge) AutomaticSignIn() (map[string]any, error) {
	bridge.operationMu.Lock()
	defer bridge.operationMu.Unlock()
	bridge.mu.Lock()
	if bridge.automaticAttempted || bridge.setupPending || bridge.state.Mode != "login" && bridge.state.Mode != "ready" {
		bridge.mu.Unlock()
		return nil, errors.New("automatic_sign_in_unavailable")
	}
	bridge.automaticAttempted = true
	bridge.mu.Unlock()
	return bridge.invokeLocked("automatic-sign-in", nil)
}

func (bridge *DesktopSetupBridge) FinishSetup() (map[string]any, error) {
	bridge.operationMu.Lock()
	defer bridge.operationMu.Unlock()
	bridge.mu.Lock()
	pending := bridge.setupPending && bridge.state.Mode != "restart"
	bridge.mu.Unlock()
	if !pending {
		return nil, errors.New("setup_unavailable")
	}
	result, err := bridge.invokeLocked("finish-setup", nil)
	if err != nil {
		return nil, err
	}
	bridge.mu.Lock()
	bridge.setupPending = false
	bridge.automaticAttempted = true
	bridge.state.Mode = "ready"
	bridge.mu.Unlock()
	return result, nil
}

func (bridge *DesktopSetupBridge) State() desktopSetupState {
	bridge.mu.Lock()
	state, snapshot := bridge.state, bridge.stateSnapshot
	bridge.mu.Unlock()
	if snapshot != nil {
		return snapshot(state)
	}
	return state
}

func (bridge *DesktopSetupBridge) Complete(password, confirmation string, trustDevice bool) (desktopSetupResult, error) {
	bridge.operationMu.Lock()
	defer bridge.operationMu.Unlock()
	bridge.mu.Lock()
	complete := bridge.complete
	available := !bridge.done && complete != nil && bridge.state.Mode == "setup"
	bridge.mu.Unlock()
	if !available {
		return desktopSetupResult{}, errors.New("setup_unavailable")
	}
	if err := validateDesktopPassword(password, confirmation); err != nil {
		return desktopSetupResult{}, err
	}
	result, err := complete(context.Background(), password, trustDevice)
	if err != nil {
		return desktopSetupResult{}, desktopBridgeError(err)
	}
	result.HandoffToken = ""
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.done = true
	bridge.complete = nil
	bridge.setupPending = !result.RestartRequired
	bridge.automaticAttempted = true
	bridge.state.Mode = "ready"
	bridge.state.AdminURL = result.AdminURL
	bridge.state.CredentialStorage = result.CredentialStorage
	if result.RestartRequired {
		bridge.state.Mode = "restart"
	}
	return result, nil
}

func validateDesktopPassword(password, confirmation string) error {
	if password != confirmation {
		return errors.New("password_confirmation_mismatch")
	}
	if len(password) > 1024 {
		return errors.New("password_too_long")
	}
	if utf8.RuneCountInString(password) < 8 || strings.TrimSpace(password) == "" {
		return errors.New("invalid_password")
	}
	return nil
}

func desktopRequestFields(request map[string]any, stringFields, boolFields []string) error {
	allowed := make(map[string]bool, len(stringFields)+len(boolFields))
	for _, field := range stringFields {
		allowed[field] = false
	}
	for _, field := range boolFields {
		allowed[field] = true
	}
	for field, value := range request {
		boolean, ok := allowed[field]
		if !ok {
			return errors.New("invalid_request")
		}
		if boolean {
			if _, ok := value.(bool); !ok {
				return errors.New("invalid_request")
			}
		} else if _, ok := value.(string); !ok {
			return errors.New("invalid_request")
		}
	}
	return nil
}

func desktopBridgeError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, adminauth.ErrRateLimited):
		return errors.New("rate_limited")
	case errors.Is(err, adminauth.ErrUnauthorized):
		return errors.New("authentication_failed")
	case errors.Is(err, adminauth.ErrForbidden):
		return errors.New("permission_denied")
	case errors.Is(err, adminauth.ErrMutationPrepared):
		return errors.New("security_restart_required")
	case errors.Is(err, adminauth.ErrPasswordRotationRequired):
		return errors.New("password_rotation_required")
	case errors.Is(err, desktopauth.ErrUnavailable):
		return errors.New("desktop_storage_unavailable")
	case errors.Is(err, desktopauth.ErrCorrupt):
		return errors.New("desktop_storage_corrupt")
	case errors.Is(err, desktopauth.ErrMissing):
		return errors.New("desktop_credential_missing")
	default:
		return errors.New("security_unavailable")
	}
}
