package egress

import (
	"context"
	"crypto/rand"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/http/httpproxy"

	"cline-go-proxy/internal/config"
	"cline-go-proxy/internal/platform"
	"cline-go-proxy/internal/transport"
)

// ClineProxyConfig is the persisted Cline egress proxy pool configuration.
type ClineProxyConfig = config.ClineEgress

// DataPathResolver resolves the Cline egress configuration path. The root
// composition replaces this before first use so legacy data-directory probing
// remains part of application startup, while leaf-package users get the
// platform default.
var DataPathResolver = func(name string) string {
	return platform.ResolveDataPath(name, nil)
}

var (
	clineProxyCfg   *ClineProxyConfig
	clineProxyCfgMu sync.Mutex
	clineProxyCount atomic.Uint64
)

func defaultClineProxyConfig() *ClineProxyConfig {
	cfg := config.DefaultClineEgress()
	return &cfg
}

// GetClineProxyConfig 惰性加载配置（避免依赖包初始化顺序）。
func GetClineProxyConfig() *ClineProxyConfig {
	clineProxyCfgMu.Lock()
	defer clineProxyCfgMu.Unlock()
	if clineProxyCfg == nil {
		path := DataPathResolver(config.ClineEgressPath)
		cfg, err := config.LoadClineEgress(path)
		if err != nil {
			platform.BackupCorruptFile(path, err)
		}
		clineProxyCfg = &cfg
	}
	return clineProxyCfg
}

// clineProxyPersistErr 最近一次持久化错误（落盘失败时后台返回 500，避免“保存成功”假象）。
var (
	clineProxyPersistErr   error
	clineProxyPersistErrMu sync.Mutex
)

func setClineProxyPersistErr(err error) {
	clineProxyPersistErrMu.Lock()
	defer clineProxyPersistErrMu.Unlock()
	clineProxyPersistErr = err
}

func GetClineProxyPersistErr() error {
	clineProxyPersistErrMu.Lock()
	defer clineProxyPersistErrMu.Unlock()
	return clineProxyPersistErr
}

// SetClineProxyConfig 原子替换配置并持久化。传输层按请求读取配置，无需重建。
func SetClineProxyConfig(c *ClineProxyConfig) {
	c.Normalize()
	clineProxyCfgMu.Lock()
	clineProxyCfg = c
	clineProxyCfgMu.Unlock()

	if err := config.SaveClineEgress(DataPathResolver(config.ClineEgressPath), c); err != nil {
		log.Printf("cline proxy config save failed: %v", err)
		setClineProxyPersistErr(err)
		return
	}
	setClineProxyPersistErr(nil)
}

func normalizeClineProxyConfig(c *ClineProxyConfig) {
	c.Normalize()
}

// snapshotClineProxyConfig 返回调用时刻的配置快照（深拷贝代理列表）。
// 传输层钩子按「一次请求一次快照」读取，避免同一请求内 active→dial 两次 get
// 之间并发更新导致的判定不一致。
func snapshotClineProxyConfig() (proxies []string, strategy string) {
	cfg := GetClineProxyConfig()
	clineProxyCfgMu.Lock()
	defer clineProxyCfgMu.Unlock()
	return cfg.Snapshot()
}

func clineProxiesActive() bool {
	cfg := GetClineProxyConfig()
	clineProxyCfgMu.Lock()
	defer clineProxyCfgMu.Unlock()
	return len(cfg.Proxies) > 0
}

// pickClineProxy 按策略选代理；未配置返回 ""。
// ponytail: 无单代理故障冷却（zen 侧有），代理挂了会体现在账号冷却与日志里，
// 升级路径：仿 cooldownZenProxy 增加索引冷却并在上游拨号失败处标记。
func pickClineProxy() string {
	proxies, strategy := snapshotClineProxyConfig()
	return pickFromClineProxies(proxies, strategy)
}

func pickFromClineProxies(proxies []string, strategy string) string {
	n := len(proxies)
	if n == 0 {
		return ""
	}
	idx := int(clineProxyCount.Add(1)-1) % n
	switch strategy {
	case "random":
		idx = randomIntn(n)
	case "fill":
		idx = 0
	}
	return proxies[idx]
}

func randomIntn(n int) int {
	if n <= 0 {
		return 0
	}
	b := make([]byte, 4)
	rand.Read(b)
	v := int(b[0])<<24 | int(b[1])<<16 | int(b[2])<<8 | int(b[3])
	if v < 0 {
		v = -v
	}
	return v % n
}

// clineEnvProxy 环境变量代理回退（可注入接缝，测试用替身）。
//
// 不用 http.ProxyFromEnvironment：它在进程内首次调用时就把环境变量缓存住，
// 此后任何修改（t.Setenv / 运行期改环境）都不再生效——只要先发生过一次出站
// 请求，代理配置就被永久冻结在那一刻（实测导致 TestHTTPTransportUsesHTTPSProxy-
// FromEnvironment 顺序相关地失败）。x/net/http/httpproxy 与标准库同一实现，
// 但每次实时读取环境，语义一致、无进程级缓存。
var clineEnvProxy = func(req *http.Request) (*url.URL, error) {
	return httpproxy.FromEnvironment().ProxyFunc()(req.URL)
}

// clineOutboundProxy 作为全局 transport 的 Proxy 钩子：应用内代理池生效时禁用
// 环境变量代理（隧道在 DialContext 内建立，叠加会双重代理），否则维持原行为。
func clineOutboundProxy(req *http.Request) (*url.URL, error) {
	if clineProxiesActive() {
		return nil, nil
	}
	return clineEnvProxy(req)
}

// clineDialContext 全局 transport 的拨号钩子。应用内代理生效时 addr 一定是目标
// 地址（Proxy 钩子已返回 nil），回环/内网目标直连（自定义 Provider 可能指向本机
// 模型服务），其余经代理池隧道；未生效时直连（可能是环境变量代理地址或目标本身）。
func clineDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if proxies, strategy := snapshotClineProxyConfig(); len(proxies) > 0 && !clineDirectDialHost(addr) {
		if p := pickFromClineProxies(proxies, strategy); p != "" {
			return transport.DialViaProxy(ctx, p, network, addr)
		}
	}
	d := &net.Dialer{Timeout: 12 * time.Second, KeepAlive: 30 * time.Second}
	return d.DialContext(ctx, network, addr)
}

// clineDirectDialHost 回环 / 内网 / 链路本地目标不走代理（本机 Ollama、LAN 网关等）。
// 仅识别 IP 字面量与 localhost；域名解析到内网地址的场景不处理。
func clineDirectDialHost(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}
