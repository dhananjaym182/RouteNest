package admin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"routenest/internal/adminauth"
	"routenest/internal/adminsso"
	"routenest/internal/platform"
	storeadminauth "routenest/internal/store/adminauth"
)

const AdminBootstrapPasswordEnv = "ROUTENEST_ADMIN_BOOTSTRAP_PASSWORD"

type SecurityOptions struct {
	DataDir           string
	ListenHost        string
	ListenPort        int
	CanonicalURL      string
	TrustedProxyCIDRs []string
	ProxyMode         bool
	EnableHSTS        bool
	BootstrapPassword string
	DeliverBootstrap  func(string) error
}

type adminSecurityRuntime struct {
	service        *adminauth.Service
	sso            *adminsso.Manager
	store          *storeadminauth.Store
	requestOptions adminauth.RequestSecurityOptions
	enableHSTS     bool
	cancel         context.CancelFunc
	done           chan struct{}
}

var adminSecurityState struct {
	sync.RWMutex
	runtime *adminSecurityRuntime
}

func ConfigureSecurity(opts SecurityOptions) error {
	adminSecurityState.RLock()
	alreadyConfigured := adminSecurityState.runtime != nil
	adminSecurityState.RUnlock()
	if alreadyConfigured {
		return nil
	}

	runtime, err := openAdminSecurity(opts)
	if err != nil {
		return err
	}
	adminSecurityState.Lock()
	if adminSecurityState.runtime != nil {
		adminSecurityState.Unlock()
		_ = runtime.close()
		return nil
	}
	adminSecurityState.runtime = runtime
	adminSecurityState.Unlock()
	return nil
}

func openAdminSecurity(opts SecurityOptions) (*adminSecurityRuntime, error) {
	requestOptions, err := requestSecurityOptions(opts)
	if err != nil {
		return nil, err
	}
	dataDir := opts.DataDir
	if dataDir == "" {
		dataDir = platform.CurrentDataDir()
	}
	authDir := filepath.Join(dataDir, "admin-auth")
	if err := platform.EnsurePrivateDir(authDir); err != nil {
		return nil, fmt.Errorf("prepare administrator authority directory: %w", err)
	}
	repo, err := storeadminauth.Open(authDir)
	if err != nil {
		return nil, fmt.Errorf("open administrator authority: %w", err)
	}
	bootstrapPassword := opts.BootstrapPassword
	if bootstrapPassword == "" {
		bootstrapPassword = os.Getenv(AdminBootstrapPasswordEnv)
	}
	deliver := opts.DeliverBootstrap
	if deliver == nil {
		deliver = deliverBootstrapToTTY
	}
	service, err := adminauth.NewService(repo, adminauth.Options{
		BootstrapPassword:  bootstrapPassword,
		DeliverBootstrap:   deliver,
		Legacy:             legacyAdminCredential(),
		MarkLegacyMigrated: markLegacyAdminMigrated,
	})
	if err != nil {
		_ = repo.Close()
		return nil, fmt.Errorf("initialize administrator identity: %w", err)
	}
	secretDir := filepath.Join(authDir, "sso-secrets")
	if err := platform.EnsurePrivateDir(secretDir); err != nil {
		_ = repo.Close()
		return nil, fmt.Errorf("prepare SSO secret directory: %w", err)
	}
	sso, err := adminsso.NewManager(context.Background(), adminsso.ManagerOptions{
		Service:      service,
		CanonicalURL: requestOptions.CanonicalURL,
		SecretRoots:  []string{secretDir},
	})
	if err != nil {
		_ = repo.Close()
		return nil, fmt.Errorf("initialize administrator SSO: %w", err)
	}
	runtime := &adminSecurityRuntime{
		service:        service,
		sso:            sso,
		store:          repo,
		requestOptions: requestOptions,
		enableHSTS:     opts.EnableHSTS,
	}
	runtime.startDenialFlusher()
	if err := runtime.sso.StartRefresh(context.Background(), 5*time.Minute, 30*time.Second); err != nil {
		_ = runtime.close()
		return nil, fmt.Errorf("start administrator SSO readiness refresh: %w", err)
	}
	return runtime, nil
}

