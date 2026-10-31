package proxycore

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"routenest/internal/routing"
	"routenest/internal/zen"
	"strconv"
	"strings"
	"time"

	"routenest/internal/config"
	"routenest/internal/domain"
	"routenest/internal/httpx"
	"routenest/internal/protocol"
	anthropicconv "routenest/internal/protocol/anthropic"
	"routenest/internal/protocol/openaichat"
	"routenest/internal/providers"
)

// Root-owned live state seams, wired once from root composition.
var (
	ConfigProvider = func() *config.Proxy { return config.DefaultProxy() }
	RecordUsage    = func(*domain.Account, *http.Response, *domain.RequestLog, string, TokenUsage) {}
)

const (
	// DefaultMaxTokens 是上游请求的默认/上限输出 token 数。
	//
	// 从 128000 降到 32000：日志实测 poolside/laguna-s-2.1:free 直接 400 ——
	// "maximum context length is 262144 tokens, however you requested about 263503
	// tokens (88799 text input, 46704 tool input, 128000 in the output)"。
	// 也就是说 128k 的输出预留本身就能把请求顶出上下文窗口，让本来可用的模型直接失败，
	// 并让其余模型陷入超长预填充/静默。可用 ROUTENEST_MAX_TOKENS（旧名 CLINE2API_MAX_TOKENS 仍兜底）调整。

	// maxUpstreamToolNameLength is the upstream tool-name limit.
	maxUpstreamToolNameLength = openaichat.MaxToolNameLength
)

// MinUpstreamMaxTokens 上游对输出 token 的硬下限：Cline 免费模型经 OpenRouter
// 转发时（如 meta/muse-spark），max_output_tokens < 16 会被上游直接 400。

// defaultFirstContentTimeout 是单次上游尝试等待「首个内容事件」的上限。
// 超时即认定该模型此刻不可用：放弃本次尝试、冷却该模型、立刻换到链上下一个模型。
//
// 依据 proxy.log 2026/09/22 12:34~13:04 的实测：同一个 glm-5.3-flash 账号在 93 tools
// 的负载下，有时 1~3 秒就出内容，有时建流后静默 5~10 分钟（超过客户端耐心后
// 只能看到「Combobulating…」然后 timeout），而同一时刻链上的 deepseek-v4.1-flash
// 1.5 秒就返回。继续等待没有收益 —— 快速换模型才有。
//
// 20s 而不是 90s：90s 的实测后果是 13:31:40 那次请求在 GLM 上白等 90s、
// 换到 laguna 立刻 400、再换 solar-pro4 又静默，客户端只看到「Fermenting… (2m51s)」。
// 好几个候选都被跳过时，旧行为（客户端自己超时后重连）体验明显好于我们长时间静默等待，
// 所以这里取「一次不行就赶紧换」的短耐心。可用 ROUTENEST_FIRST_CONTENT_TIMEOUT_MS
// （旧名 CLINE2API_FIRST_CONTENT_TIMEOUT_MS 仍兜底）覆盖。
const defaultFirstContentTimeout = 20 * time.Second

// lastAttemptFirstContentTimeout 是最后一次尝试的等待上限，比普通尝试宽容：
// 候选模型都被跳过时，宁可多等一下也要给出答案，而不是把错误抛给客户端。
// 但仍然收敛在客户端忍耐范围内，不留几分钟的静默「假活着」。
// 可用 ROUTENEST_LAST_ATTEMPT_TIMEOUT_MS（旧名 CLINE2API_LAST_ATTEMPT_TIMEOUT_MS 仍兜底）覆盖。
const lastAttemptFirstContentTimeout = 45 * time.Second

// routing.EnvWithLegacy resolves a tunable's environment variable from its
// ROUTENEST_-prefixed name, falling back to the pre-rename CLINE2API_-prefixed
// name so existing deployments keep working.

// envRoutingInt reads a positive integer override by new/legacy env-name pair.

// envRoutingDurationFromMS reads a positive millisecond-duration override by
// new/legacy env-name pair.
func envRoutingDurationFromMS(name string, def time.Duration) time.Duration {
	if v := strings.TrimSpace(routing.EnvWithLegacy(name)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Millisecond
		}
	}
	return def
}

// envRoutingString reads a string override by new/legacy env-name pair.

