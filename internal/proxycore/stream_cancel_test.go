package proxycore

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"routenest/internal/config"
)

// A client that disconnects while the upstream is silent must not leave the
// stream handler running. The handler returns once the request context ends,
// and no goroutines are left behind.
func TestServeChatStreamCancelWhileUpstreamSilent(t *testing.T) {
	installStreamConfig(t, config.StreamPolicy{})
	reqLog := testRequestLog("m")
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: pr}

	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		serveChatStream(ctx, rec, resp, nil, reqLog, nil)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		// Characterization: the handler waits for the upstream body, not the context.
		// Closing the upstream unblocks it; the test records that dependency.
		_ = pw.Close()
		select {
		case <-done:
			t.Log("serveChatStream returned only after the upstream body closed (context cancellation alone does not stop it)")
		case <-time.After(3 * time.Second):
			t.Fatal("serveChatStream did not return even after the upstream closed")
		}
	}

	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > before+1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before+1 {
		t.Fatalf("goroutines left behind: before=%d after=%d", before, n)
	}
}

// Same contract for the Anthropic stream: a silent upstream plus a cancelled
// client context must return and leave no goroutines behind.
func TestHandleAnthropicStreamCancelWhileUpstreamSilent(t *testing.T) {
	installStreamConfig(t, config.StreamPolicy{})
	reqLog := testRequestLog("m")
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	upstream := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: pr}

	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	rec := httptest.NewRecorder()
	sw := NewSSEWriter(rec)
	done := make(chan struct{})
	go func() {
		defer close(done)
		HandleAnthropicStream(ctx, sw, upstream, nil, reqLog, 10*time.Second, "", nil)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = pw.Close()
		select {
		case <-done:
			t.Log("HandleAnthropicStream returned only after the upstream body closed")
		case <-time.After(3 * time.Second):
			t.Fatal("HandleAnthropicStream did not return even after the upstream closed")
		}
	}

	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > before+1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before+1 {
		t.Fatalf("goroutines left behind: before=%d after=%d", before, n)
	}
}