func requestSecurityOptions(opts SecurityOptions) (adminauth.RequestSecurityOptions, error) {
	trusted := make([]*net.IPNet, 0, len(opts.TrustedProxyCIDRs))
	for _, raw := range opts.TrustedProxyCIDRs {
		_, network, err := net.ParseCIDR(strings.TrimSpace(raw))
		if err != nil {
			return adminauth.RequestSecurityOptions{}, fmt.Errorf("invalid trusted proxy CIDR %q: %w", raw, err)
		}
		trusted = append(trusted, network)
	}
	if opts.ProxyMode && len(trusted) == 0 {
		return adminauth.RequestSecurityOptions{}, fmt.Errorf("trusted-proxy mode requires at least one trusted proxy CIDR")
	}
	if opts.ProxyMode && strings.TrimSpace(opts.CanonicalURL) == "" {
		return adminauth.RequestSecurityOptions{}, fmt.Errorf("trusted-proxy mode requires an explicit canonical administrator URL")
	}
	if !listenHostIsLoopback(opts.ListenHost) && !opts.ProxyMode {
		return adminauth.RequestSecurityOptions{}, fmt.Errorf("non-loopback administration requires explicit trusted-proxy mode and CIDRs")
	}
	canonicalRaw := opts.CanonicalURL
	if canonicalRaw == "" {
		host := effectiveLoopbackHost(opts.ListenHost)
		if opts.ListenPort <= 0 || opts.ListenPort > 65535 {
			return adminauth.RequestSecurityOptions{}, fmt.Errorf("invalid administrator listen port")
		}
		canonicalRaw = "http://" + net.JoinHostPort(host, fmt.Sprintf("%d", opts.ListenPort))
	}
	canonical, err := url.Parse(canonicalRaw)
	if err != nil {
		return adminauth.RequestSecurityOptions{}, fmt.Errorf("invalid administrator canonical URL: %w", err)
	}
	scheme, _, err := adminauth.NormalizeCanonicalURL(canonical)
	if err != nil {
		return adminauth.RequestSecurityOptions{}, err
	}
	if opts.EnableHSTS && scheme != "https" {
		return adminauth.RequestSecurityOptions{}, fmt.Errorf("administrator HSTS requires an HTTPS canonical URL")
	}
	return adminauth.RequestSecurityOptions{
		CanonicalURL:      canonical,
		TrustedProxyCIDRs: trusted,
		ProxyMode:         opts.ProxyMode,
	}, nil
}

func listenHostIsLoopback(host string) bool {
	switch strings.TrimSpace(strings.ToLower(host)) {
	case "", "localhost":
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func effectiveLoopbackHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return "127.0.0.1"
	}
	return strings.Trim(host, "[]")
}

func deliverBootstrapToTTY(secret string) (err error) {
	tty, err := openControllingTerminal()
	if err != nil {
		return fmt.Errorf("no controlling terminal; set %s for this one-time bootstrap: %w", AdminBootstrapPasswordEnv, err)
	}
	defer func() { err = errors.Join(err, tty.Close()) }()
	_, err = fmt.Fprintf(tty, "\nRouteNest one-time administrator bootstrap secret:\n\n  %s\n\nOpen the local admin console and replace it with a permanent password. This secret is not stored in plaintext and will not be shown again.\n\n", secret)
	return err
}

func GenerateBootstrapSecret() (string, error) {
	buffer := make([]byte, 24)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate administrator bootstrap secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func SecurityBootstrapRequired() (bool, error) {
	runtime, err := configuredAdminSecurity()
	if err != nil {
		return false, err
	}
	state, err := runtime.service.State(context.Background())
	if err != nil {
		return false, err
	}
	return !state.Initialized || state.RecoveryPHC != "", nil
}

func configuredAdminSecurity() (*adminSecurityRuntime, error) {
	adminSecurityState.RLock()
	defer adminSecurityState.RUnlock()
	if adminSecurityState.runtime == nil || adminSecurityState.runtime.service == nil {
		return nil, fmt.Errorf("administrator security is not configured")
	}
	return adminSecurityState.runtime, nil
}

func CloseSecurity() error {
	adminSecurityState.Lock()
	runtime := adminSecurityState.runtime
	adminSecurityState.runtime = nil
	adminSecurityState.Unlock()
	if runtime == nil || runtime.store == nil {
		return nil
	}
	return runtime.close()
}

func (runtime *adminSecurityRuntime) close() error {
	if runtime.cancel != nil {
		runtime.cancel()
	}
	if runtime.done != nil {
		<-runtime.done
	}
	var errs []error
	if runtime.sso != nil {
		errs = append(errs, runtime.sso.Close())
	}
	if runtime.store != nil {
		errs = append(errs, runtime.store.Close())
	}
	return errors.Join(errs...)
}

func (runtime *adminSecurityRuntime) startDenialFlusher() {
	ctx, cancel := context.WithCancel(context.Background())
	runtime.cancel = cancel
	runtime.done = make(chan struct{})
	go func() {
		defer close(runtime.done)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = runtime.service.FlushDenials(context.Background())
				return
			case <-ticker.C:
				_ = runtime.service.FlushDenials(ctx)
			}
		}
	}()
}
