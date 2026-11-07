package proxycore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"routenest/internal/compaction"
	"routenest/internal/domain"
	"routenest/internal/httpx"
	"routenest/internal/protocol"
	"routenest/internal/routing"
	"strings"
	"time"

	"routenest/internal/protocol/openaichat"
	responsesconv "routenest/internal/protocol/responses"
	"routenest/internal/requestlog"
)

// ============================================================================
// OpenAI Responses API (/v1/responses) ↔ chat/completions 双向转换
// 所有上游生效：zen 免费模型与 Cline 账号池均可通过该端点访问（Cursor 等客户端直连）。
// ============================================================================

func responsesToChat(body map[string]any) map[string]any {
	return responsesconv.ToChat(body)
}

func responsesInputToMessages(input any) []any {
	return responsesconv.InputToMessages(input)
}

func responsesToolsToChat(tools []any) []any {
	return responsesconv.ToolsToChat(tools)
}

// ============ 非流式响应转换 ============

func newResponseID(prefix string) string {
	return responsesconv.NewID(prefix)
}

func chatToResponses(chat map[string]any) map[string]any {
	return responsesconv.ChatToResponses(chat)
}

// ============ 流式响应转换（chat SSE → Responses SSE） ============

// chatStreamToResponses 逐行读取上游 chat SSE，转换为完整 Responses 事件生命周期。
// 请求日志/账号统计等编排留在 internal/proxycore，事件构造由 protocol/responses 负责。
//
// 与 chat 路径共用同一套流契约：上游静默时发心跳注释；必须看到终止哨兵
// （finish_reason 或 [DONE]）才算正常结束；已输出内容后断流会补发 error
// 事件而不是伪装成 response.completed；完全没有输出的尝试不写任何事件，
// 由调用方决定是否重试（预提交重试）。
func chatStreamToResponses(ctx context.Context, w http.ResponseWriter, upstream *http.Response, reqLog *domain.RequestLog, acc *domain.Account) streamResult {
	defer upstream.Body.Close()
	// Cancelling on return releases the pump goroutine even when this attempt
	// stops early (completed event, write failure) with lines still buffered.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	res := streamResult{}
	s := responsesconv.NewSSEWriter(w)
	st := &responsesconv.StreamState{}
	var latestUsage TokenUsage
	sawTerminal := false
	started := false
	start := func() {
		if !started {
			started = true
			s.Start()
		}
	}

	startedAt := time.Now()
	if reqLog != nil {
		startedAt = reqLog.StartedAt
	}
	finalize := func(ok bool, reason string) {
		res.usage = latestUsage
		if reqLog == nil {
			return
		}
		recordUsage(acc, upstream, reqLog, reqLog.Model, latestUsage)
		requestlog.Finalize(reqLog, latestUsage, res.firstOutputAt, startedAt, ok, reason)
	}
	gone := func() streamResult {
		upstream.Body.Close()
		res.clientGone = true
		res.usage = latestUsage
		if reqLog != nil {
			requestlog.Finalize(reqLog, latestUsage, res.firstOutputAt, startedAt, false, "client disconnected")
		}
		return res
	}

	lines := pumpSSE(ctx, upstream.Body)
	sink := newSSESink(w)
	hb, stopHB := newHeartbeat()
	defer stopHB()
	stallC, stopStall := firstContentDeadline(sink)
	defer stopStall()

	for {
		select {
		case <-ctx.Done():
			return gone()

		case <-hb:
			if !sink.heartbeat() {
				return gone()
			}

		case <-stallC:
			// Bootstrap window without first content: abort as retryable, the
			// same contract as an empty attempt.
			if sink.boot != nil && !sink.boot.committed {
				upstream.Body.Close()
				res.stalled = true
				return res
			}

		case ev, ok := <-lines:
			if !ok || ev.err != nil {
				if sawTerminal {
					start()
					s.Complete(st, responsesconv.Usage{
						Prompt: latestUsage.Prompt, Completion: latestUsage.Completion, Cached: latestUsage.Cached, Total: latestUsage.Total,
					})
					sink.release()
					res.terminal = true
					finalize(true, "")
					return res
				}
				if res.produced {
					res.truncated = true
					s.Fail("upstream stream ended before completion", "upstream_stream_truncated")
					finalize(false, "upstream stream truncated before completion")
					return res
				}
				res.empty = true
				return res
			}

			line := ev.line
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(line[5:])
			if payload == "" {
				continue
			}
			if payload == "[DONE]" {
				sawTerminal = true
				continue
			}
			var obj map[string]any
			if json.Unmarshal([]byte(payload), &obj) != nil {
				continue
			}
			obj = unwrapDataEnvelope(obj)
			if reqLog != nil {
				restoreToolCallsInResponse(obj, reqLog.ToolNames)
			}
			if m, ok := obj["model"].(string); ok && m != "" {
				s.Model = m
			}
			if usage := ParseTokenUsage(obj["usage"]); usage.Valid {
				latestUsage = mergeTokenUsage(latestUsage, usage)
			}
			if chatChunkFinishReason(obj) != "" {
				sawTerminal = true
			}
			delta := protocol.GetNested(obj, "choices", 0, "delta")
			if delta == nil {
				delta = protocol.GetNested(obj, "choices", 0)
			}
			if d, ok := delta.(map[string]any); ok {
				if chatChunkHasOutput(obj) {
					res.produced = true
					if res.firstOutputAt.IsZero() {
						res.firstOutputAt = time.Now()
					}
				}
				start()
				s.EmitDelta(d, st)
				if res.produced {
					// First semantic content: release everything held by the
					// bootstrap buffer; later frames pass straight through.
					sink.release()
				}
			}
		}
	}
}

