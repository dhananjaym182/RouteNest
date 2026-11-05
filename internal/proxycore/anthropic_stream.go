package proxycore

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"routenest/internal/combos"
	"routenest/internal/compaction"
	"routenest/internal/dispatch"
	"routenest/internal/domain"
	"routenest/internal/httpx"
	"routenest/internal/platform"
	"routenest/internal/protocol"
	anthropicconv "routenest/internal/protocol/anthropic"
	"routenest/internal/protocol/openaichat"
	"routenest/internal/providers"
	"routenest/internal/requestlog"
	"routenest/internal/routing"
	"routenest/internal/zen"
	"strings"
	"time"
)

// maxStreamAttempts 预提交失败的最大尝试次数（含首次）。实际次数还会被降级链长度限制：
// 3 次 × 短耐心，总等待被压在客户端可接受范围内，而不是在整条链上把时间耗光。
// 链上有几个模型就最多试几个，避免在同一个模型上反复空等。
const maxStreamAttempts = 3

// streamAnthropicWithRetry 获取上游响应并转发 Anthropic 流。
//
// 首 token 之前失败（预提交）可以透明重试：客户端只看到已发出的信封与 ping，
// 看不到重试痕迹。每次失败还会把该模型短时冷却，使后续尝试自动切换到配置链上的
// 下一个模型 —— 这是「点名模型老是超时」的缓解：坏模型自动让位给备用模型。
// 预提交失败（上游在任何数据产出前断开/报错/静默超时）时客户端尚未收到任何字节，
// 自动重新获取并重试，对客户端完全透明 —— 这是修复「流式经常断」的核心：
// 排队/思考阶段的 504 idle timeout 以前会直接杀死流，现在变成一次不可见的重试。
// sseWriter 包装 http.ResponseWriter，让预提交重试共享同一个响应：
//   - start() 幂等，重试时不会重复 WriteHeader（Go 会报 superfluous 警告）；
//   - write() 每次写完立即 Flush，保证中间层（Cloudflare 隧道等）及时看到字节。
//
// 同时用 started 记录「是否已有字节发出」，供调用方决定重试用 JSON 还是带内 error。
type sseWriter = dispatch.SSEWriter

func NewSSEWriter(w http.ResponseWriter) *sseWriter {
	return dispatch.NewSSEWriter(w)
}

// emitAnthropicErrorEvent 在响应头已发出（SSE 模式）后向客户端报告错误：
// 此时无法再返回 JSON 状态码，改用 Anthropic 协议的 error 事件。
func emitAnthropicErrorEvent(sw *sseWriter, msg string) {
	sw.EmitError(msg)
}

// finishAnthropicStreamFailure 统一处理流式尝试失败后的报错：
// 响应头尚未发送 → JSON 状态码；已经发出 → 带内 error 事件（避免重复 WriteHeader）。
func finishAnthropicStreamFailure(sw *sseWriter, w http.ResponseWriter, reqLog *domain.RequestLog, status int, note, msg string) {
	requestlog.Finalize(reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, note)
	if sw.Started {
		log.Printf("  anthropic stream: %s (reported as in-band error event)", note)
		emitAnthropicErrorEvent(sw, msg)
		return
	}
	httpx.WriteJSON(w, status, map[string]any{
		"error": map[string]string{"message": msg, "type": "api_error"},
	})
}

