package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"

	"cline-go-proxy/internal/transport"
	zenupstream "cline-go-proxy/internal/upstream/zen"
)

// ============================================================================
// zen 出口代理池
// 配置 http/https/socks5(h) 代理后，所有发往 opencode.ai 的请求经代理池轮询出去；
// 命中限流时冷却当前出口（Retry-After 优先），冷却期内轮询自动跳过。
// HTTPS 统一走 uTLS Chrome_120 指纹 + HTTP/2，避免 Go 原生 TLS 指纹被 Cloudflare 风控。
// ============================================================================

var (
	zenHTTPClient  = &http.Client{Transport: buildZenTransport()}
	zenProxyCount  atomic.Uint64
	zenTransportMu sync.Mutex

	zenProxyCooldowns   = map[int]time.Time{} // 代理索引 → 冷却截止
	zenProxyCooldownsMu sync.Mutex
)

func getZenHTTPClient() *http.Client {
	zenTransportMu.Lock()
	defer zenTransportMu.Unlock()
	return zenHTTPClient
}

// rebuildZenTransport 代理或配置变化时重建 HTTP 客户端。
func rebuildZenTransport() {
	zenTransportMu.Lock()
	defer zenTransportMu.Unlock()
	zenHTTPClient = &http.Client{Transport: buildZenTransport()}
}

func buildZenTransport() *zenEgressTransport {
	return newZenEgressTransport()
}

// ============================================================================
// zenEgressTransport：uTLS Chrome 指纹下的 HTTP/2↔HTTP/1.1 自适应出口传输。
//
// 背景：opencode.ai 经部分网络路径（Cloudflare 配置/中间盒）ALPN 只会协商到
// http/1.1。老实现把 DialTLSContext 的返回值无条件交给 http2.Transport ——
// 无论协商结果如何都发 HTTP/2 帧给 http/1.1 对端，于是出现
// "http2: failed reading the frame payload: http2: frame too large,
// note that the frame header looked like an HTTP/1.1 header"。
// （Go 的 http.Transport 自定义 DialTLSContext 同样不会自动启用 h2，实测
// 会反向报 "malformed HTTP response"，所以两个协议必须显式分路。）
//
// 方案：对每个 host:port 做一次 uTLS ALPN 探测（Chrome 指纹，h2+http/1.1），
// 结果缓存，之后该 host 永久走对应子传输；子传输的拨号器是严格的 —— 协商
// 结果与子传输不匹配时返回类型化错误，选择层据此作废缓存并重试一次。
// ============================================================================

// zenALPNMismatchError 拨号时协商协议与期望不符：缓存作废、换路重试。
type zenALPNMismatchError struct {
	want  string // 期望协议（"h2" 或 "http/1.1"）
	got   string // 实际协商协议
	addrs string
}

func (e *zenALPNMismatchError) Error() string {
	return fmt.Sprintf("zen egress: ALPN mismatch for %s: want %s, got %q", e.addrs, e.want, e.got)
}

type zenEgressTransport struct {
	alpnMu sync.RWMutex
	alpn   map[string]string // host:port → "h2" | "http/1.1"

	h1t *http.Transport
	h2t *http2.Transport
}

func newZenEgressTransport() *zenEgressTransport {
	h1t := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}
	h1t.DialContext = zenDialContext
	h1t.DialTLSContext = zenDialTLSForHTTP1

	h2t := &http2.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return zenDialTLSForH2(ctx, network, addr)
		},
	}
	return &zenEgressTransport{alpn: map[string]string{}, h1t: h1t, h2t: h2t}
}

func (zt *zenEgressTransport) knownALPN(host string) (string, bool) {
	zt.alpnMu.RLock()
	defer zt.alpnMu.RUnlock()
	p, ok := zt.alpn[host]
	return p, ok
}

func (zt *zenEgressTransport) rememberALPN(host, proto string) {
	zt.alpnMu.Lock()
	zt.alpn[host] = proto
	zt.alpnMu.Unlock()
}

func (zt *zenEgressTransport) forgetALPN(host string) {
	zt.alpnMu.Lock()
	delete(zt.alpn, host)
	zt.alpnMu.Unlock()
}

