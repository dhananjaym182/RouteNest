package egress

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"
)

// stubClineProxyDial replaces the proxy dialer and clock. fail lists proxies that
// refuse to connect; attempts records every dial in order.
func stubClineProxyDial(t *testing.T, fail map[string]bool) (attempts *[]string, advance func(time.Duration)) {
	t.Helper()
	oldDial, oldNow := clineProxyDial, clineProxyNow
	clineProxyDownMu.Lock()
	oldDown := clineProxyDownUntil
	clineProxyDownUntil = map[string]time.Time{}
	clineProxyDownMu.Unlock()
	now := time.Unix(1_700_000_000, 0)
	clineProxyNow = func() time.Time { return now }
	var got []string
	clineProxyDial = func(ctx context.Context, proxy, network, addr string) (net.Conn, error) {
		got = append(got, proxy)
		if fail[proxy] {
			return nil, errors.New("proxy unreachable")
		}
		c, _ := net.Pipe()
		return c, nil
	}
	t.Cleanup(func() {
		clineProxyDial, clineProxyNow = oldDial, oldNow
		clineProxyDownMu.Lock()
		clineProxyDownUntil = oldDown
		clineProxyDownMu.Unlock()
	})
	return &got, func(d time.Duration) { now = now.Add(d) }
}

func dialOnce(t *testing.T) (net.Conn, error) {
	t.Helper()
	return clineDialContext(context.Background(), "tcp", "api.example.com:443")
}

func TestClineFillFailsOverToNextProxy(t *testing.T) {
	a, b, c := "socks5://a:1080", "socks5://b:1080", "socks5://c:1080"
	setClineProxyTestConfig(t, []string{a, b, c}, "fill")
	attempts, _ := stubClineProxyDial(t, map[string]bool{a: true})

	conn, err := dialOnce(t)
	if err != nil {
		t.Fatalf("dial should succeed through the second proxy: %v", err)
	}
	conn.Close()
	if want := []string{a, b}; !reflect.DeepEqual(*attempts, want) {
		t.Fatalf("attempts = %v, want %v", *attempts, want)
	}
}

func TestClineFailedProxyIsSkippedDuringCooldownThenRetried(t *testing.T) {
	a, b := "socks5://a:1080", "socks5://b:1080"
	setClineProxyTestConfig(t, []string{a, b}, "fill")
	attempts, advance := stubClineProxyDial(t, map[string]bool{a: true})

	if conn, err := dialOnce(t); err != nil {
		t.Fatal(err)
	} else {
		conn.Close()
	}
	*attempts = nil
	if conn, err := dialOnce(t); err != nil {
		t.Fatal(err)
	} else {
		conn.Close()
	}
	if want := []string{b}; !reflect.DeepEqual(*attempts, want) {
		t.Fatalf("during cooldown the failed proxy must not be tried first: %v", *attempts)
	}

	advance(clineProxyFailCooldown + time.Second)
	*attempts = nil
	if conn, err := dialOnce(t); err != nil {
		t.Fatal(err)
	} else {
		conn.Close()
	}
	if len(*attempts) == 0 || (*attempts)[0] != a {
		t.Fatalf("after the cooldown the preferred proxy is tried first again: %v", *attempts)
	}
}

func TestClineAllProxiesFailingStillTriesEveryOneEachTime(t *testing.T) {
	a, b := "socks5://a:1080", "socks5://b:1080"
	setClineProxyTestConfig(t, []string{a, b}, "fill")
	attempts, _ := stubClineProxyDial(t, map[string]bool{a: true, b: true})

	for i := 0; i < 2; i++ {
		*attempts = nil
		if _, err := dialOnce(t); err == nil {
			t.Fatal("expected an error when every proxy is down")
		}
		if len(*attempts) != 2 {
			t.Fatalf("round %d: cooldown must never exclude a proxy entirely, attempts = %v", i, *attempts)
		}
	}
}

func TestClineCancelledCallerIsNotAProxyFailure(t *testing.T) {
	a, b := "socks5://a:1080", "socks5://b:1080"
	setClineProxyTestConfig(t, []string{a, b}, "fill")
	attempts, _ := stubClineProxyDial(t, map[string]bool{a: true})

	ctx, cancel := context.WithCancel(context.Background())
	clineProxyDial = func(ctx context.Context, proxy, network, addr string) (net.Conn, error) {
		*attempts = append(*attempts, proxy)
		cancel()
		return nil, ctx.Err()
	}
	if _, err := clineDialContext(ctx, "tcp", "api.example.com:443"); err == nil {
		t.Fatal("expected the cancellation error")
	}
	if len(*attempts) != 1 {
		t.Fatalf("a cancelled caller must stop after one attempt, got %v", *attempts)
	}
	if got := orderClineProxies(a, []string{a, b}); got[0] != a {
		t.Fatalf("cancellation must not put the proxy in cooldown: %v", got)
	}
}

func TestClineSuccessClearsCooldown(t *testing.T) {
	a, b := "socks5://a:1080", "socks5://b:1080"
	proxies := []string{a, b}
	stubClineProxyDial(t, nil)
	markClineProxyDown(a)
	if got := orderClineProxies(a, proxies); got[0] != b {
		t.Fatalf("a failed proxy sorts last: %v", got)
	}
	markClineProxyUp(a)
	if got := orderClineProxies(a, proxies); got[0] != a {
		t.Fatalf("a recovered proxy sorts by strategy again: %v", got)
	}
}

func TestOrderClineProxiesRotatesAndDeduplicates(t *testing.T) {
	stubClineProxyDial(t, nil)
	got := orderClineProxies("b", []string{"a", "b", "c", "b"})
	if want := []string{"b", "c", "a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestClineProxyFailoverDoesNotApplyToDirectTargets(t *testing.T) {
	setClineProxyTestConfig(t, []string{"socks5://a:1080"}, "fill")
	attempts, _ := stubClineProxyDial(t, map[string]bool{"socks5://a:1080": true})
	if _, err := clineDialContext(context.Background(), "tcp", "127.0.0.1:1"); err == nil {
		t.Fatal("closed loopback port should fail")
	}
	if len(*attempts) != 0 {
		t.Fatalf("loopback must never use a proxy: %v", *attempts)
	}
}