// MaxOutputTokens 是客户端请求中 max_tokens 的上限。
// 客户端显式要更大的输出（例如 128k）也会被夹到这里：预留过大的输出空间会把输入+输出
// 顶出上游上下文窗口（实测 400），收益却接近没有。

// routing.ReasoningEffort 返回下发给上游的推理强度。默认保持 "high"，
// 可用 ROUTENEST_REASONING_EFFORT（旧名 CLINE2API_REASONING_EFFORT 仍兜底）
// =low|medium|high|none 覆盖：推理模型在大上下文下的思考阶段可能是长时间静默的
// 来源之一，这是用户侧最简单的延迟开关。

// routing.ClampMaxTokens 把请求的输出上限夹到 [1, MaxOutputTokens()]。

// FreeModelChain 是 model="free" 时的降级顺序。
// 顺序依据 Artificial Analysis Intelligence Index v4.1.1：
// glm-5.3-flash 57 > deepseek-v4-flash 0731 52 > longcat-2.0 34。

// builtinModels 是内置默认模型列表（不可删除），仅作为离线 / 未同步时的 fallback。
// 同步 Cline 官方推荐模型成功后，AllModels 以远程模型为主。

// AllModels 返回可用模型列表：
//   - 已同步远程模型：Cline 远程（Source=remote）+ opencode 同步（Source=zen）+ 用户自定义
//   - 未同步 / 离线：内置 fallback（Cline + zen 种子表）+ 用户自定义

// routing.BuildModelsList 组装客户端发现用的模型列表（/v1/models、/models）：
// 池内模型（已停用的排除，onlyFree 时仅免费）+ 启用中的具名模型组合。
//
// 组合不插入 accounts.Pool.Models（避免污染上游归属 / 同步 / 删除 / Cline-Zen 分流判定），
// 只在此发现层追加，owned_by="combo" 便于客户端区分。组合可能跨免费/付费源，故
// 不受 onlyFree 过滤影响（组合是管理员显式选择的集合，不是单一计费模型）。

// 管理员手动停用的模型不进客户端模型列表

// 通配符排除的模型不进列表（与请求入口同一策略）

// routing.DefaultModel 返回用户设置的默认模型；未设置时优先回退到第一个远程 free 模型，
// 否则用内置 fallback。

// 管理员设置的默认模型也会随上游下架而失效：远程同步已启用却查不到时
// 视为已下架，落到下方「第一个远程免费模型」，避免每个无模型请求必败。
// 被管理员手动停用的默认模型同样跳过（含离线模式）。

// 远程同步已启用但上游没有免费模型（列表调整 / 接口变化）：从仍然在线的
// 模型里挑一个，绝不在有实时数据时退回下面的硬编码内置表（写死的模型
// 已下架时会让每个无模型请求必败）。

// 仅离线 / 从未同步成功时才允许内置硬编码 fallback。

func CorsHandler(h http.HandlerFunc) http.HandlerFunc {
	return httpx.CORS(h)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	httpx.WriteJSON(w, status, data)
}

func cleanMessages(messages []any) []any {
	return openaichat.CleanMessages(messages)
}

// shortenToolName truncates names above the upstream limit deterministically.
func shortenToolName(orig string) string {
	return openaichat.ShortenToolName(orig)
}

// clampParamsToolNames shortens over-long tool names and returns the mapping
// used to restore them on the response path.
func clampParamsToolNames(params map[string]any) map[string]string {
	return openaichat.ClampToolNames(params)
}

// restoreToolCallsInResponse restores shortened tool names in stream and
// non-stream response shapes.
func restoreToolCallsInResponse(obj map[string]any, shortToOrig map[string]string) {
	openaichat.RestoreToolCalls(obj, shortToOrig)
}

// sanitizeMessages repairs malformed tool_calls in outbound history.
func sanitizeMessages(messages []any) []any {
	return openaichat.SanitizeMessages(messages)
}

// genToolUseID returns a fallback tool_use block id.
func genToolUseID() string {
	return openaichat.GenToolUseID()
}

// hasToolUseBlocks reports whether content contains an Anthropic tool_use block.
func hasToolUseBlocks(content any) bool {
	return openaichat.HasToolUseBlocks(content)
}

func numericInt(value any) (int, bool) {
	return openaichat.NumericInt(value)
}