// StreamAnthropicCombo 组合的 Anthropic 流式路径：按配置顺序逐个条目尝试，仅在
// **尚未向客户端产出任何内容**（文本 / thinking / 具名工具调用）时才切换到下一个
// 条目 —— 复用 HandleAnthropicStream 的预提交可重试语义与共享 sseWriter（信封只发
// 一次）。一旦已经交付有意义的内容，该次尝试即视为完成，绝不把答案重开到另一个模型。
// 尝试次数被组合的可用条目数上界约束，避免无限回退。
// compactionSummary / compactionApplied 与单模型流路径（HandleAnthropicStream）一致：
// CM 已在路由前应用并改写了 params["messages"]；组合流同样要在信封后发出压缩 advisory
// 块与 message_delta 里的 context_management。compaction 块由 sseWriter.compactionEmitted
// 幂等保护，预提交回退时不会重复发送。
func StreamAnthropicCombo(ctx context.Context, w http.ResponseWriter, params map[string]any, combo domain.ModelCombo, reqLog *domain.RequestLog, sessionID string, compactionSummary string, compactionApplied []compaction.CMAppliedEdit) {
	sw := NewSSEWriter(w)

	type cand struct {
		model string
		entry domain.ComboEntry
		order int
	}
	var cands []cand
	seen := map[string]bool{}
	trace := make([]domain.AttemptLog, 0, len(combo.Entries))
	for i, e := range combo.Entries {
		m := strings.TrimSpace(e.Model)
		if m == "" {
			continue
		}
		if e.Disabled {
			trace = append(trace, domain.AttemptLog{Model: m, Order: i + 1, Outcome: "skipped", Reason: "entry disabled by admin",
				Source: strings.TrimSpace(e.Source), Provider: strings.TrimSpace(e.Provider)})
			continue
		}
		key := combos.EntryKey(e)
		if seen[key] || routing.IsModelDisabled(m) {
			reason := "duplicate entry"
			if routing.IsModelDisabled(m) {
				reason = "model disabled by admin"
			}
			trace = append(trace, domain.AttemptLog{Model: m, Order: i + 1, Outcome: "skipped", Reason: reason,
				Source: strings.TrimSpace(e.Source), Provider: strings.TrimSpace(e.Provider)})
			continue
		}
		seen[key] = true
		cands = append(cands, cand{model: m, entry: e, order: i + 1})
	}
	// 预置组合字段：HandleAnthropicStream 成功时会直接 finalize 请求日志。
	combos.ApplyLog(reqLog, combo, trace, "")
	stampUpstream(reqLog, params)

	if len(cands) == 0 {
		finishAnthropicStreamFailure(sw, w, reqLog, http.StatusBadGateway,
			"combo has no eligible entries", "combo has no eligible entries")
		return
	}

	// 组合级主动压缩：以候选模型中最小的有效上下文为预算，一次压缩写回共享
	// params（combos.CallSingleModel 压缩的是 map 副本，条目之间不会传递）。
	if out := compaction.MaybeCompact(params, combos.CompactionModel(combo), sessionID); out.Changed {
		log.Printf("  combo %q %s", combo.ID, out.Note)
	}

	// 上下文超限的「截断重试」整条组合只做一次：截断后的 params 在后续条目间共享。
	// 与 combos.RunCombo 的非流式路径保持一致 —— 流式组合此前缺少这一步，导致某个条目
	// 因上下文超限返回 400 时整条组合直接中止（单模型路径会先压缩/截断再回退）。
	truncatedForRun := false

	for idx, c := range cands {
		if ctx.Err() != nil {
			return
		}
		start := time.Now()
		resp, acc, upstream, provName, err := combos.CallSingleModel(params, c.entry, true, sessionID)
		if err != nil && routing.IsContextLengthError(err) && !truncatedForRun {
			truncatedForRun = true
			log.Printf("  combo %q: entry %d %q context length exceeded, truncating and retrying", combo.ID, c.order, c.model)
			if out := compaction.FallbackTruncate(params, compaction.ModelForCompaction(c.model)); out.Changed {
				log.Printf("  combo %s", out.Note)
				start = time.Now()
				resp, acc, upstream, provName, err = combos.CallSingleModel(params, c.entry, true, sessionID)
			}
		}
		if err != nil {
			trace = append(trace, domain.AttemptLog{Model: c.model, Order: c.order, Upstream: upstream, Status: combos.AttemptStatus(err),
				Outcome: "failed", Reason: platform.Truncate(err.Error(), 200), DurationMs: time.Since(start).Milliseconds(),
				Source: strings.TrimSpace(c.entry.Source), Provider: strings.TrimSpace(c.entry.Provider),
				ProviderName: provName})
			combos.ApplyLog(reqLog, combo, trace, "")
			if routing.IsContextLengthError(err) {
				// 上下文上限是模型/上游各自的属性：当前条目装不下，换下一个条目仍可能成功。
				continue
			}
			if !combos.Retryable(err) {
				break
			}
			continue
		}
		params["model"] = c.model
		if upstream == zen.UpstreamProvider {
			params[routing.ServedByParam] = zen.UpstreamProvider
			params[routing.ProviderNameParam] = provName
		} else {
			delete(params, routing.ServedByParam)
			delete(params, routing.ProviderNameParam)
			delete(params, providers.ProviderIDParam)
		}
		// 每次成功建流后按实际服务方重算归因（流式组合此前只在循环外 stamp 过一次，
		// 会把 provider/zen 条目错记为 cline）。
		stampUpstream(reqLog, params)
		if acc != nil {
			reqLog.AccountID = acc.AccountID
			reqLog.AccountEmail = acc.Email
		}
		// 乐观写入成功条目：HandleAnthropicStream 成功后会直接 finalize 落盘。
		trace = append(trace, domain.AttemptLog{Model: c.model, Order: c.order, Upstream: upstream,
			Outcome: "success", DurationMs: time.Since(start).Milliseconds(),
			Source: strings.TrimSpace(c.entry.Source), Provider: strings.TrimSpace(c.entry.Provider),
			ProviderName: provName})
		combos.ApplyLog(reqLog, combo, trace, c.model)

		if HandleAnthropicStream(ctx, sw, resp, acc, reqLog, firstContentTimeoutForAttempt(idx+1, len(cands)), compactionSummary, compactionApplied) {
			return // 已交付内容 / 已完整收尾（含带内 error）
		}
		resp.Body.Close()
		if ctx.Err() != nil {
			return // 客户端断开：不是模型失败
		}
		// 预提交失败：把乐观成功条目改写为失败，冷却该条目模型，换下一个条目。
		trace[len(trace)-1] = domain.AttemptLog{Model: c.model, Order: c.order, Upstream: upstream,
			Outcome: "failed", Reason: "stream produced no content before first token",
			DurationMs: time.Since(start).Milliseconds(),
			Source:     strings.TrimSpace(c.entry.Source), Provider: strings.TrimSpace(c.entry.Provider),
			ProviderName: provName}
		combos.ApplyLog(reqLog, combo, trace, "")
		// 仅当组合里还有下一个条目时才冷却该模型（避免单条目组合冷却掉唯一模型，
		// 让下一次请求直接失败）；不再用全局 routing.ModelFallbackChain 作为判定依据。
		if acc != nil && c.model != "" && idx+1 < len(cands) {
			routing.SetModelCooldown(acc, c.model, time.Now().Add(streamFailoverCooldown))
			log.Printf("  combo %q: model %q produced no content, cooling %s and failing over", combo.ID, c.model, streamFailoverCooldown)
		}
	}
	if ctx.Err() != nil {
		return
	}
	log.Printf("  combo %q: giving up after %d attempt(s) with no content", combo.ID, len(cands))
	finishAnthropicStreamFailure(sw, w, reqLog, http.StatusBadGateway,
		"combo stream failed before first token", "upstream stream failed before first token")
}