// usageToResponses 把聚合的 TokenUsage 转成 Responses usage 结构。
func usageToResponses(u TokenUsage) map[string]any {
	return responsesconv.UsageToResponses(responsesconv.Usage{
		Prompt: u.Prompt, Completion: u.Completion, Cached: u.Cached, Total: u.Total,
	})
}

// ============ /v1/responses 入口 ============

func HandleResponses(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var params map[string]any
	if err := json.Unmarshal(body, &params); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	model, _ := params["model"].(string)
	isStream, _ := params["stream"].(bool)
	log.Printf("  responses: model=%s stream=%v", model, isStream)

	// Administrator model policy: alias rewrite + wildcard exclusions.
	if resolved, reject := routing.ResolveClientModel(model); reject != "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"message": reject, "type": "invalid_request_error"},
		})
		return
	} else if resolved != model {
		model = resolved
		params["model"] = resolved
	}

	// 管理员手动停用的模型在入口直接拒绝（不进列表，也不可点名调用）
	if model != "" && routing.IsModelDisabled(model) {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": "model " + model + " is disabled",
		})
		return
	}

	reqLog := domain.RequestLog{StartedAt: time.Now(), Protocol: "responses", Model: model, Stream: isStream}

	if msg := legacyIngressFreeOnlyRejection(model); msg != "" {
		requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, msg)
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"message": msg, "type": "invalid_request_error"},
		})
		return
	}

	// Opt-in nonstream keepalive (NonstreamKeepaliveSeconds): see chatHandler.
	if !isStream {
		if kw := newKeepaliveWriter(w); kw != nil {
			w = kw
			defer kw.stop()
		}
	}

	chat := responsesToChat(params)
	// 清洗畸形 tool_calls（空 function.name / 孤儿 tool 结果），避免上游 400
	if msgs, ok := chat["messages"].([]any); ok {
		chat["messages"] = openaichat.SanitizeMessages(msgs)
	}
	chatModel, _ := chat["model"].(string)

	sessionID := compaction.RequestSessionID(chat, r.Header)
	call, err := startUnifiedDispatch(r.Context(), chat, isStream, "responses", sessionID, &reqLog)
	if err != nil {
		log.Printf("  responses api error: %v", err)
		requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, err.Error())
		status := unifiedDispatchHTTPStatus(chatModel, err)
		errType := "api_error"
		if status == http.StatusBadRequest {
			errType = "invalid_request_error"
		}
		httpx.WriteJSON(w, status, map[string]any{
			"error": map[string]string{"message": err.Error(), "type": errType},
		})
		return
	}
	upResp, acc := call.result.Response, call.result.Account
	if upResp == nil || upResp.Body == nil {
		err := fmt.Errorf("upstream returned no response")
		requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, err.Error())
		httpx.WriteJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]string{"message": err.Error(), "type": "api_error"},
		})
		return
	}
	defer upResp.Body.Close()
	if isStream {
		serveResponsesStream(r.Context(), w, upResp, &reqLog, acc, func() (*http.Response, *domain.Account, error) {
			return call.Retry(r.Context(), "empty_stream")
		}, call.MarkCommitted)
		return
	}

	copyPassthroughHeaders(w, upResp.Header)
	var raw map[string]any
	if err := json.NewDecoder(upResp.Body).Decode(&raw); err != nil {
		requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, "decode response: "+err.Error())
		status := http.StatusInternalServerError
		if call.result.Serving.ProviderID == "opencode" || reqLog.RequestedModel != "" {
			status = http.StatusBadGateway
		}
		httpx.WriteJSON(w, status, map[string]any{
			"error": map[string]string{"message": err.Error(), "type": "parse_error"},
		})
		return
	}
	out := NormalizeOpenAIResponse(unwrapDataEnvelope(raw))
	restoreToolCallsInResponse(out, reqLog.ToolNames)
	usage := ParseTokenUsage(out["usage"])
	recordUsage(acc, upResp, &reqLog, reqLog.Model, usage)
	requestlog.Finalize(&reqLog, usage, time.Time{}, reqLog.StartedAt, true, "")
	httpx.WriteJSON(w, http.StatusOK, chatToResponses(out))
}

// unwrapDataEnvelope 剥掉部分上游返回的 {data:{...}} 信封。
func unwrapDataEnvelope(obj map[string]any) map[string]any {
	return openaichat.UnwrapDataEnvelope(obj)
}
