package proxycore

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"cline-go-proxy/internal/routing"
	"cline-go-proxy/internal/zen"
)

var protocolModelSyncOnce sync.Once

func protocolTestServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve protocol test port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	protocolModelSyncOnce.Do(func() {
		routing.SyncMu.Lock()
		routing.SyncRan = true
		routing.SyncMu.Unlock()
	})

	zen.ConfigMu.Lock()
	oldZenConfig := zen.Config
	zen.Config = &zen.ConfigData{BaseURL: zen.APIBase, Key: "public"}
	zen.ConfigMu.Unlock()

	oldServerMux := ServerMux
	ServerMu.Lock()
	oldServer := CurrentServer
	ServerMu.Unlock()
	t.Cleanup(func() {
		ServerMu.Lock()
		server := CurrentServer
		CurrentServer = oldServer
		ServerMu.Unlock()
		if server != nil && server != oldServer {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = server.Shutdown(ctx)
			cancel()
		}
		ServerMux = oldServerMux
		zen.ConfigMu.Lock()
		zen.Config = oldZenConfig
		zen.ConfigMu.Unlock()
	})

	serverErr := make(chan error, 1)
	go func() { serverErr <- Start("127.0.0.1", port) }()

	baseURL := "http://127.0.0.1:" + strconv.Itoa(port)
	client := &http.Client{Transport: &http.Transport{}, Timeout: 2 * time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, requestErr := client.Get(baseURL + "/health")
		if requestErr == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		select {
		case startErr := <-serverErr:
			t.Fatalf("start protocol test server: %v", startErr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for protocol test server")
		}
		time.Sleep(100 * time.Millisecond)
	}
	return baseURL
}