// ModelMaxOutputLimit 返回模型已知的最大输出 token 硬上限（0=未知，不封顶）。
// 已知硬限制表优先（gemini-3.8-flash 等）；其次取池中该模型的 Output 元数据
// （remote 同步 / 管理页设置 / 用户自定义模型）。zen 模型的 Output 是压缩预算
// 用的估值（未知模型默认 32768），作为硬上限会误伤长输出，跳过。

// 客户端发的 0 视为未设置、1~15 低于上游硬下限：一律兜到默认值，
// 否则 muse-spark 等模型直接 400 且错误会被回退链吞掉

// 模型已知硬上限封顶：gemini-3.8-flash 最大输出 65536，默认预算 128000 会被
// 上游网关 400（MaxOutputTokens out of range / Request contains an invalid argument），
// 且 429 配额限流回退到 vertex/google 路由时必现。

// Cline 上游不接受 "none" 枚举：客户端显式关闭推理时直接省略该字段；
// 空字符串同样省略（避免把非法枚举下发到上游）。

// 定稿前清洗 tools：null 可选字段会被部分上游网关硬拒 400（见函数注释）。

// SanitizeToolsForUpstream cleans optional tool fields before a request body is
// finalized for any gateway.
func SanitizeToolsForUpstream(body map[string]any) {
	openaichat.SanitizeTools(body)
}

func clineHeaders(token, sessionID string) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	h.Set("Content-Type", "application/json")
	h.Set("X-Task-ID", sessionID)

	cfg := ConfigProvider()
	for k, v := range cfg.Headers {
		h.Set(k, v)
	}

	return h
}

func clineErrorHTTPStatus(err error) int {
	if _, ok := err.(*routing.FreeModelUnavailableError); ok {
		return http.StatusTooManyRequests
	}
	return http.StatusInternalServerError
}

// IsDeterministicRequestError 判断 err 是否为「换账号/换模型重试也无法自愈」的
// 上游确定性 400：invalid-request 语义（invalid_request_error 类型、invalid_request
// 伪码、网关字段校验 "Invalid input"，如工具可选字段被序列化为 null）。
// 上下文超限 400 显式排除（IsContextLengthError）：那类错误有压缩+重试的自愈路径，
// 必须保留现有的 truncate/retry 行为。限流 429/5xx 不在此列。

// routing.ServedByParam 是 routing.CallClineAPI 族回写在 params 上的「实际服务方」标记：
// 自定义 provider 成功服务时写入 zen.UpstreamProvider。仅用于请求日志归因——
// BuildUpstreamBody / providers.Call / zen.Call 只拷贝白名单键，不会外发。

// routing.ProviderNameParam 与 routing.ServedByParam 配套：自定义 provider 成功服务时回写实际
// 命中之家的显示名，供 stampUpstream 归因到请求日志的 providerName 字段。

// stampUpstream 按实际服务结果写入请求日志的上游归因：
// provider 标记 > 生效模型是 zen 模型（反向故障转移）> Cline 账号池。
func stampUpstream(reqLog *domain.RequestLog, params map[string]any) {
	if v, _ := params[routing.ServedByParam].(string); v == zen.UpstreamProvider {
		reqLog.Upstream = zen.UpstreamProvider
		reqLog.ProviderName, _ = params[routing.ProviderNameParam].(string)
		reqLog.ProviderID, _ = params[providers.ProviderIDParam].(string)
		return
	}
	if m, _ := params["model"].(string); m != "" {
		if _, isZen := zen.ResolveInfo(m); isZen {
			reqLog.Upstream = zen.UpstreamOpenCode
			reqLog.ProviderName = ""
			reqLog.ProviderID = ""
			return
		}
	}
	reqLog.Upstream = zen.UpstreamCline
	reqLog.ProviderName = ""
	reqLog.ProviderID = ""
}

// 归因标记按次重算，避免流重试沿用上一次结果
// 同步清掉上次归因的 provider 名
// 同步清掉上次归因的 provider ID
// Request-level pin "model@ProviderName": only the named provider may
// serve. An explicit pin must fail loudly rather than silently fall back
// to a redundancy sibling, the Cline pool, or the fallback chain.

// upstream body and log attribution carry the bare id

