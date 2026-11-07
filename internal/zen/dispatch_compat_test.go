package zen

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestAcquireDispatchSlotHonorsConfiguredConcurrency(t *testing.T) {
	ConfigMu.Lock()
	oldConfig := Config
	Config = &ConfigData{MaxConcurrency: 1}
	ConfigMu.Unlock()
	StateMu.Lock()
	oldSem := zenSem
	zenSem = nil
	StateMu.Unlock()
	t.Cleanup(func() {
		ConfigMu.Lock()
		Config = oldConfig
		ConfigMu.Unlock()
		StateMu.Lock()
		zenSem = oldSem
		StateMu.Unlock()
	})

	release, err := AcquireDispatchSlot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := AcquireDispatchSlot(ctx); err == nil {
		t.Fatal("second dispatch slot unexpectedly bypassed maxConcurrency=1")
	}
	release()
	releaseAgain, err := AcquireDispatchSlot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	releaseAgain()
}

func TestDispatchRetryDelayPreservesZenBoundsAndSessionRotation(t *testing.T) {
	ConfigMu.Lock()
	oldConfig := Config
	Config = &ConfigData{Retries: 1}
	ConfigMu.Unlock()
	t.Cleanup(func() {
		ConfigMu.Lock()
		Config = oldConfig
		ConfigMu.Unlock()
	})

	if delay, retry := DispatchRetryDelay(http.StatusServiceUnavailable, nil, []byte(`{"error":"busy"}`), nil, 0); !retry || delay <= 0 {
		t.Fatalf("first 503 retry = %v after %v", retry, delay)
	}
	if _, retry := DispatchRetryDelay(http.StatusServiceUnavailable, nil, []byte(`{"error":"busy"}`), nil, 1); retry {
		t.Fatal("retry limit was ignored")
	}

	ConfigMu.Lock()
	Config = &ConfigData{Retries: 3, SessionIDs: []string{"ses_one", "ses_two"}}
	ConfigMu.Unlock()
	if _, retry := DispatchRetryDelay(http.StatusTooManyRequests, make(http.Header), []byte(`{"error":"rate limit"}`), nil, 0); retry {
		t.Fatal("rate-limited session should advance to the next credential instead of replaying")
	}
}
