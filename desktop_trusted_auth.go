//go:build desktop

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"routenest/internal/admin"
	"routenest/internal/adminauth"
	"routenest/internal/desktopauth"
)

var errDesktopTrustUncertain = errors.New("desktop device enrollment could not be safely discarded")

func desktopTrustRestartRequired(err error) bool {
	return errors.Is(err, errDesktopTrustUncertain) || errors.Is(err, adminauth.ErrMutationPrepared) || errors.Is(err, adminauth.ErrDegraded)
}

func (runtime *desktopRuntime) newBridge(state desktopSetupState, complete desktopSetupComplete) *DesktopSetupBridge {
	state.AdminURL = "/admin/"
	state.GatewayURL = runtime.gatewayURL
	state.CredentialStorage = runtime.credentialStorageStatus()
	bridge := newDesktopSetupBridge(state, complete)
	bridge.stateSnapshot = runtime.desktopStateSnapshot
	bridge.setOperation("status", runtime.desktopStatus)
	bridge.setOperation("admin-request", runtime.adminRequest)
	bridge.setOperation("sign-in", runtime.signInDesktop)
	bridge.setOperation("automatic-sign-in", runtime.automaticSignIn)
	bridge.setOperation("finish-setup", runtime.finishSetup)
	bridge.setOperation("recover-password", runtime.recoverDesktopPassword)
	bridge.setOperation("trust", runtime.trustThisDevice)
	bridge.setOperation("auto-sign-in", runtime.setDesktopAutoSignIn)
	bridge.setOperation("forget", runtime.forgetThisDevice)
	bridge.setOperation("remove-local", runtime.removeLocalDesktopCredential)
	bridge.setOperation("sign-out", runtime.signOutDesktop)
	bridge.setOperation("lock", runtime.lockDesktop)
	return bridge
}

func (runtime *desktopRuntime) credentialStorageStatus() adminauth.DesktopCredentialStorageStatus {
	if runtime.credentialStore == nil {
		return adminauth.DesktopCredentialStorageStatus{State: "unavailable"}
	}
	return runtime.credentialStore.Status()
}

func (runtime *desktopRuntime) desktopStateSnapshot(state desktopSetupState) desktopSetupState {
	runtime.authMu.Lock()
	defer runtime.authMu.Unlock()
	runtime.lifecycleMu.Lock()
	started, trusted := runtime.windowStarted, runtime.trustedWindow
	unavailable := runtime.closed || runtime.proxyFailure != nil
	runtime.lifecycleMu.Unlock()
	if unavailable || started && !trusted {
		state.Mode = "error"
		state.Message = "security_unavailable"
		state.CredentialStorage = adminauth.DesktopCredentialStorageStatus{State: "unavailable"}
		state.DeviceStatus = "unavailable"
		return state
	}
	if !started || state.Mode == "error" || state.Mode == "restart" {
		return state
	}
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	status, _ := runtime.desktopStatusLocked(ctx)
	state.CredentialStorage, _ = status["credentialStorage"].(adminauth.DesktopCredentialStorageStatus)
	state.DeviceStatus, _ = status["deviceStatus"].(string)
	return state
}

func (runtime *desktopRuntime) beginDesktopOperation(ctx context.Context, requireProxy bool) (context.Context, func(), error) {
	runtime.authMu.Lock()
	ctx, cancel, err := runtime.nativeOperationContext(ctx, requireProxy)
	if err != nil {
		runtime.authMu.Unlock()
		return nil, nil, err
	}
	return ctx, func() { cancel(); runtime.authMu.Unlock() }, nil
}

func (runtime *desktopRuntime) setDesktopMode(mode string) {
	if runtime.bridge == nil {
		return
	}
	runtime.bridge.mu.Lock()
	defer runtime.bridge.mu.Unlock()
	runtime.bridge.state.Mode = mode
	runtime.bridge.state.Message = ""
	runtime.bridge.automaticAttempted = true
}

func activateStoredDesktopCredential(ctx context.Context, manager *desktopauth.Manager, credential adminauth.TrustedDeviceCredential, activate func(context.Context, string) error) error {
	if manager == nil {
		return desktopauth.ErrUnavailable
	}
	if err := manager.Store(credential); err != nil {
		return err
	}
	if err := activate(ctx, credential.ID); err != nil {
		return errors.Join(err, manager.Remove())
	}
	return nil
}

