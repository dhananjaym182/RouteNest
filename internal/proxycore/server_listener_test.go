package proxycore

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/providers"
	"routenest/internal/requestlog"
	"routenest/internal/routing"
	"routenest/internal/zen"
)

func prepareListenerTest(t *testing.T) {
	t.Helper()
	protocolModelSyncOnce.Do(func() {
		routing.SyncMu.Lock()
		routing.SyncRan = true
		routing.SyncMu.Unlock()
	})
	accounts.PoolMu.Lock()
	oldPool, oldPoolPath := accounts.Pool, accounts.PoolPath
	accounts.Pool = &AccountPool{}
	accounts.PoolPath = filepath.Join(t.TempDir(), "accounts.json")
	accounts.PoolMu.Unlock()
	zen.ConfigMu.Lock()
	oldZenConfig := zen.Config
	zen.Config = &zen.ConfigData{BaseURL: zen.APIBase, Key: "public"}
	zen.ConfigMu.Unlock()
	providers.Mu.Lock()
	oldProviders, oldLoaded := providers.CustomProviders, providers.Loaded
	providers.CustomProviders, providers.Loaded = nil, true
	providers.Mu.Unlock()
	requestlog.Mu.Lock()
	oldLogs, oldLogPath := requestlog.Logs, requestlog.Path
	requestlog.Logs = nil
	requestlog.Path = filepath.Join(t.TempDir(), "requests.json")
	requestlog.Mu.Unlock()
	ServerMu.Lock()
	oldMux, oldServer := ServerMux, CurrentServer
	oldHost, oldPort := ListenHost, ListenPort
	ServerMu.Unlock()
	t.Cleanup(func() {
		ServerMu.Lock()
		ServerMux, CurrentServer = oldMux, oldServer
		ListenHost, ListenPort = oldHost, oldPort
		ServerMu.Unlock()
		requestlog.Mu.Lock()
		requestlog.Logs, requestlog.Path = oldLogs, oldLogPath
		requestlog.Mu.Unlock()
		providers.Mu.Lock()
		providers.CustomProviders, providers.Loaded = oldProviders, oldLoaded
		providers.Mu.Unlock()
		zen.ConfigMu.Lock()
		zen.Config = oldZenConfig
		zen.ConfigMu.Unlock()
		accounts.PoolMu.Lock()
		accounts.Pool, accounts.PoolPath = oldPool, oldPoolPath
		accounts.PoolMu.Unlock()
	})
}

type observedListener struct {
	net.Listener
	accepts atomic.Int32
	closes  atomic.Int32
	closed  chan struct{}
	once    sync.Once
}

func newObservedListener(t *testing.T) *observedListener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return &observedListener{Listener: listener, closed: make(chan struct{})}
}

func (listener *observedListener) Accept() (net.Conn, error) {
	listener.accepts.Add(1)
	return listener.Listener.Accept()
}

func (listener *observedListener) Close() error {
	listener.closes.Add(1)
	err := listener.Listener.Close()
	listener.once.Do(func() { close(listener.closed) })
	return err
}

type runningListenerTest struct {
	cancel context.CancelFunc
	done   chan struct{}
	ready  chan struct{}
	err    error
	calls  atomic.Int32
}

func runListenerTest(t *testing.T, listener net.Listener, register func(*http.ServeMux)) *runningListenerTest {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	run := &runningListenerTest{cancel: cancel, done: make(chan struct{}), ready: make(chan struct{})}
	t.Cleanup(func() {
		cancel()
		select {
		case <-run.done:
		case <-time.After(5 * time.Second):
			t.Error("listener did not stop")
		}
	})
	go func() {
		run.err = StartWithListener(ctx, listener, register, func() {
			run.calls.Add(1)
			close(run.ready)
		})
		close(run.done)
	}()
	return run
}

func (run *runningListenerTest) waitReady(t *testing.T) {
	t.Helper()
	select {
	case <-run.done:
		t.Fatalf("listener stopped before readiness: %v", run.err)
	case <-run.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("listener did not become ready")
	}
	select {
	case <-run.done:
		t.Fatalf("listener stopped at readiness: %v", run.err)
	default:
	}
}

