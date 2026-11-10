package proxycore

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"routenest/internal/routing"
	"routenest/internal/zen"
)

var protocolModelSyncOnce sync.Once

func protocolTestServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve protocol test listener: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())

	protocolModelSyncOnce.Do(func() {
		routing.SyncMu.Lock()
		routing.SyncRan = true
		routing.SyncMu.Unlock()
	})

	zen.ConfigMu.Lock()
	oldZenConfig := zen.Config
	zen.Config = &zen.ConfigData{BaseURL: zen.APIBase, Key: "public"}
	zen.ConfigMu.Unlock()

	ServerMu.Lock()
	oldServerMux, oldServer := ServerMux, CurrentServer
	oldHost, oldPort := ListenHost, ListenPort
	ServerMu.Unlock()
	ready, done := make(chan struct{}), make(chan struct{})
	var startErr error
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("timed out waiting for protocol test server shutdown")
		}
		ServerMu.Lock()
		ServerMux, CurrentServer = oldServerMux, oldServer
		ListenHost, ListenPort = oldHost, oldPort
		ServerMu.Unlock()
		zen.ConfigMu.Lock()
		zen.Config = oldZenConfig
		zen.ConfigMu.Unlock()
	})

	go func() {
		startErr = StartWithListener(ctx, listener, nil, func() { close(ready) })
		close(done)
	}()
	select {
	case <-ready:
	case <-done:
		t.Fatalf("start protocol test server: %v", startErr)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for protocol test server")
	}
	return "http://" + listener.Addr().String()
}
