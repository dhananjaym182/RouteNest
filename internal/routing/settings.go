package routing

import (
	"os"
	"strconv"
	"strings"
)

const (
	// DefaultMaxTokens 是上游请求的默认/上限输出 token 数。
	//
	// 从 128000 降到 32000：日志实测 poolside/laguna-s-2.1:free 直接 400 ——
	// "maximum context length is 262144 tokens, however you requested about 263503
	// tokens (88799 text input, 46704 tool input, 128000 in the output)"。
	// 也就是说 128k 的输出预留本身就能把请求顶出上下文窗口，让本来可用的模型直接失败，
	// 并让其余模型陷入超长预填充/静默。可用 ROUTENEST_MAX_TOKENS（旧名 CLINE2API_MAX_TOKENS 仍兜底）调整。
	DefaultMaxTokens       = 32000
	DefaultReasoningEffort = "high"
	fallbackDefaultModel   = "z-ai/glm-5.3-flash"
	FreeModelPrimary       = "z-ai/glm-5.3-flash"
	FreeModelFallback      = "deepseek/deepseek-v4-flash"
	freeModelLastResort    = "cline-free/longcat-2.0"
)

// MinUpstreamMaxTokens 上游对输出 token 的硬下限：Cline 免费模型经 OpenRouter
// 转发时（如 meta/muse-spark），max_output_tokens < 16 会被上游直接 400。
const MinUpstreamMaxTokens = 16

// EnvWithLegacy resolves a tunable's environment variable from its
// ROUTENEST_-prefixed name, falling back to the pre-rename CLINE2API_-prefixed
// name so existing deployments keep working.
func EnvWithLegacy(name string) string {
	if v, ok := os.LookupEnv("ROUTENEST_" + name); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return os.Getenv("CLINE2API_" + name)
}

// envRoutingInt reads a positive integer override by new/legacy env-name pair.
func envRoutingInt(name string, def int) int {
	if v := strings.TrimSpace(EnvWithLegacy(name)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// envRoutingString reads a string override by new/legacy env-name pair.
func envRoutingString(name string) string {
	return strings.TrimSpace(EnvWithLegacy(name))
}

// MaxOutputTokens 是客户端请求中 max_tokens 的上限。
// 客户端显式要更大的输出（例如 128k）也会被夹到这里：预留过大的输出空间会把输入+输出
// 顶出上游上下文窗口（实测 400），收益却接近没有。
func MaxOutputTokens() int {
	return envRoutingInt("MAX_TOKENS", DefaultMaxTokens)
}

// ReasoningEffort 返回下发给上游的推理强度。默认保持 "high"，
// 可用 ROUTENEST_REASONING_EFFORT（旧名 CLINE2API_REASONING_EFFORT 仍兜底）
// =low|medium|high|none 覆盖：推理模型在大上下文下的思考阶段可能是长时间静默的
// 来源之一，这是用户侧最简单的延迟开关。
func ReasoningEffort() string {
	if v := envRoutingString("REASONING_EFFORT"); v != "" {
		return v
	}
	return DefaultReasoningEffort
}

// ClampMaxTokens 把请求的输出上限夹到 [1, MaxOutputTokens()]。
func ClampMaxTokens(n int) int {
	limit := MaxOutputTokens()
	if n <= 0 || n > limit {
		return limit
	}
	return n
}
