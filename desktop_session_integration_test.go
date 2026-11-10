//go:build desktop

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"routenest/internal/admin"
	"routenest/internal/adminauth"
	"routenest/internal/desktopauth"
	"routenest/internal/routing"
	accountstore "routenest/internal/store/accounts"
	storeadminauth "routenest/internal/store/adminauth"
	"routenest/internal/zen"
)

const desktopSessionPassword = "DesktopSession8!"

var desktopTestModelSync sync.Once

func desktopSessionRuntime(t *testing.T, trust bool) (*desktopRuntime, *desktopCredentialKeyring, string) {
	t.Helper()
	isolateDesktopAccounts(t)
	for _, name := range []string{admin.AdminCanonicalURLEnv, admin.AdminProxyModeEnv, admin.AdminTrustedProxyCIDRsEnv, admin.AdminHSTSEnv, admin.AdminBootstrapPasswordEnv} {
		t.Setenv(name, "")
	}
	if err := admin.CloseSecurity(); err != nil {
		t.Fatal(err)
	}
	zen.ConfigMu.Lock()
	oldZen := zen.Config
	zen.Config = &zen.ConfigData{BaseURL: zen.APIBase, Key: "public"}
	zen.ConfigMu.Unlock()
	t.Cleanup(func() {
		zen.ConfigMu.Lock()
		zen.Config = oldZen
		zen.ConfigMu.Unlock()
	})
	desktopTestModelSync.Do(func() {
		routing.SyncMu.Lock()
		routing.SyncRan = true
		routing.SyncMu.Unlock()
	})
	runtime, err := prepareDesktopRuntime("127.0.0.1", 0, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.close(); err != nil {
			t.Error(err)
		}
	})
	store := &desktopCredentialKeyring{}
	runtime.credentialStore = desktopauth.NewManagerWithKeyring(store)
	if runtime.bridge.State().Mode != "setup" || runtime.securityConfigured || runtime.bootstrap != "" {
		t.Fatal("fresh desktop prepared administrator credentials before setup")
	}
	for _, name := range []string{accountstore.FileName, accountstore.FileName + ".initialized", "admin-auth"} {
		if _, err := os.Stat(filepath.Join(runtime.dataDir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("fresh startup created %s before setup: %v", name, err)
		}
	}
	runtime.onWindowStartup(desktopWindowTestContext())
	result, err := runtime.bridge.Complete(desktopSessionPassword, desktopSessionPassword, trust)
	if err != nil || result.RecoveryCode == "" || result.HandoffToken != "" || result.RestartRequired {
		t.Fatalf("complete setup = %+v, %v", result, err)
	}
	if trust && result.DeviceTrustStatus != "trusted" {
		t.Fatalf("initial device trust = %s", result.DeviceTrustStatus)
	}
	if _, err := runtime.console.Credentials(t.Context()); !errors.Is(err, adminauth.ErrUnauthorized) {
		t.Fatalf("setup exposed native session before acknowledgement: %v", err)
	}
	if _, err := runtime.bridge.AdminRequest(map[string]any{"method": "GET", "path": "/admin/api/auth/session"}); err == nil {
		t.Fatal("setup bypassed recovery acknowledgement through native API")
	}
	finish, err := runtime.bridge.FinishSetup()
	if err != nil {
		t.Fatal(err)
	}
	desktopConsumeHandoff(t, runtime, finish)
	if _, err := runtime.bridge.FinishSetup(); err == nil {
		t.Fatal("setup minted another handoff after acknowledgement")
	}
	return runtime, store, result.RecoveryCode
}

func desktopBridgeRequest(t *testing.T, runtime *desktopRuntime, method, path, body, csrf string) map[string]any {
	t.Helper()
	response, err := runtime.bridge.AdminRequest(map[string]any{"method": method, "path": path, "body": body, "csrf": csrf})
	if err != nil {
		t.Fatalf("native request %s %s: %v", method, path, err)
	}
	return response
}

