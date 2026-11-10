//go:build desktop

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"routenest/internal/admin"
	"routenest/internal/adminauth"
	"routenest/internal/app"
	"routenest/internal/desktopauth"
	"routenest/internal/proxycore"
)

var errDesktopUnavailable = errors.New("desktop security is unavailable")

type desktopRuntime struct {
	host               string
	port               int
	adminURL           string
	gatewayURL         string
	dataDir            string
	bridge             *DesktopSetupBridge
	console            *admin.DesktopConsole
	credentialStore    *desktopauth.Manager
	credentials        adminauth.Credentials
	setupPending       bool
	deviceID           string
	deviceUserID       string
	deviceExpiresAt    time.Time
	deviceStatus       string
	deviceAutomatic    bool
	authMu             sync.Mutex
	lifecycleMu        sync.Mutex
	lifecycleContext   context.Context
	lifecycleCancel    context.CancelFunc
	windowStarted      bool
	trustedWindow      bool
	closed             bool
	securityConfigured bool
	listener           net.Listener
	proxyOnce          sync.Once
	proxyDone          chan struct{}
	proxyReady         bool
	proxyFailure       error
	proxyStart         error
	closeOnce          sync.Once
	closeErr           error
	bootstrap          string
}

func prepareDesktopRuntime(host string, port int, initializeEmpty, selfCheck bool) (_ *desktopRuntime, resultErr error) {
	dataDir, err := admin.SecurityDataDir()
	if err != nil {
		return nil, err
	}
	runtime := &desktopRuntime{port: port, dataDir: dataDir, credentialStore: desktopauth.NewManagerForInstallation(dataDir)}
	runtime.setHost(host)
	defer func() {
		if resultErr != nil {
			_ = runtime.close()
		}
	}()

	fresh, inspectErr := freshClientAuthenticationStorageAllowed()
	if selfCheck || initializeEmpty {
		if runtime.host == "" {
			if fresh {
				runtime.setHost(desktopFreshHost())
			} else {
				runtime.setHost(configuredDesktopHost())
			}
		}
		if err := runtime.reserveListener(); err != nil {
			return nil, err
		}
		if err := prepareClientAuthenticationStorage(initializeEmpty); err != nil {
			return nil, err
		}
		if err := runtime.configureSecurity(!selfCheck); err != nil {
			return nil, err
		}
		if selfCheck {
			if err := runtime.ensureProxyStarted(); err != nil {
				return nil, err
			}
			return runtime, nil
		}
		return runtime.finishPreparedState()
	}

	if inspectErr != nil {
		runtime.bridge = runtime.newBridge(desktopSetupState{
			Mode:    "error",
			Message: "RouteNest cannot verify client authentication storage. Restore the account store and initialization witness, then reopen the application. Details: " + inspectErr.Error(),
		}, nil)
		return runtime, nil
	}
	if fresh {
		if runtime.host == "" {
			runtime.setHost(desktopFreshHost())
		}
		runtime.bridge = runtime.newBridge(desktopSetupState{Mode: "setup"}, runtime.completeFreshSetup)
		return runtime, nil
	}

	if err := prepareClientAuthenticationStorage(false); err != nil {
		runtime.bridge = runtime.newBridge(desktopSetupState{
			Mode:    "error",
			Message: "RouteNest authentication storage is unavailable. Restore or repair the existing data directory; the application will not reinitialize it. Details: " + err.Error(),
		}, nil)
		return runtime, nil
	}
	if runtime.host == "" {
		runtime.setHost(configuredDesktopHost())
	}
	if err := runtime.configureSecurity(true); err != nil {
		_ = runtime.close()
		runtime.bridge = runtime.newBridge(desktopSetupState{
			Mode:    "error",
			Message: "RouteNest administrator security state is unavailable. Existing credentials were not reset. Use the documented offline recovery procedure if the authority is intact. Details: " + err.Error(),
		}, nil)
		return runtime, nil
	}
	return runtime.finishPreparedState()
}

func desktopFreshHost() string {
	if host := strings.TrimSpace(os.Getenv("CLINE_PROXY_HOST")); host != "" {
		return host
	}
	return "127.0.0.1"
}

