package adminauth

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFlowOneTimeConcurrent(t *testing.T) {
	f := NewAuthFlowStore()
	handle, e := f.Create(AuthFlow{Provider: "idp", ProviderRevision: 1, ProviderFingerprint: "fingerprint", Type: MethodOIDC, Source: "127.0.0.1", Callback: "https://admin.example/callback", Nonce: "private-nonce", PKCEVerifier: "private-verifier"})
	if e != nil {
		t.Fatal(e)
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := f.Consume(handle, "idp", MethodOIDC); e == nil {
				successes.Add(1)
			} else if !errors.Is(e, ErrReplay) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatal("one-time consume count", successes.Load())
	}
}
func TestFlowLimitsExpiryBindingAndRotation(t *testing.T) {
	f := NewAuthFlowStore()
	now := time.Now()
	f.now = func() time.Time { return now }
	flow := AuthFlow{Provider: "idp", ProviderRevision: 1, ProviderFingerprint: "fingerprint", Type: MethodSAML, Source: "127.0.0.1", Callback: "https://admin.example/acs"}
	handle, e := f.Create(flow)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.Consume(handle, "other", MethodSAML); !errors.Is(e, ErrReplay) {
		t.Fatal(e)
	}
	if _, e = f.Consume(handle, "idp", MethodSAML); !errors.Is(e, ErrReplay) {
		t.Fatal("binding failure retained flow")
	}
	handle, e = f.Create(flow)
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(10 * time.Minute)
	if _, e = f.Consume(handle, "idp", MethodSAML); !errors.Is(e, ErrReplay) {
		t.Fatal("expiry accepted")
	}
	handle, _ = f.Create(flow)
	f.RevokeProvider("idp")
	if _, e = f.Consume(handle, "idp", MethodSAML); !errors.Is(e, ErrReplay) {
		t.Fatal(e)
	}
	for i := 0; i < 8; i++ {
		if _, e = f.Create(flow); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = f.Create(flow); !errors.Is(e, ErrRateLimited) {
		t.Fatal("source cap ignored", e)
	}
	f.Clear()
	flow.ExpiresAt = now.Add(11 * time.Minute)
	if _, e = f.Create(flow); e == nil {
		t.Fatal("overlong flow accepted")
	}
	flow.ExpiresAt = time.Time{}
	flow.ReturnLocation = "//evil.example"
	if _, e = f.Create(flow); e == nil {
		t.Fatal("external redirect accepted")
	}
}
func TestThrottleCapacityAndRecovery(t *testing.T) {
	th := NewThrottler()
	now := time.Now()
	th.now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		if ok, _ := th.Allow("source"); !ok {
			t.Fatal("early throttle")
		}
	}
	if ok, retry := th.Allow("source"); ok || retry <= 0 {
		t.Fatal("missing throttle")
	}
	now = now.Add(12 * time.Second)
	if ok, _ := th.Allow("source"); !ok {
		t.Fatal("bucket not refilled")
	}
	th.maxSources = 1
	if ok, _ := th.Allow("second"); ok {
		t.Fatal("bounded table can be reset")
	}
}
