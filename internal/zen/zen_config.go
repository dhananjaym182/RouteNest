package zen

import (
	"log"
	"sync"
	"time"

	"routenest/internal/config"
	"routenest/internal/platform"
	"routenest/internal/transport"
	"routenest/internal/upstream/openaicompat"
)

var (
	DataPathResolver    = func(name string) string { return platform.ResolveDataPath(name, nil) }
	BodyOptionsProvider = func() openaicompat.BodyOptions {
		return openaicompat.BodyOptions{MinMaxTokens: 16, DefaultMaxTokens: 32000}
	}
)

// ============================================================================
// opencode Zen 免费模型支持
// 按请求中的 model 自动分流：zen 免费模型 → https://opencode.ai/zen/v1；
// zen 付费模型 → 400 拒绝；其余 → Cline 账号池。
// zen 模型与 Cline 远程模型同存于 accounts.Pool.Models（Source="zen"），
// 同步采用全量替换：官方下架的模型自动移除，不会残留僵尸条目。
// ============================================================================

// APIBase 自 2026-10 中旬起免费模型只在 inference API 应答：
// /zen/v1 上的同款请求直接 403 FreeTierError（magpie zen-free 插件 0.1.6 实测）。
const APIBase = "https://opencode.ai/inference/v1"

// 请求日志中的上游标记
const (
	UpstreamCline    = "cline"
	UpstreamOpenCode = "opencode"
	UpstreamProvider = "provider"
)

// ============ zen 配置 ============

type CompactConfig = config.ZenCompact

type ConfigData = config.Zen

func DefaultConfig() *ConfigData {
	return config.DefaultZen(APIBase)
}

var (
	Config   *ConfigData
	ConfigMu sync.Mutex
)

// GetConfig 惰性加载配置（避免依赖包初始化顺序）。
func GetConfig() *ConfigData {
	ConfigMu.Lock()
	defer ConfigMu.Unlock()
	if Config == nil {
		path := DataPathResolver(config.ZenPath)
		cfg, err := config.LoadZen(path, APIBase)
		if err != nil {
			platform.BackupCorruptFile(path, err)
		}
		Config = cfg
	}
	return Config
}

// SetConfig 原子替换配置并持久化，重建信号量与 HTTP 传输层。
func SetConfig(c *ConfigData) {
	c.NormalizeForSave(APIBase)
	ConfigMu.Lock()
	Config = c
	ConfigMu.Unlock()

	if err := config.SaveZen(DataPathResolver(config.ZenPath), c); err != nil {
		log.Printf("zen config save failed: %v", err)
	}
	// 关闭故障转移时立即解除已武装的窗口：否则 UI 仍显示“故障转移中”，
	// 反向降级 clineFailoverToZen 也会被旧窗口挡住直到超时。
	if !c.Failover {
		StateMu.Lock()
		FailCount = 0
		FailUntil = time.Time{}
		StateMu.Unlock()
	}
	rebuildZenTransport()
	rebuildZenSem()
}

// ValidateProxyList 校验代理列表格式：支持 http/https/socks5/socks5h，必须含 host:port。
func ValidateProxyList(proxies []string) error {
	return transport.ValidateProxyList(proxies)
}