func zenHostKey(u *url.URL) string {
	h := u.Host
	if !strings.Contains(h, ":") {
		h += ":443"
	}
	return h
}

func (zt *zenEgressTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" {
		return zt.h1t.RoundTrip(req)
	}
	host := zenHostKey(req.URL)
	for attempt := 0; attempt < 2; attempt++ {
		proto, ok := zt.knownALPN(host)
		if !ok {
			p, err := zenProbeALPN(req.Context(), host)
			if err != nil {
				return nil, err
			}
			proto = p
			zt.rememberALPN(host, proto)
		}
		var tr http.RoundTripper = zt.h1t
		if proto == "h2" {
			tr = zt.h2t
		}
		resp, err := tr.RoundTrip(req)
		if err != nil {
			var mm *zenALPNMismatchError
			if errors.As(err, &mm) {
				// 网络路径变了（代理切换/CDN 配置）：作废缓存，按新协商结果重试一次
				zt.forgetALPN(host)
				zt.rememberALPN(host, mm.gotProto())
				continue
			}
		}
		return resp, err
	}
	// 两次都撞上 ALPN 变动：最后再交给探测到的协议一次（错误原样返回）
	proto, _ := zt.knownALPN(host)
	if proto == "h2" {
		return zt.h2t.RoundTrip(req)
	}
	return zt.h1t.RoundTrip(req)
}

// gotProto 返回错误的另一侧协议（want h2 失败 → 协商到了 h1，反之亦然）。
func (e *zenALPNMismatchError) gotProto() string {
	if e.want == "h2" {
		return "http/1.1"
	}
	return "h2"
}

// zenProbeALPN 用 Chrome 指纹做一次 ALPN 探测，返回对端支持的协议标记。
func zenProbeALPN(ctx context.Context, host string) (string, error) {
	conn, err := dialZenTLSWithProtos(ctx, "tcp", host, []string{"h2", "http/1.1"})
	if err != nil {
		return "", err
	}
	np := conn.ConnectionState().NegotiatedProtocol
	conn.Close()
	if np == "h2" {
		return "h2", nil
	}
	return "http/1.1", nil
}

// dialZenTLSWithProtos 统一的 uTLS 握手入口（经 zen 代理/环境代理/直连）。
// 指纹固定为 opencode CLI（Bun/BoringSSL）的 ClientHello：免费层按 TLS
// 指纹放行，Go/Node/curl 的 hello 一律被伪装成 429 拒绝。该 hello 只提供
// http/1.1（ALPN 是 JA4 的组成部分），因此 nextProtos 参数仅为兼容保留。
func dialZenTLSWithProtos(ctx context.Context, network, addr string, nextProtos []string) (*utls.UConn, error) {
	raw, err := zenDialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		raw.Close()
		return nil, err
	}
	uconn := utls.UClient(raw, &utls.Config{
		ServerName: host,
		NextProtos: zenupstream.BunALPN,
	}, utls.HelloCustom)
	if err := uconn.ApplyPreset(zenupstream.BunClientHelloSpec()); err != nil {
		raw.Close()
		return nil, fmt.Errorf("zen egress: apply client hello: %w", err)
	}
	// 握手限时；握手成功后 cancel 不影响已建立的连接
	hsCtx, cancel := context.WithTimeout(ctx, zenTLSHandshakeTimeout)
	defer cancel()
	if err := uconn.HandshakeContext(hsCtx); err != nil {
		raw.Close()
		return nil, err
	}
	return uconn, nil
}

// zenDialTLSWithState 供两类子传输使用：握手后检查协商协议是否符合期望，
// 不符则返回 *zenALPNMismatchError（连接关闭），由选择层重新分流。
func zenDialTLSWithState(ctx context.Context, network, addr, want string) (net.Conn, error) {
	nextProtos := []string{"h2", "http/1.1"}
	uconn, err := dialZenTLSWithProtos(ctx, network, addr, nextProtos)
	if err != nil {
		return nil, err
	}
	got := uconn.ConnectionState().NegotiatedProtocol
	if got != want {
		uconn.Close()
		return nil, &zenALPNMismatchError{want: want, got: got, addrs: addr}
	}
	return uconn, nil
}