func (run *runningListenerTest) wait(t *testing.T) error {
	t.Helper()
	select {
	case <-run.done:
		return run.err
	case <-time.After(5 * time.Second):
		t.Fatal("listener did not return")
		return nil
	}
}

func assertListenerAvailable(t *testing.T, address string) {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("listener was not released: %v", err)
	}
	_ = listener.Close()
}

func listenerTestResponse(t *testing.T, path string) (int, string) {
	t.Helper()
	ServerMu.Lock()
	server := CurrentServer
	ServerMu.Unlock()
	if server == nil {
		t.Fatal("listener server is unavailable")
	}
	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder.Code, recorder.Body.String()
}

func TestStartWithListenerOwnsAlreadyBoundPort(t *testing.T) {
	prepareListenerTest(t)
	listener := newObservedListener(t)
	address := listener.Addr().String()
	competing, err := net.Listen("tcp", address)
	if err == nil {
		_ = competing.Close()
		t.Fatal("second listener acquired the owned port")
	}
	marker := filepath.Join(t.TempDir(), "takeover")
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "powershell"), []byte("#!/bin/sh\nprintf invoked > \"$ROUTENEST_TEST_TAKEOVER\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("ROUTENEST_TEST_TAKEOVER", marker)
	run := runListenerTest(t, listener, func(mux *http.ServeMux) {
		mux.HandleFunc("/owned", func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "owned listener")
		})
	})
	run.waitReady(t)
	ServerMu.Lock()
	host, port, server := ListenHost, ListenPort, CurrentServer
	ServerMu.Unlock()
	if server == nil || listenerAddress(host, port) != address {
		t.Fatalf("published listener = %s, server = %v", listenerAddress(host, port), server)
	}
	select {
	case <-run.done:
		t.Fatalf("listener ended after readiness: %v", run.err)
	default:
	}
	status, body := listenerTestResponse(t, "/owned")
	if status != http.StatusOK || body != "owned listener" {
		t.Fatalf("owned response = %d %q", status, body)
	}
	if listener.accepts.Load() == 0 || listener.closes.Load() != 0 {
		t.Fatalf("accepts = %d, closes before cancellation = %d", listener.accepts.Load(), listener.closes.Load())
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("port takeover command was invoked: %v", err)
	}
	run.cancel()
	if err := run.wait(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
	if run.calls.Load() != 1 || listener.closes.Load() == 0 {
		t.Fatalf("ready calls = %d, closes = %d", run.calls.Load(), listener.closes.Load())
	}
	assertListenerAvailable(t, address)
}

func TestStartWithContextRejectsOccupiedPortWithoutTakeover(t *testing.T) {
	listener := newObservedListener(t)
	port := listener.Addr().(*net.TCPAddr).Port
	marker := filepath.Join(t.TempDir(), "takeover")
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "powershell"), []byte("#!/bin/sh\nprintf invoked > \"$ROUTENEST_TEST_TAKEOVER\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("ROUTENEST_TEST_TAKEOVER", marker)
	registered := false
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := StartWithContext(ctx, "127.0.0.1", port, func(*http.ServeMux) { registered = true })
	var bindErr *net.OpError
	if !errors.As(err, &bindErr) || bindErr.Op != "listen" {
		t.Fatalf("occupied port error = %v", err)
	}
	if registered || listener.closes.Load() != 0 || listener.accepts.Load() != 0 {
		t.Fatalf("occupied listener was touched: registered=%v closes=%d accepts=%d", registered, listener.closes.Load(), listener.accepts.Load())
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("port takeover command was invoked: %v", err)
	}
	if competing, err := net.Listen("tcp", listener.Addr().String()); err == nil {
		_ = competing.Close()
		t.Fatal("occupied listener was released")
	}
}

type invalidAddressListener struct {
	*observedListener
}

func (listener invalidAddressListener) Addr() net.Addr {
	return &net.UnixAddr{Name: "invalid", Net: "unix"}
}

