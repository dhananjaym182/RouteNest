package adminsso

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"routenest/internal/adminauth"
)

type lifecycleRoundTripper func(*http.Request) (*http.Response, error)

func (f lifecycleRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type blockingLifecycleTransport struct {
	entered   chan struct{}
	cancelled chan struct{}
	release   chan struct{}
	once      sync.Once
}

func newBlockingLifecycleTransport(t *testing.T) *blockingLifecycleTransport {
	t.Helper()
	transport := &blockingLifecycleTransport{entered: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(transport.unblock)
	return transport
}

func (t *blockingLifecycleTransport) unblock() { t.once.Do(func() { close(t.release) }) }

func (t *blockingLifecycleTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	close(t.entered)
	<-r.Context().Done()
	close(t.cancelled)
	<-t.release
	return nil, r.Context().Err()
}

func waitForLifecycle(t *testing.T, channel <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func installLifecycleProvider(t *testing.T, service *adminauth.Service, principal adminauth.Principal) {
	t.Helper()
	t.Setenv("ROUTENEST_TEST_MISSING_SSO_SECRET", "lifecycle-secret")
	config := managerOIDCConfig(true)
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Mutate(t.Context(), principal, adminauth.AuditSSOChange, config.ID, func(state *adminauth.State) error {
		state.Providers[config.ID] = adminauth.ProviderState{ID: config.ID, Type: config.Type, Enabled: true, Configuration: raw}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func lifecycleManager(t *testing.T, service *adminauth.Service, ctx context.Context, client *http.Client) *Manager {
	t.Helper()
	canonical, _ := url.Parse("http://127.0.0.1:8080")
	manager, err := NewManager(ctx, ManagerOptions{Service: service, CanonicalURL: canonical, HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

func TestManagerCloseCancelsRefreshAndWaitsForHTTP(t *testing.T) {
	service, admin := newManagerTestService(t)
	installLifecycleProvider(t, service, admin.Principal)
	transport := newBlockingLifecycleTransport(t)
	manager := lifecycleManager(t, service, t.Context(), &http.Client{Transport: transport})
	// Unblock before manager cleanup if an assertion fails.
	t.Cleanup(transport.unblock)
	refreshDone := make(chan error, 1)
	go func() { refreshDone <- manager.Refresh(context.Background()) }()
	waitForLifecycle(t, transport.entered, "refresh HTTP request")
	closed := make(chan struct{})
	go func() { _ = manager.Close(); close(closed) }()
	waitForLifecycle(t, transport.cancelled, "refresh cancellation")
	select {
	case <-closed:
		t.Fatal("Close returned before provider HTTP work finished")
	default:
	}
	transport.unblock()
	waitForLifecycle(t, closed, "manager shutdown")
	if err := <-refreshDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled refresh = %v", err)
	}
	if err := manager.Refresh(t.Context()); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("refresh after Close = %v", err)
	}
	if _, err := manager.LoginProviders(t.Context()); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("provider listing after Close = %v", err)
	}
	if _, _, err := manager.resolver.Resolve(SecretRef{Kind: "env", Name: "ROUTENEST_TEST_MISSING_SSO_SECRET"}); err == nil {
		t.Fatal("Close left secret resolver open")
	}
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			if err := manager.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
}

func TestManagerCancelledRefreshPreservesReadiness(t *testing.T) {
	service, admin := newManagerTestService(t)
	installLifecycleProvider(t, service, admin.Principal)
	transport := newBlockingLifecycleTransport(t)
	manager := lifecycleManager(t, service, t.Context(), &http.Client{Transport: transport})
	t.Cleanup(transport.unblock)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- manager.Refresh(ctx) }()
	waitForLifecycle(t, transport.entered, "refresh HTTP request")
	cancel()
	waitForLifecycle(t, transport.cancelled, "request cancellation")
	transport.unblock()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled refresh = %v", err)
	}
	providers, err := manager.LoginProviders(t.Context())
	if err != nil || len(providers) != 1 || providers[0].Reason != "readiness_pending" {
		t.Fatalf("cancelled refresh overwrote readiness: %+v, %v", providers, err)
	}
}

func TestManagerStateLockWaitHonorsCancellation(t *testing.T) {
	service, _ := newManagerTestService(t)
	manager := newManagerForTest(t, service)
	if err := manager.lockState(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer manager.unlockState()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := manager.Refresh(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("refresh waiting for state = %v", err)
		}
	}()
	waitForLifecycle(t, done, "cancelled state-lock wait")
}

func TestManagerPeriodicRefreshStopsWithContextAndReusesClient(t *testing.T) {
	service, admin := newManagerTestService(t)
	installLifecycleProvider(t, service, admin.Principal)
	var count atomic.Int64
	requests := make(chan struct{}, 32)
	transport := lifecycleRoundTripper(func(r *http.Request) (*http.Response, error) {
		count.Add(1)
		requests <- struct{}{}
		body := `{"issuer":"https://issuer.invalid","authorization_endpoint":"https://issuer.invalid/auth","token_endpoint":"https://issuer.invalid/token","jwks_uri":"https://issuer.invalid/jwks","id_token_signing_alg_values_supported":["RS256"]}`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	client := &http.Client{Transport: transport}
	managerCtx, cancelManager := context.WithCancel(t.Context())
	defer cancelManager()
	manager := lifecycleManager(t, service, managerCtx, client)
	if got := oidcBoundedClient(manager.oidcClient); got != manager.oidcClient {
		t.Fatal("manager adapter created another bounded client")
	}
	if _, changed := client.Transport.(lifecycleRoundTripper); !changed {
		t.Fatal("manager modified the supplied client")
	}
	if err := manager.StartRefresh(t.Context(), 5*time.Millisecond, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := manager.StartRefresh(t.Context(), time.Second, time.Second); err == nil {
		t.Fatal("duplicate periodic refresh loop accepted")
	}
	for range 3 {
		waitForLifecycle(t, requests, "periodic refresh")
	}
	cancelManager()
	stopped := make(chan struct{})
	go func() { manager.active.Wait(); close(stopped) }()
	waitForLifecycle(t, stopped, "periodic refresh shutdown")
	finalCount := count.Load()
	time.Sleep(20 * time.Millisecond)
	if got := count.Load(); got != finalCount {
		t.Fatalf("refresh continued after context cancellation: %d -> %d", finalCount, got)
	}
	if err := manager.Refresh(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("operation after manager context cancellation = %v", err)
	}
}

func TestManagerCloseCancelsDetachedOIDCJWKSFetch(t *testing.T) {
	fixture := newOIDCIssuerFixture(t)
	fixture.callbackURL = fixture.server.URL + "/admin/api/auth/oidc/callback"
	service, admin := newManagerTestService(t)
	blocker := newBlockingLifecycleTransport(t)
	base := fixture.server.Client().Transport
	client := &http.Client{Transport: lifecycleRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/jwks" {
			return blocker.RoundTrip(r)
		}
		return base.RoundTrip(r)
	})}
	canonical, _ := url.Parse(fixture.server.URL)
	manager, err := NewManager(t.Context(), ManagerOptions{Service: service, CanonicalURL: canonical, HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	t.Cleanup(blocker.unblock)
	config := fixture.config(t)
	view, err := manager.Upsert(t.Context(), admin.Principal, ProviderConfig{ID: "fixture", Type: adminauth.MethodOIDC, Enabled: true, OIDC: &config})
	if err != nil || !view.Ready {
		t.Fatalf("OIDC fixture not ready: %+v, %v", view, err)
	}
	authorization, err := manager.StartOIDC(t.Context(), "fixture", "127.0.0.1", "/admin/")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(authorization)
	values := parsed.Query()
	fixture.mu.Lock()
	fixture.nonce, fixture.challenge = values.Get("nonce"), values.Get("code_challenge")
	fixture.mu.Unlock()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	callbackDone := make(chan struct{})
	go func() {
		defer close(callbackDone)
		if _, _, err := manager.CompleteOIDC(ctx, values.Get("state"), "valid-code", "127.0.0.1"); err == nil {
			t.Error("cancelled OIDC callback issued a session")
		}
	}()
	waitForLifecycle(t, blocker.entered, "detached OIDC JWKS request")
	cancel()
	waitForLifecycle(t, callbackDone, "cancelled OIDC callback")
	closed := make(chan struct{})
	go func() { _ = manager.Close(); close(closed) }()
	waitForLifecycle(t, blocker.cancelled, "detached JWKS cancellation")
	select {
	case <-closed:
		t.Fatal("Close returned with a detached JWKS fetch still active")
	default:
	}
	blocker.unblock()
	waitForLifecycle(t, closed, "detached JWKS shutdown")
}
