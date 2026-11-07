package adminsso

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"
)

func (m *Manager) operationErr(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.lifecycleCtx.Err()
}

func (m *Manager) lockState(ctx context.Context) error {
	select {
	case m.stateMu <- struct{}{}:
		if err := m.operationErr(ctx); err != nil {
			m.unlockState()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) unlockState() { <-m.stateMu }

// OIDC's remote key set intentionally detaches caller cancellation for shared
// key fetches. Track and cancel those requests at the manager's client boundary
// too, so Close waits for the actual HTTP work rather than only its caller.
type managerTransport struct {
	manager *Manager
	base    http.RoundTripper
}

func (t managerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	ctx, finish, err := t.manager.beginOperation(request.Context())
	if err != nil {
		return nil, err
	}
	defer finish()
	return t.base.RoundTrip(request.Clone(ctx))
}

func (t managerTransport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

// beginOperation joins caller cancellation to manager shutdown without a
// dedicated watcher goroutine. Add is serialized with Close before Wait.
func (m *Manager) beginOperation(ctx context.Context) (context.Context, func(), error) {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	if m.closed {
		return nil, nil, ErrManagerClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if err := m.lifecycleCtx.Err(); err != nil {
		return nil, nil, err
	}
	joined, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(m.lifecycleCtx, cancel)
	m.active.Add(1)
	return joined, func() {
		stop()
		cancel()
		m.active.Done()
	}, nil
}

// StartRefresh starts one serialized refresh loop, including an initial probe.
// Its lifetime is bounded by both ctx and Close; individual probes are bounded
// by timeout. Stopped loops are not restarted on a closed manager.
func (m *Manager) StartRefresh(ctx context.Context, interval, timeout time.Duration) error {
	if interval <= 0 || timeout <= 0 {
		return fmt.Errorf("SSO refresh interval and timeout must be positive")
	}
	ctx, finish, err := m.beginOperation(ctx)
	if err != nil {
		return err
	}
	m.lifecycleMu.Lock()
	if m.closed {
		m.lifecycleMu.Unlock()
		finish()
		return ErrManagerClosed
	}
	if m.refreshStarted {
		m.lifecycleMu.Unlock()
		finish()
		return fmt.Errorf("SSO refresh is already started")
	}
	m.refreshStarted = true
	m.lifecycleMu.Unlock()
	go func() {
		defer finish()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			probeCtx, cancel := context.WithTimeout(ctx, timeout)
			if err := m.Refresh(probeCtx); err != nil && ctx.Err() == nil {
				log.Printf("administrator SSO readiness refresh failed: %v", err)
			}
			cancel()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return nil
}

// Close cancels provider I/O and waits for refresh and request operations before
// releasing secret-directory handles and owned idle HTTP connections.
func (m *Manager) Close() error {
	m.closeOnce.Do(func() {
		m.lifecycleMu.Lock()
		m.closed = true
		m.cancel()
		m.lifecycleMu.Unlock()
		m.active.Wait()
		if m.closeClient {
			m.client.CloseIdleConnections()
		}
		m.closeErr = m.resolver.Close()
		m.mu.Lock()
		clear(m.views)
		clear(m.formActionOrigins)
		clear(m.samlMetadata)
		clear(m.knownRevisions)
		m.mu.Unlock()
	})
	return m.closeErr
}