func streamAnthropicWithRetry(ctx context.Context, w http.ResponseWriter, fetch func() (*http.Response, *domain.Account, error), reqLog *domain.RequestLog, compactionSummary string, compactionApplied []compaction.CMAppliedEdit) {
	sw := NewSSEWriter(w)
	// 尝试次数不超过降级链上的模型数：每个模型只给一次机会。
	maxAttempts := maxStreamAttempts
	if n := len(routing.ModelFallbackChain(reqLog.Model)); n > 0 && n < maxAttempts {
		maxAttempts = n
	}
	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return // 客户端已断开，不再发起上游请求
		}
		resp, acc, err := fetch()
		if err != nil {
			log.Printf("  anthropic api error: %v", err)
			finishAnthropicStreamFailure(sw, w, reqLog, clineErrorHTTPStatus(err), err.Error(), err.Error())
			return
		}
		if acc != nil {
			reqLog.AccountID = acc.AccountID
			reqLog.AccountEmail = acc.Email
		}
		if HandleAnthropicStream(ctx, sw, resp, acc, reqLog, firstContentTimeoutForAttempt(attempt, maxAttempts), compactionSummary, compactionApplied) {
			return
		}
		resp.Body.Close()
		// 客户端主动断开（Esc 中断 / Claude Code 空闲看门狗重连）不是模型的失败：
		// 此时冷却会把用户点名/配置的模型拉黑 2 分钟，后续请求被静默赶到回退链上，
		// 表现为「配置了模型却总走 fallback」。实测见 proxy.log 2026/09/26 09:52:52 /
		// 09:59:00：「client disconnected before first token」紧跟 pixel-canary 被冷却。
		// ctx 由 r.Context() 派生，在 handler 内只有客户端断开才会取消。
		if ctx.Err() != nil {
			return
		}
		// 上游在产出任何内容前失败（排队超时 / 空闲 504 / 早断流）：把这次实际使用的模型
		// 标记为短时冷却，下一次尝试就会自动落到链上的下一个模型（pool 的选择器会跳过
		// 冷却中的模型）。有备用模型时才冷却，避免把整条链一起冷掉。
		if acc != nil && reqLog.Model != "" && routing.HasAlternativeModel(acc, reqLog.Model) {
			routing.SetModelCooldown(acc, reqLog.Model, time.Now().Add(streamFailoverCooldown))
			log.Printf("  anthropic stream: model %q produced no content, cooling it for %s and failing over",
				reqLog.Model, streamFailoverCooldown)
		}
		if attempt >= maxAttempts {
			log.Printf("  anthropic stream: giving up after %d attempts with no content", attempt)
			finishAnthropicStreamFailure(sw, w, reqLog, http.StatusBadGateway,
				"stream failed before first token", "upstream stream failed before first token")
			return
		}
		log.Printf("  anthropic stream: upstream produced no content, retrying (%d/%d)", attempt+1, maxAttempts)
		select {
		case <-ctx.Done():
			return
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// HandleAnthropicStream 把上游 OpenAI SSE 流转换为 Anthropic 事件流转发给客户端。
//
// 关键设计：
//  1. 响应头 + message_start 在上游接通后立即发出。保活心跳分两段：
//     尚未出内容时只发 SSE 注释行 `: ping` —— 足以喂饱中间层（Cloudflare 隧道 120s
//     Proxy Read Timeout）的字节级读超时、避免 524，但按规范它不是事件，因此**不会**
//     喂客户端的事件级看门狗（CLAUDE_STREAM_IDLE_TIMEOUT_MS）。这是有意为之：注定失败的
//     尝试宁可让客户端自己超时后重连（用户实测「断开重连」好于长时间静默），也不要
//     一直哄着它假活着；一旦出过内容就改发真实 ping 事件，因为那时保住这一回合的产出
//     比让客户端重跑更划算。
//  2. 信封提前发送但内容不提前：只要还没向客户端发出任何内容事件（文本 / thinking /
//     具名工具调用），本次尝试的失败（排队超时、空闲 504、上游静默、早断流）都可以透明
//     重试；重试沿用同一 message_start（msgID 由请求开始时间推导），客户端只看到一条
//     连续消息。判定依据是「有无内容」而不是「有无首行」—— 上游常常先送一条空 delta
//     （仅 role）就静默数分钟，那种情况必须能换模型，否则用户只能干等到超时。
//  3. firstContentTimeout 是本次尝试等待首个内容事件的上限，超时同样是可重试的失败。
//
// 返回 true 表示客户端响应已完整处理（正常收尾或已发出 error 事件）；
// 返回 false 表示上游尚未产出任何内容 —— 调用方重试，或用 sw.Started 判断后发带内 error。
func HandleAnthropicStream(ctx context.Context, sw *sseWriter, upstream *http.Response, acc *domain.Account, reqLog *domain.RequestLog, firstContentTimeout time.Duration, compactionSummary string, compactionApplied []compaction.CMAppliedEdit) bool {
	if upstream.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(upstream.Body, 2048))
		upstream.Body.Close()
		log.Printf("  anthropic stream: upstream status %d before stream: %s", upstream.StatusCode, platform.Truncate(string(b), 200))
		return false
	}
	if sw.Flusher == nil {
		log.Printf("  streaming not supported for client")
		io.Copy(io.Discard, upstream.Body)
		upstream.Body.Close()
		return false
	}
	if !sw.Started {
		copyPassthroughHeaders(sw.W, upstream.Header)
	}
	log.Printf("  anthropic stream: waiting for first token")

	// 上游 SSE 逐行泵：独立 goroutine 阻塞读取，主循环通过 select 同时处理
	// 心跳、客户端断开（ctx）与上游数据；放弃时关闭 body + pumpStop 确保不泄漏。
	type sseLine struct {
		line string
		err  error
	}
	lines := make(chan sseLine)
	pumpStop := make(chan struct{})
	go func() {
		reader := bufio.NewReader(upstream.Body)
		for {
			l, err := reader.ReadString('\n')
			select {
			case lines <- sseLine{l, err}:
			case <-pumpStop:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	defer close(pumpStop)

	emit := func(event string, data any) {
		d, _ := json.Marshal(data)
		sw.Write([]byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event, string(d))))
	}

	// msgID 跨预提交重试保持稳定：整个请求只发一个信封，重试沿用同一条消息。
	msgID := "msg_" + fmt.Sprintf("%x", reqLog.StartedAt.UnixMilli())
	// 块索引/思考-文本生命周期/工具累计是纯协议逻辑，由 anthropic 适配器负责；
	// 这里只做上游泵、心跳、重试与记账。compaction advisory 占用 index 0。
	startIndex := -1
	if compactionSummary != "" {
		startIndex = 0
	}
	st := anthropicconv.NewStreamState(startIndex)
	st.ToolID = openaichat.GenToolUseID
	committed := false

	// 信封提前发送：响应头 + message_start + 首个 ping 事件。
	// 必须从第 0 秒起就有事件流动 —— 大上下文下上游排队/预填充实测 TTFT 可达 3~4 分钟，
	// 期间只有注释行（不是事件）时，Claude Code 的流空闲看门狗会把连接当成卡死。
	// 重试沿用同一信封（sw.Envelope 幂等），客户端看不到任何重试痕迹。
	// model 取 reqLog.Model（调用方已在 fetch 时更新为含回退的实际服务模型），
	// 不能发空串 —— 客户端会丢失流式消息的模型身份。
	sw.Start()
	if !sw.Envelope {
		sw.Envelope = true
		emit("message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":          msgID,
				"type":        "message",
				"role":        "assistant",
				"content":     []any{},
				"model":       reqLog.Model,
				"stop_reason": nil,
			},
		})
	}
	// 接通即写一个注释行：从第 0 秒起就有字节流出（Cloudflare 不会 524），
	// 但客户端的事件级看门狗不受影响 —— 这段等待若是白等，客户端会按自己的节奏超时重连。
	sw.Write([]byte(": ping\n\n"))

	if compactionSummary != "" && !sw.CompactionEmitted {
		sw.CompactionEmitted = true
		compaction.EmitCompactionBlock(sw, compactionSummary)
	}

	// commit：上游首条有效数据行到达，开始实时转发（信封已提前发送）。
	// committed=true 后本次尝试不再重试，只能实时转发或发带内 error。
	commit := func() {
		committed = true
		log.Printf("  anthropic stream: starting real-time forward")
	}

	var latestUsage TokenUsage
	var firstOutputAt time.Time

	// 心跳：SSE 注释行，所有 SSE 客户端按规范忽略，零开销。
	// 提交前后都要发：提交前是防止中间层（Cloudflare 120s 空闲读超时）掐断连接的关键，
	// 提交后用于长时间思考/工具生成阶段保活。
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	// 首个内容事件的上限：上游连一行 data 都没有、或只发空 delta 就长期静默时，
	// 最多等 firstContentTimeout 就放弃本次尝试并让调用方换链上下一个模型。
	firstContentTimer := time.NewTimer(firstContentTimeout)
	defer firstContentTimer.Stop()