func desktopConsumeHandoff(t *testing.T, runtime *desktopRuntime, result map[string]any) adminauth.Credentials {
	t.Helper()
	handoff, _ := result["handoffToken"].(string)
	if handoff == "" || result["adminURL"] != "/admin/" {
		t.Fatalf("invalid handoff result: %#v", result)
	}
	body, _ := json.Marshal(map[string]string{"token": handoff})
	response := desktopBridgeRequest(t, runtime, http.MethodPost, "/admin/api/auth/desktop-handoff", string(body), "")
	if response["status"] != http.StatusOK {
		t.Fatalf("handoff redemption = %#v", response)
	}
	credentials, err := runtime.console.Credentials(t.Context())
	if err != nil || credentials.Token == "" || runtime.credentials.Token != "" {
		t.Fatalf("native session was not transferred exclusively: %v", err)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), credentials.Token) {
		t.Fatal("native response disclosed the session token")
	}
	return credentials
}

func desktopPasswordLogin(t *testing.T, runtime *desktopRuntime, username, password string, trust bool) adminauth.Credentials {
	t.Helper()
	result, err := runtime.bridge.SignIn(map[string]any{"username": username, "password": password, "trustDevice": trust})
	if err != nil {
		t.Fatalf("password sign-in: %v", err)
	}
	return desktopConsumeHandoff(t, runtime, result)
}

