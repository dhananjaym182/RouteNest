package combos

// 模型组合（Named Model Combos）+ 有序回退 —— Phase 1。
//
// 设计目标：管理员可配置若干具名组合（如 premium-coding / fast-free），每个组合
// 是一串**有序**的现有模型条目；客户端把组合 ID 当作普通模型名请求，路由器按配置
// 顺序逐个尝试，遇到可重试的上游失败就换下一个条目，直到成功或耗尽。
//
// 关键约束（实现时严格遵守）：
//   - 组合内的顺序是唯一权威。**不**按用量/延迟/质量/随机/账号轮询重排健康条目；
//     运行时可用性只会让某个条目被「跳过」，不会改变配置顺序。
//   - 每个条目的派发复用既有路径：zen 免费模型走 zen.Call；自定义 provider 走
//     providers.CallAPI（含冗余组、粘性会话）；其余走 Cline 账号池（含账号轮询、
//     模型级冷却、402/429 处理）。**不**对每个条目递归调用全局 ModelChain / free 链。
//   - 默认情况下组合只走自己的序列；只有管理员显式开启 emergencyFallback 才允许
//     落到全局 free 链（应急），且该行为在 UI 与报告中明确标注。
//   - 复用既有持久化（.routenest-config.json + 原子写 + 损坏备份），不引入新存储。
//
// 数据模型预留了 Phase 2 的扩展位（Category 等）；本阶段不实现任何自动质量评分，
// 也**不**从模型名推断质量。

import (
	"context"
	"fmt"
	"net/http"
	"routenest/internal/config"
	"routenest/internal/domain"
	"sync"
	"time"
)

// Root-owned live state seams, wired once from root composition. Defaults keep
// the package self-contained for unit tests.
var (
	ConfigMu     = &defaultConfigMu
	Config       = func() *config.Proxy { return defaultConfig }
	GetConfig    = func() *config.Proxy { return defaultConfig }
	EnsureConfig = func() *config.Proxy { return defaultConfig }
	SaveConfig   = func() {}

	IsAccountUnavailable    = func(error) bool { return false }
	NewUnavailableError     = func(message string) error { return &unavailableError{message: message} }
	CallClineAPIWithAccount = func(context.Context, *domain.Account, map[string]any, bool) (*http.Response, *domain.Account, error) {
		return nil, nil, fmt.Errorf("cline API not wired")
	}
	IsDeterministicRequestError = func(error) bool { return false }
	SetModelCooldown            = func(*domain.Account, string, time.Time) {}
	TruncateEmail               = func(email string) string { return email }
	CallFreeClineAPI            = func(map[string]any, bool) (*http.Response, *domain.Account, error) {
		return nil, nil, fmt.Errorf("free chain not wired")
	}
	WithModel = func(params map[string]any, model string) map[string]any {
		cp := make(map[string]any, len(params)+1)
		for k, v := range params {
			cp[k] = v
		}
		cp["model"] = model
		return cp
	}
)

var (
	defaultConfigMu sync.Mutex
	defaultConfig   = config.DefaultProxy()
)

type unavailableError struct{ message string }

func (e *unavailableError) Error() string { return e.message }