func TestStartWithListenerEarlyErrorsCloseListener(t *testing.T) {
	for _, name := range []string{"nil context", "canceled", "deadline", "invalid address"} {
		t.Run(name, func(t *testing.T) {
			listener := newObservedListener(t)
			address := listener.Addr().String()
			var owned net.Listener = listener
			ctx := context.Background()
			var expected error
			switch name {
			case "nil context":
				ctx = nil
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				expected = context.Canceled
			case "deadline":
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
				expected = context.DeadlineExceeded
			case "invalid address":
				owned = invalidAddressListener{listener}
			}
			registered, ready := false, false
			err := StartWithListener(ctx, owned, func(*http.ServeMux) { registered = true }, func() { ready = true })
			if err == nil || expected != nil && !errors.Is(err, expected) {
				t.Fatalf("early startup error = %v, expected %v", err, expected)
			}
			if registered || ready || listener.closes.Load() == 0 {
				t.Fatalf("early error: registered=%v ready=%v closes=%d", registered, ready, listener.closes.Load())
			}
			assertListenerAvailable(t, address)
		})
	}
	if err := StartWithListener(context.Background(), nil, nil, nil); err == nil {
		t.Fatal("nil listener was accepted")
	}
}

func TestStartWithListenerCancellationDuringInitialization(t *testing.T) {
	prepareListenerTest(t)
	listener := newObservedListener(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	run := runListenerTest(t, listener, func(*http.ServeMux) {
		close(entered)
		<-release
	})
	t.Cleanup(unblock)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("route initialization did not run")
	}
	select {
	case <-run.ready:
		t.Fatal("reported ready before initialization completed")
	default:
	}
	run.cancel()
	select {
	case <-listener.closed:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not close listener during initialization")
	}
	unblock()
	if err := run.wait(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("startup cancellation = %v", err)
	}
	if listener.accepts.Load() != 0 || run.calls.Load() != 0 {
		t.Fatalf("canceled initialization served requests: accepts=%d ready=%d", listener.accepts.Load(), run.calls.Load())
	}
	assertListenerAvailable(t, listener.Addr().String())
}

func TestStartWithListenerRejectsSecondStartup(t *testing.T) {
	prepareListenerTest(t)
	first := newObservedListener(t)
	run := runListenerTest(t, first, nil)
	run.waitReady(t)
	second := newObservedListener(t)
	registered, ready := false, false
	err := StartWithListener(context.Background(), second, func(*http.ServeMux) { registered = true }, func() { ready = true })
	if err == nil || registered || ready || second.closes.Load() == 0 {
		t.Fatalf("second startup: err=%v registered=%v ready=%v closes=%d", err, registered, ready, second.closes.Load())
	}
	if first.closes.Load() != 0 {
		t.Fatal("second startup closed the active listener")
	}
	assertListenerAvailable(t, second.Addr().String())
}

type failingOwnedListener struct {
	*observedListener
	fail <-chan struct{}
	err  error
}

func (listener failingOwnedListener) Accept() (net.Conn, error) {
	listener.accepts.Add(1)
	select {
	case <-listener.fail:
		return nil, listener.err
	case <-listener.closed:
		return nil, net.ErrClosed
	}
}

func TestStartWithListenerReturnsServeFailure(t *testing.T) {
	for _, immediate := range []bool{true, false} {
		name := "after readiness"
		if immediate {
			name = "initial accept"
		}
		t.Run(name, func(t *testing.T) {
			prepareListenerTest(t)
			listener := newObservedListener(t)
			fail := make(chan struct{})
			failure := errors.New("owned listener accept failed")
			if immediate {
				close(fail)
			}
			run := runListenerTest(t, failingOwnedListener{observedListener: listener, fail: fail, err: failure}, nil)
			if !immediate {
				run.waitReady(t)
				close(fail)
			}
			if err := run.wait(t); !errors.Is(err, failure) {
				t.Fatalf("serve failure = %v", err)
			}
			if listener.closes.Load() == 0 {
				t.Fatal("serve failure did not close listener")
			}
			assertListenerAvailable(t, listener.Addr().String())
		})
	}
}