func (runtime *desktopRuntime) setHost(host string) {
	runtime.host = strings.TrimSpace(host)
	gatewayHost := strings.Trim(runtime.host, "[]")
	switch gatewayHost {
	case "", "0.0.0.0":
		gatewayHost = "127.0.0.1"
	case "::":
		gatewayHost = "::1"
	}
	runtime.adminURL = "/admin/"
	runtime.gatewayURL = "http://" + net.JoinHostPort(gatewayHost, strconv.Itoa(runtime.port)) + "/v1"
}

func (runtime *desktopRuntime) reserveListener() error {
	runtime.lifecycleMu.Lock()
	defer runtime.lifecycleMu.Unlock()
	if runtime.closed || runtime.proxyFailure != nil {
		return errDesktopUnavailable
	}
	if runtime.listener != nil {
		return nil
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(strings.Trim(runtime.host, "[]"), strconv.Itoa(runtime.port)))
	if err != nil {
		return fmt.Errorf("reserve embedded gateway listener: %w", err)
	}
	runtime.listener = listener
	runtime.port = listener.Addr().(*net.TCPAddr).Port
	runtime.setHost(runtime.host)
	runtime.ensureLifecycleLocked()
	return nil
}

func (runtime *desktopRuntime) ensureLifecycleLocked() {
	if runtime.lifecycleContext == nil {
		runtime.lifecycleContext, runtime.lifecycleCancel = context.WithCancel(context.Background())
	}
}

func (runtime *desktopRuntime) configureSecurity(nativeSetup bool) error {
	if err := runtime.reserveListener(); err != nil {
		return err
	}
	app.Configure()
	options, err := admin.SecurityOptionsFromEnvironment(admin.SecurityOptions{DataDir: runtime.dataDir, ListenHost: runtime.host, ListenPort: runtime.port})
	if err != nil {
		return fmt.Errorf("administrator security configuration failed: %w", err)
	}
	if nativeSetup {
		runtime.bootstrap, err = admin.GenerateBootstrapSecret()
		if err != nil {
			return err
		}
		options.BootstrapPassword = runtime.bootstrap
	}
	if err := admin.ConfigureSecurity(options); err != nil {
		return fmt.Errorf("administrator security failed: %w", err)
	}
	runtime.securityConfigured = true
	runtime.console, err = admin.NewDesktopConsole()
	return err
}

func (runtime *desktopRuntime) finishPreparedState() (*desktopRuntime, error) {
	status, err := admin.SecuritySetupState()
	if err != nil {
		return nil, err
	}
	if err := runtime.ensureProxyStarted(); err != nil {
		return nil, err
	}
	if status.BootstrapRequired {
		runtime.bridge = runtime.newBridge(desktopSetupState{Mode: "setup"}, runtime.completePreparedSetup)
		return runtime, nil
	}
	runtime.bootstrap = ""
	if status.RecoveryRequired {
		runtime.bridge = runtime.newBridge(desktopSetupState{Mode: "recovery"}, nil)
		return runtime, nil
	}
	if status.LegacyRotationRequired {
		runtime.bridge = runtime.newBridge(desktopSetupState{Mode: "legacy"}, nil)
		return runtime, nil
	}
	runtime.bridge = runtime.newBridge(desktopSetupState{Mode: "login"}, nil)
	return runtime, nil
}

func (runtime *desktopRuntime) completeFreshSetup(ctx context.Context, password string, trustDevice bool) (desktopSetupResult, error) {
	runtime.authMu.Lock()
	defer runtime.authMu.Unlock()
	ctx, cancel, err := runtime.nativeOperationContext(ctx, false)
	if err != nil {
		return desktopSetupResult{}, err
	}
	defer cancel()
	if runtime.bootstrap != "" && runtime.securityConfigured {
		return runtime.completePreparedSetupLocked(ctx, password, trustDevice)
	}
	fresh, err := freshClientAuthenticationStorageAllowed()
	if err != nil || !fresh {
		if err == nil {
			err = errors.New("installation state changed while setup was open")
		}
		return desktopSetupResult{}, err
	}
	if err := runtime.reserveListener(); err != nil {
		return desktopSetupResult{}, err
	}
	if err := prepareClientAuthenticationStorage(true); err != nil {
		return desktopSetupResult{}, err
	}
	if err := runtime.configureSecurity(true); err != nil {
		return desktopSetupResult{}, err
	}
	return runtime.completePreparedSetupLocked(ctx, password, trustDevice)
}