// Cline 残血池整条链耗尽（429 冷却/无账号）→ 落到 zen 免费模型。
// 转移前记下链尾实际模型：没有任何 zen 模型服务成功时还原，
// 否则请求日志会把失败请求记成最后一个 zen 尝试的模型
// （链尾 cline 模型才是实际归宿）。

// zen 免费模型进入 cline 池仅发生在 zen 故障转移期间：改写成 cline 侧可用的
// free 模型链，否则 Cline 上游会报 "invalid model format. Expected format:
// modelType/model"（zen 的裸模型 ID 不符合 Cline 的 provider/model 格式）。

// 显式模型请求降级序列：
//  1. 自定义 provider（若该模型接入了 provider）
//  2. 客户端点名模型走 Cline 池
//  3. modelChain（管理员配置或内置 free 链）逐个降级
//  4. 全部失败 → 明确报错
// 可用性感知重排：点名模型保持首位，其余按「未冷却优先、用量少优先」
// 重排，避免回退流量每次都集中砸在第一个可用模型上直到它也冷却。

// 会话亲和（管理员开关，默认关闭）：同一会话在 TTL 内钉到同一 Cline 账号。

// 回退轮数/每轮凭证上限由管理员 RetryPolicy 决定；默认 rounds=1、
// credCap=0（不限），与旧行为完全一致。rounds>1 时每轮之间等待一个有界
// 窗口，给短冷却账号恢复的机会；确定性错误/stop 规则走 return 直接终止，
// 不会被后续轮次空转重跑。

// 先试自定义 provider

// 回写实际服务模型，供请求日志归因

// providers.CallAPI 已逐个记录 provider 失败原因，链上不重复打日志

// 错误规则声明终止：不再走账号池/链

// 点名模型保持原有轮询语义；链上的降级模型改用「最久未用优先」，
// 避免所有降级流量都压到第一个可用账号/模型的额度上。

// 本轮该模型已试满 maxCredentialsPerRound 个账号

// 该模型所有账号均冷却/不可用 → 尝试链上下一个模型

// 回写实际服务模型，供请求日志归因

// 管理员错误规则先于内置 402/429 分类：stop 立即中止整条链，
// continue 落到下面照常轮转（仅可能附加冷却），二者都不改变成功路径。

// 402 余额不足是账号级问题（该账号扣费额度耗尽），不是模型级：
// 冷却该账号上的这个模型（30min），换下一个账号继续试当前模型。
// 不换账号会导致轮询反复选中同一破产账号，每个请求先烧一次 402 往返；
// 不能整账号冷却 —— 同账号上仍在免费列表的模型零成本可用。

// 非 429/402 错误：若后面还有候选（provider / 链）则继续降级，否则透传

// 429：模型冷却已记录，换下一个账号；全部冷却后降级到下一个模型

// 终极兜底：free 链（手动链全部失败时自动切换）

// CallFreeClineAPI 就地改写 fp，取回实际服务模型

// provider 经 free 链服务时同步归因标记

// 同步实际服务的 provider 名

// Cline 侧（含 free 链）全部耗尽 → 反向故障转移到 zen 免费模型。
// 没有任何 zen 模型服务成功时还原模型名：请求日志按 params 记实际服务
// 模型，不能记成最后一次失败的 zen 尝试

// WithModel 返回带指定模型名的参数副本（避免污染调用方 map）。

// isFreeAliasModel 判断是否为 "free" 别名或链内免费模型（避免重复兜底）。

// defaultFreeChain 动态派生默认回退链（管理员未配置 modelChain 时）：
// 优先取 Cline 远程同步的免费模型（有效、不下架），再补内置常量链。
// 已下架的内置模型（如 longcat-2.0，不在远程同步列表）自动排除，不再写死；
// 从未同步成功（离线）时回退内置常量链。

// 启用中自定义 provider 的免费模型参与自动回退（provider 失败时也会被
// CallFreeClineAPI 内的 providers.CallAPI 尝试，其次才是账号池）

// LiveModelSet 返回当前仍然「在线」的模型 ID 集合：池内全部模型（远程同步
// remote / opencode zen 同步 / 管理员自定义，已手动停用的除外）+ 启用中自定义
// provider 声明的模型。
// 调用方须持有 accounts.PoolMu；providers 走独立的 providersMu，仓库内不存在反向持锁路径。