func TestStartWithListenerClosedListenerIsNotRebound(t *testing.T) {
	prepareListenerTest(t)
	listener := newObservedListener(t)
	address := listener.Addr().String()
	_ = listener.Close()
	run := runListenerTest(t, listener, nil)
	if err := run.wait(t); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed owned listener startup = %v", err)
	}
	assertListenerAvailable(t, address)
}

func TestStartWithListenerSurvivesRestartAndCancelsReplacement(t *testing.T) {
	prepareListenerTest(t)
	listener := newObservedListener(t)
	run := runListenerTest(t, listener, func(mux *http.ServeMux) {
		mux.HandleFunc("/retained", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) })
	})
	run.waitReady(t)
	ServerMu.Lock()
	firstServer, firstMux := CurrentServer, ServerMux
	ServerMu.Unlock()
	if err := RestartListener("127.0.0.1", listener.Addr().(*net.TCPAddr).Port); err != nil {
		t.Fatal(err)
	}
	if listener.closes.Load() != 0 {
		t.Fatal("same-address restart closed listener")
	}
	if err := RestartListener("0.0.0.0", 0); err == nil {
		t.Fatal("non-loopback restart was accepted")
	}
	if err := RestartListener("127.0.0.1", 0); err != nil {
		t.Fatalf("listener restart failed: %v", err)
	}
	ServerMu.Lock()
	replacement, replacementMux := CurrentServer, ServerMux
	replacementAddress := listenerAddress(ListenHost, ListenPort)
	ServerMu.Unlock()
	if replacement == firstServer || replacementMux != firstMux || listener.closes.Load() == 0 {
		t.Fatalf("restart lost ownership or routes: replacement=%v mux retained=%v closes=%d", replacement != firstServer, replacementMux == firstMux, listener.closes.Load())
	}
	select {
	case <-run.done:
		t.Fatalf("restart ended caller lifecycle: %v", run.err)
	default:
	}
	if status, _ := listenerTestResponse(t, "/retained"); status != http.StatusAccepted {
		t.Fatalf("registered route was lost on restart: %d", status)
	}
	if competing, err := net.Listen("tcp", replacementAddress); err == nil {
		_ = competing.Close()
		t.Fatal("replacement listener was not bound")
	}
	run.cancel()
	if err := run.wait(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("replacement cancellation = %v", err)
	}
	if run.calls.Load() != 1 {
		t.Fatalf("startup readiness repeated after restart: %d", run.calls.Load())
	}
	assertListenerAvailable(t, replacementAddress)
}

func TestStartWithListenerReturnsReplacementFailure(t *testing.T) {
	prepareListenerTest(t)
	listener := newObservedListener(t)
	run := runListenerTest(t, listener, nil)
	run.waitReady(t)
	if err := RestartListener("127.0.0.1", 0); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("replacement accept failed")
	next := newObservedListener(t)
	fail := make(chan struct{})
	listenerLifecycleMu.Lock()
	ServerMu.Lock()
	old, mux := CurrentServer, ServerMux
	ServerMu.Unlock()
	if err := old.Close(); err != nil {
		listenerLifecycleMu.Unlock()
		t.Fatal(err)
	}
	server := newListenerServer(next.Addr().String(), mux, listenerContextLocked())
	ServerMu.Lock()
	CurrentServer = server
	ListenHost, ListenPort = "127.0.0.1", next.Addr().(*net.TCPAddr).Port
	ServerMu.Unlock()
	serving := serveListenerLocked(server, failingOwnedListener{observedListener: next, fail: fail, err: failure})
	listenerLifecycleMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := serving.waitReady(ctx); err != nil {
		t.Fatal(err)
	}
	close(fail)
	if err := run.wait(t); !errors.Is(err, failure) {
		t.Fatalf("replacement serve failure = %v", err)
	}
	assertListenerAvailable(t, next.Addr().String())
}

