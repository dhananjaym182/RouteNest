//go:build desktop

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"routenest/internal/admin"
	"routenest/internal/adminauth"
	"routenest/internal/app"
	"routenest/internal/proxycore"
)

type desktopRuntime struct {
	host       string
	port       int
	adminURL   string
	bridge     *DesktopSetupBridge
	proxyOnce  sync.Once
	proxyErr   chan error
	proxyStart error
	bootstrap  string
}

func prepareDesktopRuntime(host string, port int, initializeEmpty, selfCheck bool) (*desktopRuntime, error) {
	runtime := &desktopRuntime{port: port}
	runtime.setHost(host)
	if selfCheck || initializeEmpty {
		if err := prepareClientAuthenticationStorage(initializeEmpty); err != nil {
			return nil, err
		}
		if runtime.host == "" {
			runtime.setHost(configuredDesktopHost())
		}
		if err := runtime.configureSecurity(!selfCheck); err != nil {
			return nil, err
		}
		if selfCheck {
			if err := runtime.ensureProxyStarted("/ready"); err != nil {
				return nil, err
			}
			return runtime, nil
		}
		return runtime.finishPreparedState()
	}

	fresh, inspectErr := freshClientAuthenticationStorageAllowed()
	if inspectErr != nil {
		runtime.bridge = newDesktopSetupBridge(desktopSetupState{
			Mode:    "error",
			Message: "RouteNest cannot verify client authentication storage. Restore the account store and initialization witness, then reopen the application. Details: " + inspectErr.Error(),
		}, nil)
		return runtime, nil
	}
	if fresh {
		if runtime.host == "" {
			runtime.setHost("127.0.0.1")
		}
		runtime.bridge = newDesktopSetupBridge(desktopSetupState{Mode: "setup", AdminURL: runtime.adminURL}, runtime.completeFreshSetup)
		return runtime, nil
	}

	if err := prepareClientAuthenticationStorage(false); err != nil {
		runtime.bridge = newDesktopSetupBridge(desktopSetupState{
			Mode:    "error",
			Message: "RouteNest authentication storage is unavailable. Restore or repair the existing data directory; the application will not reinitialize it. Details: " + err.Error(),
		}, nil)
		return runtime, nil
	}
	if runtime.host == "" {
		runtime.setHost(configuredDesktopHost())
	}
	if err := runtime.configureSecurity(true); err != nil {
		runtime.bridge = newDesktopSetupBridge(desktopSetupState{
			Mode:    "error",
			Message: "RouteNest administrator security state is unavailable. Existing credentials were not reset. Use the documented offline recovery procedure if the authority is intact. Details: " + err.Error(),
		}, nil)
		return runtime, nil
	}
	return runtime.finishPreparedState()
}

func (runtime *desktopRuntime) setHost(host string) {
	runtime.host = strings.TrimSpace(host)
	browserHost := strings.Trim(runtime.host, "[]")
	if ip := net.ParseIP(browserHost); ip == nil || !ip.IsLoopback() {
		if !strings.EqualFold(browserHost, "localhost") {
			browserHost = "127.0.0.1"
		}
	}
	if browserHost == "" {
		browserHost = "127.0.0.1"
	}
	runtime.adminURL = "http://" + net.JoinHostPort(browserHost, fmt.Sprintf("%d", runtime.port)) + "/admin/"
}

func (runtime *desktopRuntime) configureSecurity(nativeSetup bool) error {
	app.Configure()
	options, err := admin.SecurityOptionsFromEnvironment(admin.SecurityOptions{ListenHost: runtime.host, ListenPort: runtime.port})
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
	return nil
}

func (runtime *desktopRuntime) finishPreparedState() (*desktopRuntime, error) {
	status, err := admin.SecuritySetupState()
	if err != nil {
		return nil, err
	}
	if status.BootstrapRequired {
		runtime.bridge = newDesktopSetupBridge(desktopSetupState{Mode: "setup", AdminURL: runtime.adminURL}, runtime.completePreparedSetup)
		return runtime, nil
	}
	runtime.bootstrap = ""
	if err := runtime.ensureProxyStarted("/health"); err != nil {
		return nil, err
	}
	// Offline recovery is completed by the existing localhost administrator UI,
	// which accepts the owner-terminal recovery secret. Native first-run setup
	// never substitutes a newly generated bootstrap capability for that proof.
	runtime.bridge = newDesktopSetupBridge(desktopSetupState{Mode: "ready", AdminURL: runtime.adminURL}, nil)
	return runtime, nil
}

func (runtime *desktopRuntime) completeFreshSetup(ctx context.Context, password string) (desktopSetupResult, error) {
	fresh, err := freshClientAuthenticationStorageAllowed()
	if err != nil || !fresh {
		if err == nil {
			err = errors.New("installation state changed while setup was open")
		}
		return desktopSetupResult{}, err
	}
	if err := prepareClientAuthenticationStorage(true); err != nil {
		return desktopSetupResult{}, err
	}
	if err := runtime.configureSecurity(true); err != nil {
		return desktopSetupResult{}, err
	}
	return runtime.completePreparedSetup(ctx, password)
}

func (runtime *desktopRuntime) completePreparedSetup(ctx context.Context, password string) (desktopSetupResult, error) {
	if runtime.bootstrap == "" {
		return desktopSetupResult{}, errors.New("desktop setup capability is unavailable")
	}
	if err := runtime.ensureProxyStarted("/health"); err != nil {
		return desktopSetupResult{}, err
	}
	recoveryCode, handoffToken, err := admin.CompleteDesktopBootstrap(ctx, runtime.bootstrap, password)
	result, err := desktopBootstrapCompletion(runtime.adminURL, recoveryCode, handoffToken, err)
	if err != nil {
		return desktopSetupResult{}, err
	}
	runtime.bootstrap = ""
	return result, nil
}

func desktopBootstrapCompletion(adminURL, recoveryCode, handoffToken string, err error) (desktopSetupResult, error) {
	if err != nil {
		if recoveryCode != "" && (errors.Is(err, adminauth.ErrMutationPrepared) || errors.Is(err, admin.ErrDesktopHandoffUnavailable)) {
			return desktopSetupResult{RecoveryCode: recoveryCode, AdminURL: adminURL, RestartRequired: true}, nil
		}
		return desktopSetupResult{}, err
	}
	return desktopSetupResult{RecoveryCode: recoveryCode, HandoffToken: handoffToken, AdminURL: adminURL}, nil
}

func (runtime *desktopRuntime) ensureProxyStarted(probePath string) error {
	runtime.proxyOnce.Do(func() {
		runtime.proxyErr = make(chan error, 1)
		go func() {
			runtime.proxyErr <- proxycore.Start(runtime.host, runtime.port, admin.RegisterRoutes)
		}()
		runtime.proxyStart = waitForEmbeddedProxy(runtime.adminURL, probePath, 15*time.Second, runtime.proxyErr)
	})
	return runtime.proxyStart
}

func (runtime *desktopRuntime) close() error {
	return admin.CloseSecurity()
}

func (runtime *desktopRuntime) selfCheckMessage() string {
	return fmt.Sprintf("selfcheck ok: embedded proxy ready at http://127.0.0.1:%d/ready", runtime.port)
}

func isExpectedProxyShutdown(err error) bool {
	return err == nil || errors.Is(err, http.ErrServerClosed)
}