// provider 声明的模型（已同步进池的直接生效；池内被停用的一致排除）

// routing.IsModelDisabled 判断模型是否被管理员在管理页手动停用（Disabled 标记）。
// 支持 "opencode/" 前缀别名（与 zen.RouteModel 的匹配方式一致）。
// 停用语义：不进客户端模型列表、直接请求在入口处拒绝、默认模型与回退链跳过。

// filterStaleModels 从回退链里剔除已下架的模型。
//
// 上游免费/推荐模型每日调整，写死或旧配置里的模型会随下架而失效：继续尝试
// 只会白白多一次必败的上游往返（404/400 + 冷却），排在链头时每个请求都要先
// 撞一次失败。判定来源：远程同步成功的模型列表（含 zen/自定义）+ 启用中
// provider 的 modelIds。
//
// 远程同步从未成功（离线冷启动）时不判定、原样保留；剔空时也原样保留
// （宁可按原链试一次，也不要空链直接报错）。

// hasAnyFallbackLeft 判断点名模型之后是否还有候选（决定 500 是否透传）。

// routing.ModelFallbackChain 显式模型的降级序列：点名模型优先，其后是管理员配置的
// 回退链（modelChain）；未配置时动态派生默认链（排除已下架模型）。均去重。
// 用户显式配置的链**不做列表过滤**：官方列表之外常有实测仍可用的模型
// （列表只是参考不是白名单），失效条目由请求时的模型级冷却与故障转移兜底；
// 只有动态派生的免费链才按 LiveModelSet 过滤下架条目。点名模型本身始终
// 保留 —— 它是客户端显式要求的（可能由 zen / provider 服务，不归本仓判定）。

// hasActiveAccounts 池中是否存在 active 状态的账号。

// "free" 别名的实际顺序：管理员配置的回退链优先，否则动态派生默认链。

// 先剔除已下架模型，再按可用性重排：把流量摊到用量最少的可用模型上，
// 而不是顺序打满第一个，也不在必败的死模型上浪费一次上游往返。

// 本次调用重新归因
// 同步清掉上次归因的 provider 名
// 同步清掉上次归因的 provider ID

// 与显式模型链一致：链内模型命中自定义 provider 时先走 provider，
// 失败（记冷却）后继续用账号池兜底——free 别名不再绕过 provider。

// providers.CallAPI 已逐个记录 provider 失败原因，链上不重复打日志

// 错误规则声明终止 free 链

// "free" 链是纯降级路径：全部用「最久未用优先」挑账号，摊平用量。
// 会话亲和开启时优先复用绑定的账号（free 别名请求正是上游 session
// 连续性最有价值的路径）。

// 绑定账号只试一次，失败后融入常规轮转

// 管理员错误规则先于内置分类：stop 中止整条 free 链。

// 429 限流 / 402 余额不足：都是账号级问题 —— 402 冷却该账号上的
// 这个模型后换下一个账号继续试，不能换模型（其他账号可能还有额度）

// 确定性 payload 400：链上其余模型会因同一非法请求体收到同一 400，
// 整条链直接放弃 —— 不再逐账号/逐模型空转注定失败的上游往返。

// 其他 API 错误（如上游 500）：换链内下一个模型降级

// 整条链被限流耗尽：按 free 池不可用处理（429）

// Try other accounts

// Refresh token and retry

// 429：模型级冷却 —— 只暂停该模型，账号保持可用，其他模型继续转发

// 上游明确报「模型不存在」时清理下架残留（同步标记 Delisted 保留的模型）

// parseCooldownUntil 从 429 响应体中解析 "Try again in 1h 1m" 格式的等待时长，
// 返回预计恢复时间；解析失败则回退到 1 小时后。

// 解析失败，回退 1 小时

// routing.StartCooldownRecovery 启动后台 goroutine，每 30 秒检查一次 cooldown 账号，
// 对 CooldownUntil 已过期的账号执行探活，成功则自动激活。

// 有恢复时间且已过期 → 探活
// 无恢复时间（旧数据）→ 也尝试探活

// TestAccount sends a minimal "hi" request through a specific account to verify
// it can complete an upstream call. It does not update aggregate token counters
// or request logs; it is a diagnostic-only probe.