func (runtime *desktopRuntime) completePreparedSetup(ctx context.Context, password string, trustDevice bool) (desktopSetupResult, error) {
	runtime.authMu.Lock()
	defer runtime.authMu.Unlock()
	ctx, cancel, err := runtime.nativeOperationContext(ctx, false)
	if err != nil {
		return desktopSetupResult{}, err
	}
	defer cancel()
	return runtime.completePreparedSetupLocked(ctx, password, trustDevice)
}

func (runtime *desktopRuntime) completePreparedSetupLocked(ctx context.Context, password string, trustDevice bool) (desktopSetupResult, error) {
	if runtime.bootstrap == "" {
		return desktopSetupResult{}, errors.New("desktop setup capability is unavailable")
	}
	if err := runtime.ensureProxyStarted(); err != nil {
		return desktopSetupResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return desktopSetupResult{}, err
	}
	recoveryCode, credentials, err := admin.CompleteDesktopBootstrapCredentials(ctx, runtime.bootstrap, password)
	result, completionErr := desktopBootstrapCompletion(recoveryCode, err)
	if completionErr != nil {
		return desktopSetupResult{}, completionErr
	}
	runtime.bootstrap = ""
	runtime.credentials = credentials
	runtime.setupPending = !result.RestartRequired
	result.DeviceTrustStatus = "not_requested"
	if trustDevice && credentials.Token != "" && !result.RestartRequired {
		if err := runtime.enrollDesktopCredentialLocked(ctx, credentials, "", true); err != nil {
			result.DeviceTrustStatus = "unavailable"
			if desktopTrustRestartRequired(err) {
				result.RestartRequired = true
				runtime.credentials = adminauth.Credentials{}
				runtime.setupPending = false
			}
		} else {
			result.DeviceTrustStatus = "trusted"
		}
	}
	if runtime.credentialStore != nil {
		result.CredentialStorage = runtime.credentialStore.Inspect()
	}
	return result, nil
}

func desktopBootstrapCompletion(recoveryCode string, err error) (desktopSetupResult, error) {
	if err != nil {
		if recoveryCode != "" && (errors.Is(err, adminauth.ErrMutationPrepared) || errors.Is(err, admin.ErrDesktopHandoffUnavailable)) {
			return desktopSetupResult{RecoveryCode: recoveryCode, AdminURL: "/admin/", RestartRequired: true}, nil
		}
		return desktopSetupResult{}, err
	}
	return desktopSetupResult{RecoveryCode: recoveryCode, AdminURL: "/admin/"}, nil
}

func (runtime *desktopRuntime) ensureProxyStarted() error {
	runtime.proxyOnce.Do(func() {
		if err := runtime.reserveListener(); err != nil {
			runtime.proxyStart = err
			return
		}
		runtime.lifecycleMu.Lock()
		ctx, listener := runtime.lifecycleContext, runtime.listener
		runtime.proxyDone = make(chan struct{})
		done := runtime.proxyDone
		runtime.lifecycleMu.Unlock()
		ready := make(chan struct{})
		go runtime.runProxy(ctx, listener, ready, done)
		timer := time.NewTimer(15 * time.Second)
		defer timer.Stop()
		select {
		case <-ready:
		case <-done:
		case <-timer.C:
			runtime.failProxy(errors.New("embedded gateway startup timed out"))
		case <-ctx.Done():
		}
		runtime.proxyStart = runtime.proxyAvailability()
	})
	if runtime.proxyStart != nil {
		return runtime.proxyStart
	}
	return runtime.proxyAvailability()
}

func (runtime *desktopRuntime) runProxy(ctx context.Context, listener net.Listener, ready, done chan struct{}) {
	defer close(done)
	err := proxycore.StartWithListener(ctx, listener, admin.RegisterRoutes, func() {
		runtime.lifecycleMu.Lock()
		if !runtime.closed && runtime.proxyFailure == nil && ctx.Err() == nil {
			runtime.proxyReady = true
		}
		runtime.lifecycleMu.Unlock()
		close(ready)
	})
	if err == nil {
		err = errors.New("embedded gateway stopped unexpectedly")
	}
	runtime.failProxy(err)
}

