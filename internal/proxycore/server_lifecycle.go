package proxycore

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

var (
	listenerLifecycleMu     sync.Mutex
	activeListenerLifecycle *listenerLifecycle
)

type listenerLifecycle struct {
	ctx     context.Context
	server  *http.Server
	serving *listenerServe
	result  chan error
}

type listenerServe struct {
	ready chan struct{}
	done  chan struct{}
	err   error
}

type readyListener struct {
	net.Listener
	ready chan struct{}
	once  sync.Once
}

func (listener *readyListener) Accept() (net.Conn, error) {
	listener.once.Do(func() { close(listener.ready) })
	return listener.Listener.Accept()
}

func (serving *listenerServe) waitReady(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-serving.done:
		if err := ctx.Err(); err != nil {
			return err
		}
		return serving.err
	case <-serving.ready:
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-serving.done:
		if err := ctx.Err(); err != nil {
			return err
		}
		return serving.err
	default:
		return nil
	}
}

func beginListenerLifecycle(ctx context.Context) (*listenerLifecycle, error) {
	listenerLifecycleMu.Lock()
	defer listenerLifecycleMu.Unlock()
	if activeListenerLifecycle != nil {
		return nil, fmt.Errorf("a proxy listener is already running")
	}
	lifecycle := &listenerLifecycle{ctx: ctx, result: make(chan error, 1)}
	activeListenerLifecycle = lifecycle
	return lifecycle, nil
}

func listenerContextLocked() context.Context {
	if activeListenerLifecycle != nil {
		return activeListenerLifecycle.ctx
	}
	return context.Background()
}

func newListenerServer(addr string, mux http.Handler, ctx context.Context) *http.Server {
	return &http.Server{
		Addr:        addr,
		Handler:     mux,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
}

func serveListenerLocked(server *http.Server, listener net.Listener) *listenerServe {
	serving := &listenerServe{ready: make(chan struct{}), done: make(chan struct{})}
	lifecycle := activeListenerLifecycle
	if lifecycle != nil {
		lifecycle.server = server
		lifecycle.serving = serving
	}
	go func() {
		serving.err = server.Serve(&readyListener{Listener: listener, ready: serving.ready})
		close(serving.done)
		listenerLifecycleMu.Lock()
		defer listenerLifecycleMu.Unlock()
		if lifecycle != nil && activeListenerLifecycle == lifecycle && lifecycle.server == server {
			lifecycle.fail(serving.err)
		} else if serving.err != nil && serving.err != http.ErrServerClosed {
			log.Printf("listener stopped unexpectedly: %v", serving.err)
		}
	}()
	return serving
}

func (lifecycle *listenerLifecycle) fail(err error) {
	select {
	case lifecycle.result <- err:
	default:
	}
}

func clearListenerLocked() {
	ServerMu.Lock()
	CurrentServer = nil
	ServerMu.Unlock()
	if lifecycle := activeListenerLifecycle; lifecycle != nil {
		lifecycle.server = nil
		lifecycle.serving = nil
	}
}

func (lifecycle *listenerLifecycle) stop() {
	listenerLifecycleMu.Lock()
	server, serving := lifecycle.server, lifecycle.serving
	listenerLifecycleMu.Unlock()
	if server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := server.Shutdown(ctx); err != nil {
			_ = server.Close()
		}
		cancel()
		<-serving.done
	}
	listenerLifecycleMu.Lock()
	defer listenerLifecycleMu.Unlock()
	if activeListenerLifecycle == lifecycle {
		ServerMu.Lock()
		if CurrentServer == server {
			CurrentServer = nil
			ServerMux = nil
		}
		ServerMu.Unlock()
		activeListenerLifecycle = nil
	}
}