func TestRestartListenerOccupiedPortRollsBackWithoutTakeover(t *testing.T) {
	prepareListenerTest(t)
	listener := newObservedListener(t)
	occupied := newObservedListener(t)
	run := runListenerTest(t, listener, nil)
	run.waitReady(t)
	ServerMu.Lock()
	original := CurrentServer
	ServerMu.Unlock()
	err := RestartListener("127.0.0.1", occupied.Addr().(*net.TCPAddr).Port)
	var bindErr *net.OpError
	if !errors.As(err, &bindErr) || bindErr.Op != "listen" {
		t.Fatalf("occupied restart error = %v", err)
	}
	ServerMu.Lock()
	restored, address := CurrentServer, listenerAddress(ListenHost, ListenPort)
	ServerMu.Unlock()
	if restored == nil || restored == original || address != listener.Addr().String() {
		t.Fatalf("previous listener not restored: changed=%v address=%s", restored != original, address)
	}
	select {
	case <-run.done:
		t.Fatalf("rollback ended caller lifecycle: %v", run.err)
	default:
	}
	if occupied.closes.Load() != 0 || occupied.accepts.Load() != 0 {
		t.Fatal("restart touched somebody else's listener")
	}
	run.cancel()
	if err := run.wait(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("rollback listener cancellation = %v", err)
	}
	assertListenerAvailable(t, address)
}

type reclaimingListener struct {
	*observedListener
	onClosed func()
	reclaim  sync.Once
}

func (listener *reclaimingListener) Close() error {
	err := listener.observedListener.Close()
	listener.reclaim.Do(listener.onClosed)
	return err
}

func TestRestartListenerFailedRollbackReturnsToOwner(t *testing.T) {
	prepareListenerTest(t)
	listener := newObservedListener(t)
	occupied := newObservedListener(t)
	var reclaimed net.Listener
	var reclaimErr error
	owned := &reclaimingListener{observedListener: listener, onClosed: func() {
		reclaimed, reclaimErr = net.Listen("tcp", listener.Addr().String())
	}}
	run := runListenerTest(t, owned, nil)
	run.waitReady(t)
	err := RestartListener("127.0.0.1", occupied.Addr().(*net.TCPAddr).Port)
	if reclaimErr != nil {
		t.Fatal(reclaimErr)
	}
	if reclaimed != nil {
		defer reclaimed.Close()
	}
	if err == nil {
		t.Fatal("restart with both ports occupied succeeded")
	}
	if result := run.wait(t); result == nil || result.Error() != err.Error() {
		t.Fatalf("owner error = %v, restart error = %v", result, err)
	}
	ServerMu.Lock()
	server := CurrentServer
	ServerMu.Unlock()
	if server != nil {
		t.Fatal("unavailable listener remains published")
	}
}

type pipeOwnedListener struct {
	*observedListener
	connections chan net.Conn
}

func (listener pipeOwnedListener) Accept() (net.Conn, error) {
	listener.accepts.Add(1)
	select {
	case conn := <-listener.connections:
		return conn, nil
	case <-listener.closed:
		return nil, net.ErrClosed
	}
}

func TestStartWithListenerCancelsActiveRequest(t *testing.T) {
	prepareListenerTest(t)
	listener := pipeOwnedListener{observedListener: newObservedListener(t), connections: make(chan net.Conn)}
	entered, canceled := make(chan struct{}), make(chan struct{})
	run := runListenerTest(t, listener, func(mux *http.ServeMux) {
		mux.HandleFunc("/wait", func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			<-r.Context().Done()
			close(canceled)
		})
	})
	run.waitReady(t)
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	select {
	case listener.connections <- server:
	case <-time.After(time.Second):
		t.Fatal("server did not accept the owned connection")
	}
	if err := client.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(client, "GET /wait HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("registered handler was not served")
	}
	run.cancel()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("request did not inherit the listener's cancellation")
	}
	_ = client.Close()
	if err := run.wait(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("active-request cancellation = %v", err)
	}
}

func TestStartWithListenerExternalShutdownReturnsServerClosed(t *testing.T) {
	prepareListenerTest(t)
	listener := newObservedListener(t)
	run := runListenerTest(t, listener, nil)
	run.waitReady(t)
	ServerMu.Lock()
	server := CurrentServer
	ServerMu.Unlock()
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := run.wait(t); !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("external shutdown = %v", err)
	}
}