L:
	for {
		select {
		case <-ctx.Done():
			// 客户端断开：停止转发并释放上游连接，不再为已放弃的请求继续耗账号流量
			upstream.Body.Close()
			if committed {
				log.Printf("  anthropic stream aborted: client disconnected")
				requestlog.Finalize(reqLog, latestUsage, firstOutputAt, reqLog.StartedAt, false, "client disconnected")
			} else {
				log.Printf("  anthropic stream: client disconnected before first token")
			}
			return committed
		case <-firstContentTimer.C:
			if !st.HasContent() {
				log.Printf("  anthropic stream: no content from upstream within %s, aborting attempt", firstContentTimeout)
				upstream.Body.Close()
				return false
			}
		case <-heartbeat.C:
			// 已出过内容 → 发真实事件：这一回合已经有产出，宁可保活到结束也不要让客户端
			// 中途放弃后整轮重跑（长回合重跑的代价远大于多等一会儿）。
			// 还没出内容 → 只发注释行：只喂字节级看门狗（Cloudflare），注定失败的尝试
			// 让客户端按自己的节奏超时并重连（用户偏好断开重连而非长时间静默假活着）。
			if st.HasContent() {
				emit("ping", map[string]any{"type": "ping"})
			} else {
				sw.Write([]byte(": ping\n\n"))
			}
		case sl, ok := <-lines:
			if !ok {
				// 读取泵退出但未送达错误（防御性处理）
				if !st.HasContent() {
					log.Printf("  anthropic stream: upstream closed before any content, failing over")
					return false
				}
				log.Printf("  anthropic stream broken: upstream closed unexpectedly")
				emit("error", map[string]any{"type": "error", "error": map[string]any{"type": "api_error", "message": "upstream connection closed unexpectedly"}})
				requestlog.Finalize(reqLog, latestUsage, firstOutputAt, reqLog.StartedAt, false, "upstream closed unexpectedly")
				return true
			}
			if sl.err != nil {
				if !st.HasContent() {
					// 建流后没出任何内容就断了（实测 "unexpected EOF" 会静默 10 分钟才发生）：
					// 不要把它当成「已提交所以只能报错」，换模型重试对客户端完全无感。
					log.Printf("  anthropic stream: upstream ended before any content: %v", sl.err)
					return false
				}
				if errors.Is(sl.err, io.EOF) {
					break L // 正常结束 → 走收尾路径（ReadString 带回的残包与旧实现一致忽略）
				}
				// 真实的读取错误（超时/重置）：记录原因、通知客户端，而不是无声截断
				log.Printf("  anthropic stream broken: %v", sl.err)
				emit("error", map[string]any{"type": "error", "error": map[string]any{"type": "api_error", "message": sl.err.Error()}})
				requestlog.Finalize(reqLog, latestUsage, firstOutputAt, reqLog.StartedAt, false, "stream read error: "+sl.err.Error())
				return true
			}
			line := strings.TrimRight(sl.line, "\r\n")
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(line[5:])
			if payload == "" {
				continue
			}
			if payload == "[DONE]" {
				if !st.HasContent() {
					// 上游一个内容块都没给就关闭 —— 视为失败，调用方可换模型重试
					log.Printf("  anthropic stream: upstream sent empty stream before any content")
					return false
				}
				continue
			}

			var obj map[string]any
			if err := json.Unmarshal([]byte(payload), &obj); err != nil {
				continue
			}
			if data, ok := obj["data"]; ok {
				if d, ok := data.(map[string]any); ok {
					obj = d
				}
			}
			// 回程还原出站截短的工具名（Meta 后端硬限 ≤64 的映射）
			restoreToolCallsInResponse(obj, reqLog.ToolNames)
			if usage := ParseTokenUsage(obj["usage"]); usage.Valid {
				latestUsage = mergeTokenUsage(latestUsage, usage)
			}
			if firstOutputAt.IsZero() && hasFirstOutput(obj) {
				firstOutputAt = time.Now()
			}

			// 上游带内错误（如 {"code":504,"message":"Upstream idle timeout exceeded"}）。
			// 只要还没发出内容事件（StreamState.HasContent 为 false）就仍是可透明重试的失败；
			// 已经发出内容后则转发 error 事件并终止 —— 绝不伪造 end_turn 收尾（旧行为会让
			// 客户端收到 error 后又收到一个「成功的空消息」，表现为静默断流）。
			if errPayload, ok := obj["error"]; ok {
				errBody, _ := json.Marshal(errPayload)
				if !st.HasContent() {
					log.Printf("  anthropic stream: upstream error before any content: %s", string(errBody))
					return false
				}
				log.Printf("  anthropic stream broken by upstream error: %s", string(errBody))
				emit("error", map[string]any{"type": "error", "error": errPayload})
				requestlog.Finalize(reqLog, latestUsage, firstOutputAt, reqLog.StartedAt, false, "upstream error: "+string(errBody))
				return true
			}

			// 上游被截短的工具名还原为客户端原始名
			restoreToolCallsInResponse(obj, reqLog.ToolNames)

			if !committed {
				commit() // 首条有效数据行到达，提交响应头 + message_start
			}

			choices, _ := protocol.GetNested(obj, "choices").([]any)
			if len(choices) == 0 {
				continue
			}
			choice, _ := choices[0].(map[string]any)
			if choice == nil {
				continue
			}

			for _, ev := range st.PushDelta(choice) {
				emit(ev.Name, ev.Data)
			}
		}
	}

	// 上游正常收尾但一个内容事件都没发出（无文本 / 无思考 / 无具名工具调用）：
	// 这不是可用的回答 —— 实测 glm-5.3-flash 会以 4~5 分钟的空 end_turn 结束。
	// 视为本模型失败：返回 false 让调用方冷却该模型并换链上下一个模型重试。
	// 此时客户端只收到信封与 ping，没有任何内容事件，因此重试完全无感
	// （否则会重复 content_block index，客户端会错乱）。
	if !st.HasContent() {
		log.Printf("  anthropic stream: model %q finished with no content, failing over", reqLog.Model)
		return false
	}

	for _, ev := range st.Finalize() {
		emit(ev.Name, ev.Data)
	}

	msgDelta := map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   st.StopReason(),
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"output_tokens": latestUsage.Completion,
		},
	}
	if compactionApplied != nil {
		msgDelta["context_management"] = compaction.ContextManagementResponse(compactionApplied)
	}
	emit("message_delta", msgDelta)
	recordUsage(acc, upstream, reqLog, reqLog.Model, latestUsage)
	requestlog.Finalize(reqLog, latestUsage, firstOutputAt, reqLog.StartedAt, true, "")

	emit("message_stop", map[string]any{"type": "message_stop"})
	log.Printf("  anthropic stream done: hasText=%v tools=%d reason=%s", st.TextStarted(), st.ToolCount(), st.StopReason())
	return true
}