func (runtime *desktopRuntime) failProxy(err error) {
	runtime.lifecycleMu.Lock()
	defer runtime.lifecycleMu.Unlock()
	if !runtime.closed && runtime.proxyFailure == nil {
		runtime.proxyFailure = err
	}
	runtime.proxyReady = false
	if runtime.lifecycleCancel != nil {
		runtime.lifecycleCancel()
	}
	if runtime.listener != nil {
		_ = runtime.listener.Close()
	}
}

func (runtime *desktopRuntime) proxyAvailability() error {
	runtime.lifecycleMu.Lock()
	defer runtime.lifecycleMu.Unlock()
	if runtime.proxyFailure != nil {
		return fmt.Errorf("embedded gateway unavailable: %w", runtime.proxyFailure)
	}
	if runtime.closed || !runtime.proxyReady {
		return errDesktopUnavailable
	}
	return nil
}

func desktopTrustedWindowContext(ctx context.Context) bool {
	if ctx == nil || ctx.Err() != nil {
		return false
	}
	buildType, ok := ctx.Value("buildtype").(string)
	if !ok || buildType != "production" && buildType != "debug" {
		return false
	}
	for _, key := range []string{"frontenddevserverurl", "devserver", "assetdir"} {
		if ctx.Value(key) != nil {
			return false
		}
	}
	return true
}

func (runtime *desktopRuntime) onWindowStartup(ctx context.Context) {
	runtime.lifecycleMu.Lock()
	defer runtime.lifecycleMu.Unlock()
	if runtime.closed || runtime.windowStarted {
		return
	}
	runtime.ensureLifecycleLocked()
	runtime.windowStarted = true
	runtime.trustedWindow = desktopTrustedWindowContext(ctx)
}

func (runtime *desktopRuntime) nativeOperationContext(ctx context.Context, requireProxy bool) (context.Context, context.CancelFunc, error) {
	runtime.lifecycleMu.Lock()
	defer runtime.lifecycleMu.Unlock()
	if ctx == nil || ctx.Err() != nil || !runtime.windowStarted || !runtime.trustedWindow || runtime.closed || runtime.proxyFailure != nil || requireProxy && !runtime.proxyReady {
		return nil, nil, errDesktopUnavailable
	}
	runtime.ensureLifecycleLocked()
	operationContext, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(runtime.lifecycleContext, cancel)
	return operationContext, func() { stop(); cancel() }, nil
}

func (runtime *desktopRuntime) serveDesktopConsole(w http.ResponseWriter, request *http.Request) {
	if !desktopAssetRequestAllowed(request) {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	runtime.authMu.Lock()
	defer runtime.authMu.Unlock()
	ctx, cancel, err := runtime.nativeOperationContext(request.Context(), true)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	defer cancel()
	if runtime.console == nil {
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	runtime.console.ServeHTTP(w, request.WithContext(ctx))
}

func (runtime *desktopRuntime) close() error {
	runtime.closeOnce.Do(func() {
		runtime.lifecycleMu.Lock()
		runtime.closed = true
		runtime.trustedWindow = false
		runtime.proxyReady = false
		cancel, listener := runtime.lifecycleCancel, runtime.listener
		runtime.lifecycleMu.Unlock()
		if cancel != nil {
			cancel()
		}
		if listener != nil {
			_ = listener.Close()
		}
		runtime.authMu.Lock()
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		runtime.closeErr = runtime.clearDesktopSessionsLocked(ctx)
		stop()
		runtime.bootstrap = ""
		runtime.lifecycleMu.Lock()
		done := runtime.proxyDone
		runtime.lifecycleMu.Unlock()
		runtime.authMu.Unlock()
		if done != nil {
			<-done
		}
		if runtime.securityConfigured {
			runtime.closeErr = errors.Join(runtime.closeErr, admin.CloseSecurity())
		}
	})
	return runtime.closeErr
}

func (runtime *desktopRuntime) selfCheckMessage() string {
	return "selfcheck ok: embedded proxy ready at " + strings.TrimSuffix(runtime.gatewayURL, "/v1") + "/ready"
}

func isExpectedProxyShutdown(err error) bool {
	return err == nil || errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled)
}