func desktopAuthorityState(t *testing.T, runtime *desktopRuntime) adminauth.State {
	t.Helper()
	state, err := storeadminauth.Inspect(t.Context(), runtime.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestDesktopFreshSetupRequiresAcknowledgementAndKeepsBrowsersUnauthenticated(t *testing.T) {
	runtime, _, recoveryCode := desktopSessionRuntime(t, true)
	for _, name := range []string{accountstore.FileName, accountstore.FileName + ".initialized"} {
		if _, err := os.Stat(filepath.Join(runtime.dataDir, name)); err != nil {
			t.Fatalf("fresh setup did not create %s: %v", name, err)
		}
	}
	state := desktopAuthorityState(t, runtime)
	if !state.Initialized || state.RecoveryCodePHC == "" || len(state.Sessions) != 1 || len(state.TrustedDevices) != 1 {
		t.Fatal("fresh setup did not persist recovery, session, and trusted device authority")
	}
	credential, storage, err := runtime.credentialStore.Credential()
	if err != nil || !storage.Stored || !storage.AutomaticSignIn || state.TrustedDevices[credential.ID].ActivatedAt.IsZero() {
		t.Fatalf("fresh setup failed to activate the verified protected credential: %v", err)
	}
	session, err := runtime.console.Credentials(t.Context())
	if err != nil || session.Principal.UserID != adminauth.BreakGlassID || session.Principal.DeviceID != credential.ID || runtime.credentials.Token != "" {
		t.Fatalf("fresh handoff did not transfer the sole native session: %v", err)
	}
	if _, err := runtime.bridge.Complete(desktopSessionPassword, desktopSessionPassword, true); err == nil {
		t.Fatal("completed fresh setup remained callable")
	}
	publicState, err := json.Marshal(runtime.bridge.State())
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{recoveryCode, credential.Secret, session.Token} {
		if strings.Contains(string(publicState), secret) {
			t.Fatal("fresh setup state exposed a secret after handoff")
		}
	}
	mux := http.NewServeMux()
	admin.RegisterRoutes(mux)
	request := httptest.NewRequest(http.MethodGet, strings.TrimSuffix(runtime.gatewayURL, "/v1")+"/admin/api/auth/session", nil)
	request.RemoteAddr = "127.0.0.1:43000"
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || len(response.Result().Cookies()) != 0 {
		t.Fatalf("fresh desktop session authenticated an ordinary browser: %d", response.Code)
	}
}

func TestDesktopNativeSessionIsSoleAuthorityAfterHandoffAndRelogin(t *testing.T) {
	runtime, _, _ := desktopSessionRuntime(t, false)
	initial, err := runtime.console.Credentials(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	request := desktopBridgeRequest(t, runtime, http.MethodGet, "/admin/api/auth/session", "", "")
	if request["status"] != http.StatusOK || !strings.Contains(request["body"].(string), `"username":"admin"`) {
		t.Fatalf("current session response = %#v", request)
	}
	create := desktopBridgeRequest(t, runtime, http.MethodPost, "/admin/api/auth/users", `{"username":"viewer","password":"ViewerPassword8!","roles":["viewer"]}`, initial.CSRFToken)
	if create["status"] != http.StatusCreated {
		t.Fatalf("create viewer = %#v", create)
	}
	viewer := desktopPasswordLogin(t, runtime, "viewer", "ViewerPassword8!", true)
	if viewer.Principal.Username != "viewer" {
		t.Fatal("native password login ignored supplied username")
	}
	if _, err := admin.CreateDesktopSessionHandoff(initial); !errors.Is(err, adminauth.ErrUnauthorized) {
		t.Fatalf("previous session remained usable after login switch: %v", err)
	}
	if _, err := runtime.bridge.TrustThisDevice(map[string]any{"currentPassword": "ViewerPassword8!"}); err == nil {
		t.Fatal("viewer enrolled a device using stale administrator credentials")
	}
	status, err := runtime.bridge.DesktopStatus()
	if err != nil || len(status["devices"].([]adminauth.TrustedDeviceView)) != 0 {
		t.Fatalf("viewer device status = %#v, %v", status, err)
	}
	if _, err := runtime.bridge.LockDesktop(); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.console.Credentials(t.Context()); !errors.Is(err, adminauth.ErrUnauthorized) {
		t.Fatalf("locked native session remains authenticated: %v", err)
	}
	if len(desktopAuthorityState(t, runtime).Sessions) != 0 {
		t.Fatal("session transition left orphaned administrator sessions")
	}
}

func TestDesktopPasswordLoginFallsBackWhenCredentialStorageUnavailable(t *testing.T) {
	for _, failure := range []string{"unavailable", "corrupt"} {
		t.Run(failure, func(t *testing.T) {
			runtime, store, _ := desktopSessionRuntime(t, false)
			if _, err := runtime.bridge.LockDesktop(); err != nil {
				t.Fatal(err)
			}
			store.mu.Lock()
			if failure == "corrupt" {
				store.value = "invalid saved credential"
			} else {
				store.err = errors.New("keyring locked")
			}
			store.mu.Unlock()
			result, err := runtime.bridge.SignIn(map[string]any{"username": "admin", "password": desktopSessionPassword, "trustDevice": true})
			if err != nil || result["deviceTrustStatus"] != "unavailable" {
				t.Fatalf("password fallback = %#v, %v", result, err)
			}
			desktopConsumeHandoff(t, runtime, result)
			if len(desktopAuthorityState(t, runtime).TrustedDevices) != 0 {
				t.Fatal("failed trust enrolled a usable device")
			}
		})
	}
}

func TestDesktopLockRetainsTrustAndSignOutPausesWithoutPassword(t *testing.T) {
	runtime, _, _ := desktopSessionRuntime(t, true)
	credential, _, err := runtime.credentialStore.Credential()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.bridge.LockDesktop(); err != nil {
		t.Fatal(err)
	}
	state := desktopAuthorityState(t, runtime)
	if len(state.Sessions) != 0 || !state.TrustedDevices[credential.ID].AutomaticSignIn || !runtime.credentialStore.Inspect().AutomaticSignIn {
		t.Fatal("lock disabled trust or retained session")
	}
	runtime.bridge = runtime.newBridge(desktopSetupState{Mode: "login"}, nil)
	automatic, err := runtime.bridge.AutomaticSignIn()
	if err != nil {
		t.Fatal(err)
	}
	deviceSession := desktopConsumeHandoff(t, runtime, automatic)
	if deviceSession.Principal.DeviceID != credential.ID {
		t.Fatal("automatic native session was not bound to device")
	}
	if _, err := runtime.bridge.SignOutDesktop(); err != nil {
		t.Fatal(err)
	}
	state = desktopAuthorityState(t, runtime)
	if len(state.Sessions) != 0 || state.TrustedDevices[credential.ID].AutomaticSignIn || runtime.credentialStore.Inspect().AutomaticSignIn {
		t.Fatal("sign-out retained session or automatic trust")
	}
	if _, err := runtime.bridge.AutomaticSignIn(); err == nil {
		t.Fatal("same-launch sign-out allowed immediate automatic reauthentication")
	}
	desktopPasswordLogin(t, runtime, "admin", desktopSessionPassword, false)
	if runtime.credentialStore.Inspect().AutomaticSignIn {
		t.Fatal("password login silently resumed paused automatic trust")
	}
}

func TestDesktopPasswordSessionSignOutPausesSavedDeviceEvenWhenLocalPauseFails(t *testing.T) {
	runtime, store, _ := desktopSessionRuntime(t, true)
	credential, _, err := runtime.credentialStore.Credential()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.bridge.LockDesktop(); err != nil {
		t.Fatal(err)
	}
	credentials := desktopPasswordLogin(t, runtime, "admin", desktopSessionPassword, false)
	if credentials.Principal.DeviceID != "" {
		t.Fatal("password-only session unexpectedly bound to device")
	}
	store.mu.Lock()
	store.setErr = errors.New("local preference write failed")
	store.mu.Unlock()
	result, err := runtime.bridge.SignOutDesktop()
	if err != nil || result["storageWarning"] != true || result["sessionEnded"] != true || result["automaticSignInPaused"] != true {
		t.Fatalf("failed local pause was not reported after server sign-out: %#v, %v", result, err)
	}
	state := desktopAuthorityState(t, runtime)
	if len(state.Sessions) != 0 || state.TrustedDevices[credential.ID].AutomaticSignIn {
		t.Fatal("password-only sign-out left the saved device enabled on the server")
	}
	if _, _, err := admin.AuthenticateDesktopDevice(t.Context(), credential.ID, credential.Secret); !errors.Is(err, adminauth.ErrTrustedDeviceAutoSignInDisabled) {
		t.Fatalf("saved device still authenticates after sign-out: %v", err)
	}
}

func TestDesktopSignOutUsesVerifiedCachedDeviceWhenVaultBecomesUnreadable(t *testing.T) {
	runtime, store, _ := desktopSessionRuntime(t, true)
	credential, _, err := runtime.credentialStore.Credential()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.bridge.LockDesktop(); err != nil {
		t.Fatal(err)
	}
	desktopPasswordLogin(t, runtime, "admin", desktopSessionPassword, false)
	store.mu.Lock()
	store.err = errors.New("vault cannot be read")
	store.mu.Unlock()
	result, err := runtime.bridge.SignOutDesktop()
	if err != nil || result["sessionEnded"] != true || result["storageWarning"] != true || result["automaticSignInPaused"] != true {
		t.Fatalf("unreadable-vault sign-out = %#v, %v", result, err)
	}
	state := desktopAuthorityState(t, runtime)
	if len(state.Sessions) != 0 || state.TrustedDevices[credential.ID].AutomaticSignIn {
		t.Fatal("cached-device sign-out left automatic server access")
	}
}

func TestDesktopSignOutWithoutTrustDoesNotDependOnWorkingKeyring(t *testing.T) {
	runtime, store, _ := desktopSessionRuntime(t, false)
	store.mu.Lock()
	store.err = errors.New("vault unavailable")
	store.mu.Unlock()
	result, err := runtime.bridge.SignOutDesktop()
	if err != nil || result["sessionEnded"] != true || result["storageWarning"] != true || result["automaticSignInPauseIncomplete"] != true {
		t.Fatalf("password-only sign-out = %#v, %v", result, err)
	}
	if len(desktopAuthorityState(t, runtime).Sessions) != 0 {
		t.Fatal("password-only sign-out retained session")
	}
}

func TestDesktopWrongSecretRequiresExplicitLocalRemoval(t *testing.T) {
	runtime, _, _ := desktopSessionRuntime(t, true)
	credential, _, err := runtime.credentialStore.Credential()
	if err != nil {
		t.Fatal(err)
	}
	credential.Secret = token43("wrong saved proof")
	if err := runtime.credentialStore.Store(credential); err != nil {
		t.Fatal(err)
	}
	status, err := runtime.bridge.DesktopStatus()
	if err != nil || status["deviceStatus"] != "corrupt" || status["automaticSignIn"] != false {
		t.Fatalf("wrong-proof device status = %#v, %v", status, err)
	}
	if _, err := runtime.bridge.ForgetThisDevice(map[string]any{"currentPassword": desktopSessionPassword, "confirmRemoval": true}); err == nil {
		t.Fatal("wrong proof was treated as an absent registration")
	}
	if _, err := runtime.bridge.TrustThisDevice(map[string]any{"currentPassword": desktopSessionPassword}); err == nil {
		t.Fatal("wrong proof allowed silent credential replacement")
	}
	if state := desktopAuthorityState(t, runtime); len(state.TrustedDevices) != 1 || state.TrustedDevices[credential.ID].ID == "" {
		t.Fatal("wrong proof changed server registration")
	}
	if _, err := runtime.bridge.RemoveLocalDesktopCredential(map[string]any{"confirmRemoval": true}); err != nil {
		t.Fatalf("explicit local wrong-proof removal failed: %v", err)
	}
}

func TestDesktopExpiredRegistrationIsReportedExpiredAndCanBeExplicitlyReplaced(t *testing.T) {
	runtime, _, _ := desktopSessionRuntime(t, true)
	credential, _, err := runtime.credentialStore.Credential()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.bridge.LockDesktop(); err != nil {
		t.Fatal(err)
	}
	if err := admin.CloseSecurity(); err != nil {
		t.Fatal(err)
	}
	runtime.securityConfigured = false
	repository, err := storeadminauth.OpenForRecovery(runtime.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	expiredAt := time.Now().UTC().Add(-time.Hour)
	event := adminauth.AuditEvent{ID: token43("expire-runtime-device"), Kind: adminauth.AuditTrustedDeviceAutoSignIn, ActorID: adminauth.BreakGlassID, TargetID: credential.ID, Outcome: "success", At: time.Now().UTC()}
	err = repository.Apply(t.Context(), event, func(state *adminauth.State) error {
		device := state.TrustedDevices[credential.ID]
		device.CreatedAt = expiredAt.Add(-time.Hour)
		device.ActivatedAt = expiredAt.Add(-30 * time.Minute)
		device.ExpiresAt = expiredAt
		state.TrustedDevices[credential.ID] = device
		return adminauth.ValidateState(*state)
	})
	closeErr := repository.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("expire device authority: %v, %v", err, closeErr)
	}
	if err := admin.ConfigureSecurity(admin.SecurityOptions{DataDir: runtime.dataDir, ListenHost: runtime.host, ListenPort: runtime.port}); err != nil {
		t.Fatal(err)
	}
	runtime.securityConfigured = true
	runtime.console, err = admin.NewDesktopConsole()
	if err != nil {
		t.Fatal(err)
	}
	runtime.bridge = runtime.newBridge(desktopSetupState{Mode: "login"}, nil)
	status, err := runtime.bridge.DesktopStatus()
	if err != nil || status["deviceStatus"] != "expired" || status["automaticSignIn"] != false {
		t.Fatalf("expired server credential status = %#v, %v", status, err)
	}
	current := desktopPasswordLogin(t, runtime, "admin", desktopSessionPassword, true)
	newCredential, local, err := runtime.credentialStore.Credential()
	if err != nil || newCredential.ID == credential.ID || !local.AutomaticSignIn || current.Principal.DeviceID != newCredential.ID {
		t.Fatalf("explicit expired credential replacement failed: %v", err)
	}
	if state := desktopAuthorityState(t, runtime); len(state.TrustedDevices) != 1 || state.TrustedDevices[credential.ID].ID != "" {
		t.Fatal("expired credential remained after replacement")
	}
}

func TestDesktopNativePasswordRotationInvalidatesTrustAndAllowsExplicitRetrust(t *testing.T) {
	runtime, _, _ := desktopSessionRuntime(t, true)
	oldDevice, _, err := runtime.credentialStore.Credential()
	if err != nil {
		t.Fatal(err)
	}
	before, err := runtime.console.Credentials(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	response := desktopBridgeRequest(t, runtime, http.MethodPost, "/admin/api/auth/password", `{"oldPassword":"`+desktopSessionPassword+`","newPassword":"ReplacementPassword8!"}`, before.CSRFToken)
	if response["status"] != http.StatusOK {
		t.Fatalf("password rotation = %#v", response)
	}
	if _, err := runtime.console.Credentials(t.Context()); !errors.Is(err, adminauth.ErrUnauthorized) || runtime.credentials.Token != "" {
		t.Fatalf("password change retained parallel credentials: %v", err)
	}
	if runtime.credentialStore.Inspect().AutomaticSignIn {
		t.Fatal("password change retained local automatic preference")
	}
	credentials := desktopPasswordLogin(t, runtime, "admin", "ReplacementPassword8!", true)
	newDevice, status, err := runtime.credentialStore.Credential()
	if err != nil || !status.AutomaticSignIn || newDevice.ID == oldDevice.ID || credentials.Principal.DeviceID != newDevice.ID {
		t.Fatalf("explicit re-trust after password reset failed: %v", err)
	}
	state := desktopAuthorityState(t, runtime)
	if len(state.TrustedDevices) != 1 || state.TrustedDevices[oldDevice.ID].ID != "" {
		t.Fatal("re-trust preserved revoked device")
	}
}

func TestDesktopNativeRequestsRejectPathsCSRFAndSocketHandoffs(t *testing.T) {
	runtime, _, _ := desktopSessionRuntime(t, false)
	credentials, err := runtime.console.Credentials(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"http://127.0.0.1/admin/api/auth/session", "//127.0.0.1/admin/api/auth/session", "/admin/api/../auth/session", "/admin/api/%2e%2e/auth/session", "/admin/api/auth/session#fragment", "/v1/models"} {
		response := desktopBridgeRequest(t, runtime, http.MethodGet, path, "", "")
		if response["status"].(int) < 400 {
			t.Fatalf("unsafe path %q succeeded: %#v", path, response)
		}
	}
	response := desktopBridgeRequest(t, runtime, http.MethodPost, "/admin/api/logout", `{}`, "")
	if response["status"] != http.StatusForbidden {
		t.Fatalf("missing CSRF logout = %#v", response)
	}
	handoff, err := admin.CreateDesktopSessionHandoff(credentials)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	admin.RegisterRoutes(mux)
	request := httptest.NewRequest(http.MethodPost, strings.TrimSuffix(runtime.gatewayURL, "/v1")+"/admin/api/auth/desktop-handoff", strings.NewReader(`{"token":"`+handoff+`"}`))
	request.RemoteAddr = "127.0.0.1:41000"
	request.Header.Set("Origin", strings.TrimSuffix(runtime.gatewayURL, "/v1"))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || len(recorder.Result().Cookies()) != 0 {
		t.Fatalf("socket handoff accepted: %d", recorder.Code)
	}
	for _, path := range []string{"/admin/api/auth/session", "/admin/api/auth/desktop-handoff"} {
		recorder = httptest.NewRecorder()
		runtime.serveDesktopConsole(recorder, httptest.NewRequest(http.MethodGet, desktopAssetOrigin()+path, nil))
		if recorder.Code != http.StatusNotFound || len(recorder.Result().Cookies()) != 0 {
			t.Fatalf("static native handler exposed API %q: %d", path, recorder.Code)
		}
	}
}

func TestDesktopTrustEnrollmentVerifiesStorageBeforeActivationAndDiscardsFailure(t *testing.T) {
	for _, failStore := range []bool{false, true} {
		t.Run(map[bool]string{false: "verified", true: "write-failed"}[failStore], func(t *testing.T) {
			runtime, store, _ := desktopSessionRuntime(t, false)
			var observedPending bool
			var inspectionErr error
			store.mu.Lock()
			store.beforeSet = func() {
				state, err := storeadminauth.Inspect(t.Context(), runtime.dataDir)
				inspectionErr = err
				for _, device := range state.TrustedDevices {
					if device.ActivatedAt.IsZero() {
						observedPending = true
					}
				}
			}
			if failStore {
				store.setErr = errors.New("keyring write failed")
			}
			store.mu.Unlock()
			result, err := runtime.bridge.TrustThisDevice(map[string]any{"currentPassword": desktopSessionPassword})
			if inspectionErr != nil || !observedPending {
				t.Fatalf("enrollment was activated before protected storage verification: %v", inspectionErr)
			}
			state := desktopAuthorityState(t, runtime)
			if failStore {
				if err == nil || len(state.TrustedDevices) != 0 {
					t.Fatalf("failed OS store retained trusted device: %v", err)
				}
				credentials, err := runtime.console.Credentials(t.Context())
				if err != nil || credentials.Principal.DeviceID != "" {
					t.Fatalf("discard failed to retain the valid password session: %v", err)
				}
			} else {
				if err != nil || result["deviceStatus"] != "trusted" || result["automaticSignIn"] != true {
					t.Fatalf("trust result = %#v, %v", result, err)
				}
				for _, device := range state.TrustedDevices {
					if device.ActivatedAt.IsZero() {
						t.Fatal("verified OS credential was not activated")
					}
				}
			}
		})
	}
}

func TestDesktopAutoSignInRequiresPasswordAndRollsBackFailedLocalEnable(t *testing.T) {
	runtime, store, _ := desktopSessionRuntime(t, true)
	credential, _, err := runtime.credentialStore.Credential()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.bridge.SetDesktopAutoSignIn(map[string]any{"enabled": false, "currentPassword": "wrong password"}); err == nil {
		t.Fatal("pausing automatic sign-in accepted the wrong password")
	}
	if !desktopAuthorityState(t, runtime).TrustedDevices[credential.ID].AutomaticSignIn {
		t.Fatal("wrong-password pause mutated authority")
	}
	if _, err := runtime.bridge.SetDesktopAutoSignIn(map[string]any{"enabled": false, "currentPassword": desktopSessionPassword}); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.setErr = errors.New("protected storage is read-only")
	store.mu.Unlock()
	if _, err := runtime.bridge.SetDesktopAutoSignIn(map[string]any{"enabled": true, "currentPassword": desktopSessionPassword}); err == nil {
		t.Fatal("failed local enable reported success")
	}
	if desktopAuthorityState(t, runtime).TrustedDevices[credential.ID].AutomaticSignIn {
		t.Fatal("failed local enable left automatic server access")
	}
	status, err := runtime.bridge.DesktopStatus()
	if err != nil || status["automaticSignIn"] != false {
		t.Fatalf("failed enable status = %#v, %v", status, err)
	}
}

func TestDesktopForgetAfterRemoteRevocationAndCorruptLocalRemoval(t *testing.T) {
	runtime, store, _ := desktopSessionRuntime(t, true)
	credentials, err := runtime.console.Credentials(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	credential, _, err := runtime.credentialStore.Credential()
	if err != nil {
		t.Fatal(err)
	}
	response := desktopBridgeRequest(t, runtime, http.MethodPost, "/admin/api/auth/trusted-devices/revoke", `{"currentPassword":"`+desktopSessionPassword+`","deviceID":"`+credential.ID+`"}`, credentials.CSRFToken)
	if response["status"] != http.StatusOK {
		t.Fatalf("remote revoke = %#v", response)
	}
	if _, err := runtime.bridge.ForgetThisDevice(map[string]any{"confirmRemoval": true}); err != nil {
		t.Fatalf("already-revoked credential could not be forgotten: %v", err)
	}
	if runtime.credentialStore.Inspect().Stored {
		t.Fatal("forget retained local credential")
	}
	store.mu.Lock()
	store.value = "corrupt local record"
	store.mu.Unlock()
	if _, err := runtime.bridge.RemoveLocalDesktopCredential(map[string]any{}); err == nil {
		t.Fatal("corrupt local credential removed without confirmation")
	}
	store.mu.Lock()
	unchanged := store.value == "corrupt local record"
	store.mu.Unlock()
	if !unchanged {
		t.Fatal("missing confirmation changed local credential")
	}
	if _, err := runtime.bridge.RemoveLocalDesktopCredential(map[string]any{"confirmRemoval": true}); err != nil {
		t.Fatalf("explicit corrupt local removal failed: %v", err)
	}
	if runtime.credentialStore.Inspect().State != "missing" {
		t.Fatal("corrupt local removal was not verified")
	}
}

func TestDesktopForgetDoesNotReportSuccessWhenDeleteReadbackFails(t *testing.T) {
	runtime, store, _ := desktopSessionRuntime(t, true)
	credential, _, err := runtime.credentialStore.Credential()
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.ignoreDelete = true
	store.mu.Unlock()
	if _, err := runtime.bridge.ForgetThisDevice(map[string]any{"currentPassword": desktopSessionPassword, "confirmRemoval": true}); err == nil {
		t.Fatal("unverified keyring deletion reported success")
	}
	if len(desktopAuthorityState(t, runtime).TrustedDevices) != 0 {
		t.Fatal("failed local deletion retained server trust")
	}
	if _, _, err := admin.AuthenticateDesktopDevice(t.Context(), credential.ID, credential.Secret); err == nil {
		t.Fatal("failed deletion left automatic access")
	}
	if _, err := runtime.console.Credentials(t.Context()); err == nil {
		t.Fatal("failed deletion retained native session")
	}
}

func TestDesktopCredentialOwnedByAnotherUserIsNotOverwritten(t *testing.T) {
	runtime, _, _ := desktopSessionRuntime(t, true)
	initial, err := runtime.console.Credentials(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	oldCredential, _, err := runtime.credentialStore.Credential()
	if err != nil {
		t.Fatal(err)
	}
	response := desktopBridgeRequest(t, runtime, http.MethodPost, "/admin/api/auth/users", `{"username":"security","password":"OtherSecurity8!","roles":["security_admin"]}`, initial.CSRFToken)
	if response["status"] != http.StatusCreated {
		t.Fatalf("create security user = %#v", response)
	}
	result, err := runtime.bridge.SignIn(map[string]any{"username": "security", "password": "OtherSecurity8!", "trustDevice": true})
	if err != nil || result["deviceTrustStatus"] != "unavailable" {
		t.Fatalf("other-user trust fallback = %#v, %v", result, err)
	}
	current := desktopConsumeHandoff(t, runtime, result)
	if current.Principal.Username != "security" {
		t.Fatal("fallback signed in wrong account")
	}
	saved, _, err := runtime.credentialStore.Credential()
	if err != nil || saved.ID != oldCredential.ID || saved.Secret != oldCredential.Secret {
		t.Fatal("other-user password login replaced saved credential")
	}
	if _, err := runtime.bridge.TrustThisDevice(map[string]any{"currentPassword": "OtherSecurity8!"}); err == nil {
		t.Fatal("explicit trust silently replaced another user's saved credential")
	}
	if _, err := runtime.bridge.SignOutDesktop(); err != nil {
		t.Fatal(err)
	}
	if !runtime.credentialStore.Inspect().AutomaticSignIn || !desktopAuthorityState(t, runtime).TrustedDevices[oldCredential.ID].AutomaticSignIn {
		t.Fatal("other-user sign-out altered the owner's saved trust")
	}
}

func TestDesktopRecoveryRevokesNativeSessionsAndTrustWithoutAutomaticLogin(t *testing.T) {
	runtime, store, recoveryCode := desktopSessionRuntime(t, true)
	before, err := runtime.console.Credentials(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.setErr = errors.New("vault write unavailable")
	store.mu.Unlock()
	result, err := runtime.bridge.RecoverPassword(map[string]any{"recoveryCode": recoveryCode, "newPassword": "RecoveredPassword8!", "confirmation": "RecoveredPassword8!"})
	if err != nil || result["recoveryCode"] == "" || result["recoveryCode"] == recoveryCode || result["loginRequired"] != true || result["storageIssue"] == nil {
		t.Fatalf("native recovery = %#v, %v", result, err)
	}
	if _, exists := result["handoffToken"]; exists {
		t.Fatal("password recovery created automatic login handoff")
	}
	if _, err := runtime.console.Credentials(t.Context()); err == nil || runtime.credentials.Token != "" {
		t.Fatal("recovery retained native credentials")
	}
	if _, err := admin.CreateDesktopSessionHandoff(before); err == nil {
		t.Fatal("recovery retained previously issued session")
	}
	if state := desktopAuthorityState(t, runtime); len(state.Sessions) != 0 || len(state.TrustedDevices) != 0 {
		t.Fatal("recovery retained authority sessions or trusted devices")
	}
	if _, err := runtime.bridge.AutomaticSignIn(); err == nil {
		t.Fatal("recovery enabled immediate automatic reauthentication")
	}
	desktopPasswordLogin(t, runtime, "admin", "RecoveredPassword8!", false)
}

func TestDesktopProxyFailureAfterReadinessBlocksNativeOperationsAndReleasesListener(t *testing.T) {
	runtime, _, _ := desktopSessionRuntime(t, false)
	address := runtime.listener.Addr().String()
	if err := runtime.listener.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runtime.proxyDone:
	case <-time.After(5 * time.Second):
		t.Fatal("listener failure was not monitored")
	}
	if err := runtime.ensureProxyStarted(); err == nil {
		t.Fatal("readiness stayed successful after listener failure")
	}
	if _, err := runtime.bridge.AdminRequest(map[string]any{"method": "GET", "path": "/admin/api/auth/session"}); err == nil {
		t.Fatal("native API continued after listener failure")
	}
	if _, err := runtime.bridge.SignIn(map[string]any{"password": desktopSessionPassword}); err == nil {
		t.Fatal("native sign-in continued after listener failure")
	}
	recorder := httptest.NewRecorder()
	runtime.serveDesktopConsole(recorder, httptest.NewRequest(http.MethodGet, desktopAssetOrigin()+"/admin/", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatal("static native handler continued after listener failure")
	}
	if err := runtime.close(); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("closed desktop retained listener: %v", err)
	}
	_ = listener.Close()
	if _, err := admin.SecuritySetupState(); err == nil {
		t.Fatal("closed desktop retained administrator authority")
	}
	if _, err := runtime.console.Credentials(context.Background()); err == nil {
		t.Fatal("closed desktop retained native session")
	}
}