// max_tokens 不能太小：推理模型（effort=high）会把预算全耗在 reasoning 上，
// 16 token 时上游必回「empty response content」500，账号测试与冷却恢复
// 频繁误报（issue #32）。1024 对连通性探针足够且成本可忽略。

// If the account was in cooldown/expired but the test succeeded, restore it.

type TokenUsage = openaichat.Usage

func ParseTokenUsage(value any) TokenUsage {
	return openaichat.ParseUsage(value)
}

func mergeTokenUsage(current, next TokenUsage) TokenUsage {
	return openaichat.MergeUsage(current, next)
}

// 先判断是否免费模型（AllModels 会拿 accounts.PoolMu，必须在持有锁之前计算）

// 按模型细分统计（仅记录 free 模型）

// IsFreeModelID 判断模型是否为 free 计费（用于按模型统计和模型级冷却）。

// 未知模型：按 ID 后缀/前缀启发式判断

// ModelCooldownActive 判断某账号下该模型是否处于模型级冷却中。

// routing.HasAlternativeModel 判断降级链上是否还有未冷却的候选模型。
// 用于避免「所有模型都被冷却」导致整条链不可用。

// routing.SetModelCooldown 记录模型级冷却（429 或首 token 前失败时调用）：只暂停该模型，账号保持可用。
// fallback 为解析失败时的恢复时长（默认 1 小时）。

func hasFirstOutput(obj map[string]any) bool {
	return openaichat.HasFirstOutput(obj)
}

// Anthropic Messages API support

func extractStringContent(raw json.RawMessage) string {
	return anthropicconv.ExtractStringContent(raw)
}

// routing.ZenFailoverToCline zen 调用失败后的透明降级：改走 cline 账号池 free 模型链。
// attempted=false 表示未启用故障转移，调用方维持原错误路径。
// 注意：失败计数由 zen.Call 内部标记（每请求恰好一次），此处不再重复计数，
// 否则限流/服务错误路径 + 此处各计一次，故障转移会被过早触发。

// clineFailoverToZen Cline 侧（含 free 池链）全部耗尽后的反向故障转移：
// 落到 opencode zen 免费模型。与 routing.ZenFailoverToCline 方向相反，形成双向闭环——
// 任一免费上游挂掉，流量自动落到另一条。zen 未启用或处于故障转移窗口
// （连续失败被判定不可达）时不尝试；最多试 3 个免费模型，避免在坏模型上反复烧时间。

// firstContentTimeoutForAttempt 返回本次尝试等待「首个内容事件」的上限：
// 普通尝试用较短的上限快速换模型；最后一次尝试放宽，宁可慢也尽量把答案交出去。
func firstContentTimeoutForAttempt(attempt, maxAttempts int) time.Duration {
	if attempt >= maxAttempts {
		return envRoutingDurationFromMS("LAST_ATTEMPT_TIMEOUT_MS", lastAttemptFirstContentTimeout)
	}
	return envRoutingDurationFromMS("FIRST_CONTENT_TIMEOUT_MS", defaultFirstContentTimeout)
}

// streamFailoverCooldown 是「上游在产出任何内容前失败」时对该模型施加的冷却时长。
// 目的：点名模型（如 glm-5.3-flash）排队超时 / 空闲 504 / 早断流时，同一次请求内
// 立即换到配置链上的下一个模型，而不是把错误抛给客户端；同时短期内不再把新请求压到它上面。
const streamFailoverCooldown = 2 * time.Minute

func NormalizeOpenAIResponse(obj map[string]any) map[string]any {
	return openaichat.NormalizeResponse(obj)
}

func sanitizeContent(s string) string { return protocol.SanitizeContent(s) }

func normalizeMessage(msg map[string]any) map[string]any {
	return openaichat.NormalizeMessage(msg)
}

func getNested(obj map[string]any, keys ...any) any {
	return protocol.GetNested(obj, keys...)
}

func freePort(port int) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return // port is free
	}
	conn.Close()

	// Try to kill the process using the port
	cmd := exec.Command("powershell", "-Command",
		fmt.Sprintf(`$p=Get-NetTCPConnection -LocalPort %d -ErrorAction SilentlyContinue; if($p){Stop-Process -Id $p.OwningProcess -Force}`, port))
	_ = cmd.Run()
	time.Sleep(500 * time.Millisecond)
}