// zenDialTLSForH2 http2 子传输专用：必须协商到 h2，否则类型化失败让选择层换路。
func zenDialTLSForH2(ctx context.Context, network, addr string) (net.Conn, error) {
	return zenDialTLSWithState(ctx, network, addr, "h2")
}

// zenDialTLSForHTTP1 http/1.1 子传输专用：协商到 h2 时类型化失败（此时
// http.Transport 会把 h2 连接当 http/1.1 用，直接发 malformed 帧错误）。
func zenDialTLSForHTTP1(ctx context.Context, network, addr string) (net.Conn, error) {
	return zenDialTLSWithState(ctx, network, addr, "http/1.1")
}

// zenTLSHandshakeTimeout 限制 TLS 握手时长：GFW 式黑洞（TCP 通、TLS 静默丢弃）
// 会让无超时的握手永久挂起，请求既不失败也不返回（故障转移也无法激活）。
const zenTLSHandshakeTimeout = 15 * time.Second

func zenDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	p, _ := pickZenProxy()
	if p == "" {
		// 未配置 zen 代理时回退系统代理（HTTPS_PROXY / HTTP_PROXY），再直连
		if u, envErr := http.ProxyFromEnvironment(&http.Request{URL: &url.URL{Scheme: "https", Host: addr}}); envErr == nil && u != nil {
			return transport.DialViaProxy(ctx, u.String(), network, addr)
		}
		d := &net.Dialer{Timeout: 12 * time.Second, KeepAlive: 30 * time.Second}
		return d.DialContext(ctx, network, addr)
	}
	return transport.DialViaProxy(ctx, p, network, addr)
}

// pickZenProxy 按策略选代理，返回 (代理URL, 索引)；未配置返回 ("", -1)。
// 冷却中的代理线性探测跳过；轮询计数与日志索引保持一致。
func pickZenProxy() (string, int) {
	cfg := getZenConfig()
	n := len(cfg.Proxies)
	if n == 0 {
		return "", -1
	}
	idx := int(zenProxyCount.Add(1)-1) % n
	switch cfg.ProxyStrategy {
	case "random":
		idx = randIntn(n)
	case "fill":
		idx = 0
	}
	for i := 0; i < n; i++ {
		if zenProxyAvailable(idx) {
			break
		}
		idx = (idx + 1) % n
	}
	return cfg.Proxies[idx], idx
}

// lastZenProxyIdx 最近一次实际使用的代理索引（日志/冷却定位用）。
func lastZenProxyIdx() int {
	v := int64(zenProxyCount.Load())
	if v <= 0 {
		return -1
	}
	n := len(getZenConfig().Proxies)
	if n == 0 {
		n = 1
	}
	return int((v - 1) % int64(n))
}

func cooldownZenProxy(idx int, d time.Duration) {
	if idx < 0 {
		return
	}
	if d <= 0 {
		d = 10 * time.Minute
	}
	zenProxyCooldownsMu.Lock()
	zenProxyCooldowns[idx] = time.Now().Add(d)
	zenProxyCooldownsMu.Unlock()
	log.Printf("  zen proxy[%d] cooled down for %v", idx+1, d)
}

func zenProxyAvailable(idx int) bool {
	zenProxyCooldownsMu.Lock()
	defer zenProxyCooldownsMu.Unlock()
	until, ok := zenProxyCooldowns[idx]
	if !ok {
		return true
	}
	if time.Now().After(until) {
		delete(zenProxyCooldowns, idx)
		return true
	}
	return false
}

// zenProxyCooldownStatus 供管理后台展示：代理 URL → 冷却截止时刻。
func zenProxyCooldownStatus() map[string]string {
	cfg := getZenConfig()
	zenProxyCooldownsMu.Lock()
	defer zenProxyCooldownsMu.Unlock()
	out := map[string]string{}
	for idx, until := range zenProxyCooldowns {
		if idx >= 0 && idx < len(cfg.Proxies) && time.Now().Before(until) {
			out[cfg.Proxies[idx]] = until.Format("15:04:05")
		}
	}
	return out
}

func maskProxyURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = url.User("***")
	return u.String()
}