func (runtime *desktopRuntime) liveDesktopCredentials(ctx context.Context) (adminauth.Credentials, error) {
	if runtime.console == nil {
		return adminauth.Credentials{}, adminauth.ErrUnauthorized
	}
	return runtime.console.Credentials(ctx)
}

func (runtime *desktopRuntime) clearDesktopSessionsLocked(ctx context.Context) error {
	var errs []error
	current, err := runtime.liveDesktopCredentials(ctx)
	if err == nil {
		errs = append(errs, admin.EndDesktopSession(ctx, current))
	} else if !errors.Is(err, adminauth.ErrUnauthorized) {
		errs = append(errs, err)
	}
	if runtime.console != nil {
		runtime.console.Clear()
	}
	pending := runtime.credentials
	runtime.credentials = adminauth.Credentials{}
	runtime.setupPending = false
	if pending.Token != "" && pending.Token != current.Token {
		if err := admin.EndDesktopSession(ctx, pending); !errors.Is(err, adminauth.ErrUnauthorized) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (runtime *desktopRuntime) automaticSignIn(ctx context.Context, _ map[string]any) (map[string]any, error) {
	ctx, done, err := runtime.beginDesktopOperation(ctx, true)
	if err != nil {
		return nil, err
	}
	defer done()
	if runtime.setupPending || runtime.credentials.Token != "" {
		return nil, errDesktopUnavailable
	}
	if credentials, _ := runtime.liveDesktopCredentials(ctx); credentials.Token != "" {
		return nil, errDesktopUnavailable
	}
	if runtime.credentialStore == nil {
		return runtime.desktopStatusLocked(ctx)
	}
	credential, status, err := runtime.credentialStore.Credential()
	if err != nil || !status.AutomaticSignIn {
		return runtime.desktopStatusLocked(ctx)
	}
	if !credential.ExpiresAt.After(time.Now()) {
		runtime.recordDesktopDeviceState(credential.ID, "expired", false)
		return runtime.desktopStatusLocked(ctx)
	}
	credentials, handoff, err := admin.AuthenticateDesktopDevice(ctx, credential.ID, credential.Secret)
	if err != nil {
		deviceStatus := "unavailable"
		switch {
		case errors.Is(err, adminauth.ErrTrustedDeviceAutoSignInDisabled):
			deviceStatus = "paused"
		case errors.Is(err, adminauth.ErrTrustedDeviceExpired):
			deviceStatus = "expired"
		case errors.Is(err, adminauth.ErrTrustedDeviceUntrusted), errors.Is(err, adminauth.ErrUnauthorized):
			deviceStatus = "revoked"
		}
		runtime.recordDesktopDeviceState(credential.ID, deviceStatus, false)
		result, _ := runtime.desktopStatusLocked(ctx)
		result["deviceStatus"] = deviceStatus
		result["automaticSignIn"] = false
		return result, nil
	}
	if err := ctx.Err(); err != nil {
		_ = admin.EndDesktopSession(context.Background(), credentials)
		return nil, err
	}
	runtime.credentials = credentials
	runtime.recordDesktopDeviceState(credential.ID, "trusted", true)
	result, _ := runtime.desktopStatusLocked(ctx)
	result["handoffToken"] = handoff
	runtime.setDesktopMode("ready")
	return result, nil
}

func (runtime *desktopRuntime) signInDesktop(ctx context.Context, request map[string]any) (map[string]any, error) {
	ctx, done, err := runtime.beginDesktopOperation(ctx, true)
	if err != nil {
		return nil, err
	}
	defer done()
	if runtime.setupPending {
		return nil, errDesktopUnavailable
	}
	password := desktopString(request, "password")
	username := desktopString(request, "username")
	trust, _ := request["trustDevice"].(bool)
	if password == "" || len(password) > adminauth.MaxPasswordBytes || len(username) > adminauth.MaxUsernameBytes {
		return nil, adminauth.ErrUnauthorized
	}
	credentials, err := admin.AuthenticateDesktopLocal(ctx, username, password)
	if err != nil {
		return nil, err
	}
	if err := runtime.clearDesktopSessionsLocked(ctx); err != nil {
		_ = admin.EndDesktopSession(ctx, credentials)
		return nil, err
	}
	trustStatus := "not_requested"
	if trust {
		if err := runtime.enrollDesktopCredentialLocked(ctx, credentials, "", true); err != nil {
			if desktopTrustRestartRequired(err) {
				_ = admin.EndDesktopSession(ctx, credentials)
				runtime.setDesktopMode("restart")
				return nil, err
			}
			trustStatus = "unavailable"
		} else {
			trustStatus = "trusted"
		}
	}
	if err := ctx.Err(); err != nil {
		_ = admin.EndDesktopSession(context.Background(), credentials)
		return nil, err
	}
	handoff, err := admin.CreateDesktopSessionHandoff(credentials)
	if err != nil {
		_ = admin.EndDesktopSession(ctx, credentials)
		return nil, err
	}
	runtime.credentials = credentials
	runtime.setDesktopMode("ready")
	result, _ := runtime.desktopStatusLocked(ctx)
	result["handoffToken"] = handoff
	result["deviceTrustStatus"] = trustStatus
	return result, nil
}

func (runtime *desktopRuntime) finishSetup(ctx context.Context, _ map[string]any) (map[string]any, error) {
	ctx, done, err := runtime.beginDesktopOperation(ctx, true)
	if err != nil {
		return nil, err
	}
	defer done()
	if !runtime.setupPending || runtime.credentials.Token == "" {
		return nil, errDesktopUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	handoff, err := admin.CreateDesktopSessionHandoff(runtime.credentials)
	if err != nil {
		return nil, err
	}
	runtime.setupPending = false
	return map[string]any{"handoffToken": handoff, "adminURL": "/admin/", "gatewayURL": runtime.gatewayURL}, nil
}

func (runtime *desktopRuntime) adminRequest(ctx context.Context, request map[string]any) (map[string]any, error) {
	ctx, done, err := runtime.beginDesktopOperation(ctx, true)
	if err != nil {
		return nil, err
	}
	defer done()
	if err := desktopRequestFields(request, []string{"method", "path", "body", "csrf"}, nil); err != nil {
		return nil, err
	}
	if runtime.console == nil || runtime.setupPending {
		return nil, errDesktopUnavailable
	}
	input := admin.DesktopConsoleRequest{
		Method: desktopString(request, "method"), Path: desktopString(request, "path"),
		Body: desktopString(request, "body"), CSRF: desktopString(request, "csrf"),
	}
	before, _ := runtime.liveDesktopCredentials(ctx)
	response := runtime.console.Request(ctx, input)
	after, _ := runtime.liveDesktopCredentials(ctx)
	path, _, _ := strings.Cut(input.Path, "?")
	handoff := input.Method == http.MethodPost && path == "/admin/api/auth/desktop-handoff"
	reset := desktopSessionResetResponse(path, response)
	loginAttempt := input.Method == http.MethodPost && path == "/admin/api/login"
	if after.Token != "" || handoff || reset || loginAttempt || response.Status == http.StatusUnauthorized {
		pending := runtime.credentials
		runtime.credentials = adminauth.Credentials{}
		if pending.Token != "" && pending.Token != after.Token {
			_ = admin.EndDesktopSession(ctx, pending)
		}
	}
	if before.Token != "" && after.Token != before.Token {
		_ = admin.EndDesktopSession(ctx, before)
	}
	var storageErr error
	if reset {
		storageErr = runtime.pauseLocalDesktopCredentialLocked()
		runtime.recordDesktopDeviceState(runtime.deviceID, "revoked", false)
		runtime.setDesktopMode("login")
	}
	if after.Token != "" {
		runtime.setDesktopMode("ready")
	}
	result := map[string]any{"status": response.Status, "body": response.Body, "contentType": response.ContentType, "csrf": response.CSRF}
	if storageErr != nil {
		result["storageIssue"] = desktopBridgeError(storageErr).Error()
		result["credentialStorage"] = runtime.credentialStorageStatus()
	}
	return result, nil
}

func desktopSessionResetResponse(path string, response admin.DesktopConsoleResponse) bool {
	if path != "/admin/api/auth/password" && path != "/admin/api/auth/recovery" && path != "/admin/api/auth/bootstrap" && path != "/admin/api/auth/legacy/rotate" {
		return false
	}
	if response.Status >= 200 && response.Status < 300 {
		return true
	}
	var body struct {
		Error string `json:"error"`
	}
	return response.Status == http.StatusServiceUnavailable && json.Unmarshal([]byte(response.Body), &body) == nil && body.Error == "security_restart_required"
}

func (runtime *desktopRuntime) recoverDesktopPassword(ctx context.Context, request map[string]any) (map[string]any, error) {
	ctx, done, err := runtime.beginDesktopOperation(ctx, true)
	if err != nil {
		return nil, err
	}
	defer done()
	password := desktopString(request, "newPassword")
	if err := validateDesktopPassword(password, desktopString(request, "confirmation")); err != nil {
		return nil, err
	}
	replacement, restart, recoveryErr := admin.RecoverDesktopPassword(ctx, desktopString(request, "recoveryCode"), password)
	if recoveryErr != nil && !(replacement != "" && restart && errors.Is(recoveryErr, adminauth.ErrMutationPrepared)) {
		return nil, recoveryErr
	}
	_ = runtime.clearDesktopSessionsLocked(ctx)
	storageErr := runtime.pauseLocalDesktopCredentialLocked()
	runtime.recordDesktopDeviceState(runtime.deviceID, "revoked", false)
	if restart {
		runtime.setDesktopMode("restart")
	} else {
		runtime.setDesktopMode("login")
	}
	result := map[string]any{
		"recoveryCode": replacement, "restartRequired": restart, "loginRequired": true,
		"adminURL": "/admin/", "gatewayURL": runtime.gatewayURL,
		"automaticSignIn": false, "credentialStorage": runtime.credentialStorageStatus(),
	}
	if storageErr != nil {
		result["storageIssue"] = desktopBridgeError(storageErr).Error()
	}
	return result, nil
}

func (runtime *desktopRuntime) desktopStatus(ctx context.Context, _ map[string]any) (map[string]any, error) {
	ctx, done, err := runtime.beginDesktopOperation(ctx, false)
	if err != nil {
		return nil, err
	}
	defer done()
	return runtime.desktopStatusLocked(ctx)
}

func (runtime *desktopRuntime) desktopStatusLocked(ctx context.Context) (map[string]any, error) {
	status := runtime.credentialStorageStatus()
	var credential adminauth.TrustedDeviceCredential
	if runtime.credentialStore != nil {
		credential, status, _ = runtime.credentialStore.Credential()
	}
	deviceStatus := status.State
	automatic := false
	if status.Stored {
		deviceStatus = "unavailable"
		if credential.ID == runtime.deviceID && runtime.deviceStatus != "" {
			deviceStatus = runtime.deviceStatus
			automatic = status.AutomaticSignIn && runtime.deviceAutomatic
		}
		if !credential.ExpiresAt.After(time.Now()) {
			deviceStatus = "expired"
		} else if runtime.securityConfigured {
			view, registered, valid, err := runtime.inspectLocalDesktopDevice(ctx, credential)
			if err == nil && registered {
				runtime.rememberDesktopDevice(view)
			}
			switch {
			case errors.Is(err, desktopauth.ErrCorrupt):
				deviceStatus = "corrupt"
			case err != nil:
				deviceStatus = "unavailable"
			case registered && !view.ExpiresAt.After(time.Now()):
				deviceStatus = "expired"
			case !registered || !valid:
				deviceStatus = "revoked"
			case !status.AutomaticSignIn || !view.AutomaticSignIn:
				deviceStatus = "paused"
			default:
				deviceStatus = "trusted"
				automatic = true
			}
		} else if !status.AutomaticSignIn {
			deviceStatus = "paused"
		}
		if deviceStatus != "trusted" {
			automatic = false
		}
		runtime.recordDesktopDeviceState(credential.ID, deviceStatus, automatic)
	}
	devices := []adminauth.TrustedDeviceView{}
	if credentials, err := runtime.liveDesktopCredentials(ctx); err == nil {
		if listed, err := admin.ListDesktopDevices(ctx, credentials); err == nil {
			devices = listed
		}
	}
	return map[string]any{
		"storage": status.State, "credentialStorage": status, "automaticSignIn": automatic,
		"deviceID": credential.ID, "deviceStatus": deviceStatus, "devices": devices,
		"adminURL": "/admin/", "gatewayURL": runtime.gatewayURL,
	}, nil
}

func (runtime *desktopRuntime) recordDesktopDeviceState(id, status string, automatic bool) {
	if id != runtime.deviceID {
		runtime.deviceUserID = ""
		runtime.deviceExpiresAt = time.Time{}
	}
	runtime.deviceID = id
	runtime.deviceStatus = status
	runtime.deviceAutomatic = automatic
}

func (runtime *desktopRuntime) rememberDesktopDevice(view adminauth.TrustedDeviceView) {
	runtime.recordDesktopDeviceState(view.ID, runtime.deviceStatus, runtime.deviceAutomatic)
	runtime.deviceUserID = view.UserID
	runtime.deviceExpiresAt = view.ExpiresAt
}

func (runtime *desktopRuntime) inspectLocalDesktopDevice(ctx context.Context, credential adminauth.TrustedDeviceCredential) (adminauth.TrustedDeviceView, bool, bool, error) {
	if !runtime.securityConfigured {
		return adminauth.TrustedDeviceView{}, false, false, errDesktopUnavailable
	}
	return admin.InspectDesktopDeviceCredential(ctx, credential)
}

func (runtime *desktopRuntime) enrollDesktopCredentialLocked(ctx context.Context, credentials adminauth.Credentials, password string, afterAuthentication bool) error {
	if runtime.credentialStore == nil {
		return desktopauth.ErrUnavailable
	}
	old, _, err := runtime.credentialStore.Credential()
	if err != nil && !errors.Is(err, desktopauth.ErrMissing) {
		return err
	}
	replaceID := ""
	if old.ID != "" {
		view, registered, _, err := runtime.inspectLocalDesktopDevice(ctx, old)
		if err != nil {
			return err
		}
		if registered {
			if view.UserID != credentials.Principal.UserID {
				return adminauth.ErrForbidden
			}
			replaceID = old.ID
		}
	}
	var credential adminauth.TrustedDeviceCredential
	if afterAuthentication {
		credential, err = admin.EnrollDesktopDeviceAfterAuthentication(ctx, credentials, runtime.credentialStore.Metadata(), replaceID)
	} else {
		credential, err = admin.EnrollDesktopDevice(ctx, credentials, password, runtime.credentialStore.Metadata(), replaceID)
	}
	if err != nil {
		return err
	}
	if err := activateStoredDesktopCredential(ctx, runtime.credentialStore, credential, func(ctx context.Context, id string) error {
		return admin.ActivateDesktopDevice(ctx, credentials, id)
	}); err != nil {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		discardErr := admin.DiscardDesktopDevice(cleanupContext, credentials, credential.ID)
		cancel()
		runtime.recordDesktopDeviceState(credential.ID, "revoked", false)
		if discardErr != nil {
			return errors.Join(errDesktopTrustUncertain, err, discardErr)
		}
		return err
	}
	runtime.recordDesktopDeviceState(credential.ID, "trusted", true)
	runtime.rememberDesktopDevice(adminauth.TrustedDeviceView{ID: credential.ID, UserID: credentials.Principal.UserID, ExpiresAt: credential.ExpiresAt})
	return nil
}

func (runtime *desktopRuntime) trustThisDevice(ctx context.Context, request map[string]any) (map[string]any, error) {
	ctx, done, err := runtime.beginDesktopOperation(ctx, true)
	if err != nil {
		return nil, err
	}
	defer done()
	credentials, err := runtime.liveDesktopCredentials(ctx)
	if err != nil {
		return nil, err
	}
	password := desktopString(request, "currentPassword")
	if password == "" {
		return nil, adminauth.ErrUnauthorized
	}
	if err := runtime.enrollDesktopCredentialLocked(ctx, credentials, password, false); err != nil {
		if desktopTrustRestartRequired(err) {
			_ = runtime.clearDesktopSessionsLocked(ctx)
			runtime.setDesktopMode("restart")
		}
		return nil, err
	}
	if _, err := runtime.liveDesktopCredentials(ctx); err != nil {
		return nil, err
	}
	return runtime.desktopStatusLocked(ctx)
}

func (runtime *desktopRuntime) setDesktopAutoSignIn(ctx context.Context, request map[string]any) (map[string]any, error) {
	ctx, done, err := runtime.beginDesktopOperation(ctx, true)
	if err != nil {
		return nil, err
	}
	defer done()
	credentials, err := runtime.liveDesktopCredentials(ctx)
	if err != nil {
		return nil, err
	}
	password := desktopString(request, "currentPassword")
	enabled, ok := request["enabled"].(bool)
	if password == "" || !ok {
		return nil, adminauth.ErrUnauthorized
	}
	if runtime.credentialStore == nil {
		return nil, desktopauth.ErrUnavailable
	}
	credential, _, err := runtime.credentialStore.Credential()
	if err != nil {
		return nil, err
	}
	view, registered, valid, err := runtime.inspectLocalDesktopDevice(ctx, credential)
	if err != nil {
		return nil, err
	}
	if !registered {
		return nil, adminauth.ErrTrustedDeviceUntrusted
	}
	if !valid || !view.ExpiresAt.After(time.Now()) {
		return nil, adminauth.ErrTrustedDeviceExpired
	}
	if view.UserID != credentials.Principal.UserID {
		return nil, adminauth.ErrForbidden
	}
	if !enabled {
		verified, err := admin.AuthenticateDesktopLocal(ctx, credentials.Principal.Username, password)
		if err != nil {
			return nil, err
		}
		if err := admin.EndDesktopSession(ctx, verified); err != nil {
			return nil, err
		}
		if verified.Principal.UserID != credentials.Principal.UserID {
			return nil, adminauth.ErrUnauthorized
		}
	}
	if err := admin.SetDesktopDeviceAutoSignIn(ctx, credentials, password, credential.ID, enabled); err != nil {
		return nil, err
	}
	if err := runtime.credentialStore.SetAutoSignIn(enabled); err != nil {
		var rollbackErr error
		if enabled {
			cleanupContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			rollbackErr = admin.SetDesktopDeviceAutoSignIn(cleanupContext, credentials, password, credential.ID, false)
			cancel()
			if rollbackErr != nil {
				_ = runtime.clearDesktopSessionsLocked(context.Background())
				runtime.setDesktopMode("restart")
			}
		}
		runtime.recordDesktopDeviceState(credential.ID, "paused", false)
		return nil, errors.Join(err, rollbackErr)
	}
	runtime.recordDesktopDeviceState(credential.ID, "paused", false)
	return runtime.desktopStatusLocked(ctx)
}

func (runtime *desktopRuntime) forgetThisDevice(ctx context.Context, request map[string]any) (map[string]any, error) {
	ctx, done, err := runtime.beginDesktopOperation(ctx, true)
	if err != nil {
		return nil, err
	}
	defer done()
	if confirmed, _ := request["confirmRemoval"].(bool); !confirmed {
		return nil, errors.New("confirmation_required")
	}
	if runtime.credentialStore == nil {
		return nil, desktopauth.ErrUnavailable
	}
	credential, _, err := runtime.credentialStore.Credential()
	if err != nil && !errors.Is(err, desktopauth.ErrMissing) {
		return nil, err
	}
	if credential.ID != "" {
		view, registered, _, err := runtime.inspectLocalDesktopDevice(ctx, credential)
		if err != nil {
			return nil, err
		}
		if registered {
			credentials, err := runtime.liveDesktopCredentials(ctx)
			if err != nil {
				return nil, err
			}
			if credentials.Principal.UserID != view.UserID {
				return nil, adminauth.ErrForbidden
			}
			if err := admin.RevokeDesktopDevice(ctx, credentials, desktopString(request, "currentPassword"), credential.ID); err != nil {
				if !errors.Is(err, adminauth.ErrTrustedDeviceUntrusted) {
					return nil, err
				}
				if _, registered, _, inspectErr := runtime.inspectLocalDesktopDevice(ctx, credential); inspectErr != nil || registered {
					return nil, errors.Join(err, inspectErr)
				}
			}
		}
	}
	runtime.recordDesktopDeviceState(credential.ID, "revoked", false)
	removeErr := runtime.credentialStore.Remove()
	clearErr := runtime.clearDesktopSessionsLocked(ctx)
	runtime.setDesktopMode("login")
	if err := errors.Join(removeErr, clearErr); err != nil {
		return nil, err
	}
	return runtime.desktopStatusLocked(ctx)
}

func (runtime *desktopRuntime) removeLocalDesktopCredential(ctx context.Context, request map[string]any) (map[string]any, error) {
	ctx, done, err := runtime.beginDesktopOperation(ctx, false)
	if err != nil {
		return nil, err
	}
	defer done()
	if confirmed, _ := request["confirmRemoval"].(bool); !confirmed {
		return nil, errors.New("confirmation_required")
	}
	if runtime.credentialStore == nil {
		return nil, desktopauth.ErrUnavailable
	}
	removeErr := runtime.credentialStore.Remove()
	clearErr := runtime.clearDesktopSessionsLocked(ctx)
	runtime.setDesktopMode("login")
	if err := errors.Join(removeErr, clearErr); err != nil {
		return nil, err
	}
	return runtime.desktopStatusLocked(ctx)
}

func (runtime *desktopRuntime) pauseLocalDesktopCredentialLocked() error {
	if runtime.credentialStore == nil {
		return desktopauth.ErrUnavailable
	}
	credential, status, err := runtime.credentialStore.Credential()
	if errors.Is(err, desktopauth.ErrMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	runtime.recordDesktopDeviceState(credential.ID, "paused", false)
	if !status.AutomaticSignIn {
		return nil
	}
	return runtime.credentialStore.SetAutoSignIn(false)
}

func (runtime *desktopRuntime) signOutDesktop(ctx context.Context, _ map[string]any) (map[string]any, error) {
	ctx, done, err := runtime.beginDesktopOperation(ctx, true)
	if err != nil {
		return nil, err
	}
	defer done()
	credentials, credentialErr := runtime.liveDesktopCredentials(ctx)
	if errors.Is(credentialErr, adminauth.ErrUnauthorized) && runtime.credentials.Token != "" {
		credentials, credentialErr = runtime.credentials, nil
	}
	var sessionErr, storageErr error
	deviceID := ""
	pauseLocal := true
	pauseKnown := false
	if runtime.credentialStore != nil {
		credential, _, readErr := runtime.credentialStore.Credential()
		storageErr = readErr
		if errors.Is(readErr, desktopauth.ErrMissing) {
			storageErr = nil
			pauseKnown = true
		} else if readErr == nil {
			view, registered, valid, inspectErr := runtime.inspectLocalDesktopDevice(ctx, credential)
			if inspectErr != nil {
				storageErr = inspectErr
			} else if !registered || !valid || !view.ExpiresAt.After(time.Now()) {
				pauseKnown = true
			} else if credentialErr == nil && view.UserID == credentials.Principal.UserID {
				deviceID = credential.ID
				runtime.rememberDesktopDevice(view)
			} else if credentialErr == nil {
				pauseLocal = false
				pauseKnown = true
			}
		}
	} else {
		storageErr = desktopauth.ErrUnavailable
	}
	if deviceID == "" && credentialErr == nil && runtime.deviceUserID == credentials.Principal.UserID && runtime.deviceExpiresAt.After(time.Now()) {
		deviceID = runtime.deviceID
	}
	if credentialErr == nil {
		sessionErr = admin.SignOutDesktopDeviceSession(ctx, credentials, deviceID)
		if deviceID != "" && (errors.Is(sessionErr, adminauth.ErrTrustedDeviceUntrusted) || errors.Is(sessionErr, adminauth.ErrTrustedDeviceExpired)) {
			sessionErr = admin.SignOutDesktopSession(ctx, credentials)
			pauseKnown = true
		}
		if sessionErr == nil && (deviceID != "" || credentials.Principal.DeviceID != "") {
			pauseKnown = true
		}
	} else if !errors.Is(credentialErr, adminauth.ErrUnauthorized) {
		sessionErr = credentialErr
	}
	clearErr := runtime.clearDesktopSessionsLocked(ctx)
	if pauseLocal {
		if localErr := runtime.pauseLocalDesktopCredentialLocked(); localErr != nil {
			storageErr = errors.Join(storageErr, localErr)
		} else {
			pauseKnown = true
		}
	}
	runtime.setDesktopMode("login")
	result := map[string]any{
		"sessionEnded": true, "automaticSignIn": false, "automaticSignInPaused": pauseKnown,
		"adminURL": "/admin/", "gatewayURL": runtime.gatewayURL,
		"credentialStorage": runtime.credentialStorageStatus(),
	}
	if storageErr != nil {
		result["storageIssue"] = desktopBridgeError(storageErr).Error()
		result["storageWarning"] = true
	}
	if !pauseKnown {
		result["automaticSignInPauseIncomplete"] = true
	}
	return result, errors.Join(sessionErr, clearErr)
}

func (runtime *desktopRuntime) lockDesktop(ctx context.Context, _ map[string]any) (map[string]any, error) {
	ctx, done, err := runtime.beginDesktopOperation(ctx, true)
	if err != nil {
		return nil, err
	}
	defer done()
	err = runtime.clearDesktopSessionsLocked(ctx)
	runtime.setDesktopMode("login")
	if err != nil {
		return nil, err
	}
	return map[string]any{"sessionEnded": true, "adminURL": "/admin/", "gatewayURL": runtime.gatewayURL}, nil
}

func desktopString(request map[string]any, key string) string {
	value, _ := request[key].(string)
	return value
}
