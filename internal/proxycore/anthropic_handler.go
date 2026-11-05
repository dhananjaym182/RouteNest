package proxycore

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"routenest/internal/accounts"
	"routenest/internal/combos"
	"routenest/internal/compaction"
	"routenest/internal/domain"
	"routenest/internal/httpx"
	anthropicconv "routenest/internal/protocol/anthropic"
	"routenest/internal/protocol/openaichat"
	"routenest/internal/requestlog"
	"routenest/internal/routing"
	"routenest/internal/zen"
	"time"
)

// AnthropicReq is the Anthropic Messages request body. The type lives in
// protocol/anthropic; the alias keeps the proxycore call sites unchanged.
type AnthropicReq = anthropicconv.MessagesRequest

type anthropicMsg = anthropicconv.MessagesMessage

func AnthropicToolsToOpenAI(tools []any) []any {
	return anthropicconv.ToolsToOpenAI(tools)
}

// AnthropicToOpenAI converts an Anthropic Messages request to OpenAI form, using
// the same options as the compaction path (see compaction.AnthropicOptions).
func AnthropicToOpenAI(req AnthropicReq) map[string]any {
	return anthropicconv.MessagesToOpenAI(req, compaction.AnthropicOptions())
}

func openAIToAnthropic(openAI map[string]any) map[string]any {
	return anthropicconv.FromOpenAI(openAI, anthropicconv.Options{NewToolUseID: openaichat.GenToolUseID})
}

func handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"message": err.Error(), "type": "parse_error"},
		})
		return
	}

	var req AnthropicReq
	if err := json.Unmarshal(body, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"message": err.Error(), "type": "parse_error"},
		})
		return
	}

	if len(req.Messages) == 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"message": "messages is required", "type": "parse_error"},
		})
		return
	}

	req.MaxTokens = routing.ClampMaxTokens(req.MaxTokens)

	openAIReq := AnthropicToOpenAI(req)

	log.Printf("  anthropic: model=%s stream=%v msgs=%d", req.Model, req.Stream, len(req.Messages))

	reqLog := domain.RequestLog{StartedAt: time.Now(), Protocol: "anthropic", Model: req.Model, Stream: req.Stream}

	// Administrator model policy: alias rewrite + wildcard exclusions.
	if resolved, reject := routing.ResolveClientModel(req.Model); reject != "" {
		requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, reject)
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"message": reject, "type": "invalid_request_error"},
		})
		return
	} else if resolved != req.Model {
		req.Model = resolved
		reqLog.Model = resolved
		openAIReq["model"] = resolved
	}

	if msg := routing.FreeOnlyRejection(req.Model); msg != "" {
		requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, msg)
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"message": msg, "type": "invalid_request_error"},
		})
		return
	}

	// Context management (Anthropic context_management edits) — polyfill.
	// Inspected before routing so compaction can rewrite openAIReq["messages"].
	var cmResult *compaction.CMResult
	cmPaused := false
	var cmCompactionSummary string
	var cmApplied []compaction.CMAppliedEdit
	if len(req.ContextManagement) > 0 {
		res, err := compaction.ApplyContextManagement(r.Context(), req.Model, req.ContextManagement, openAIReq)
		if err != nil {
			msg := err.Error()
			requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, msg)
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"message": msg, "type": "invalid_request_error"}})
			return
		}
		cmResult = res
		if res != nil {
			if res.AppliedEdits != nil {
				cmApplied = res.AppliedEdits
			} else {
				cmApplied = []compaction.CMAppliedEdit{}
			}
			if res.Compacted {
				cmCompactionSummary = res.Summary
				cmPaused = res.Paused
			}
		}
	}

	// Paused compaction: return compaction-only response, no upstream call.
	if cmPaused {
		if req.Stream {
			sw := NewSSEWriter(w)
			sw.Start()
			msgID := "msg_" + fmt.Sprintf("%x", reqLog.StartedAt.UnixMilli())
			emit := func(event string, data any) {
				d, _ := json.Marshal(data)
				sw.Write([]byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event, string(d))))
			}
			if !sw.Envelope {
				sw.Envelope = true
				emit("message_start", map[string]any{
					"type": "message_start",
					"message": map[string]any{
						"id": msgID, "type": "message", "role": "assistant",
						"content": []any{}, "model": req.Model, "stop_reason": nil,
					},
				})
			}
			sw.Write([]byte(": ping\n\n"))
			if !sw.CompactionEmitted {
				sw.CompactionEmitted = true
				compaction.EmitCompactionBlock(sw, cmCompactionSummary)
			}
			emit("message_delta", map[string]any{
				"type":               "message_delta",
				"delta":              map[string]any{"stop_reason": "compaction", "stop_sequence": nil},
				"usage":              map[string]any{"output_tokens": 0},
				"context_management": compaction.ContextManagementResponse(cmApplied),
			})
			emit("message_stop", map[string]any{"type": "message_stop"})
			requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, true, "")
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"id":                 "msg_" + fmt.Sprintf("%x", reqLog.StartedAt.UnixMilli()),
			"type":               "message",
			"role":               "assistant",
			"content":            []any{map[string]any{"type": "compaction", "content": cmCompactionSummary}},
			"model":              req.Model,
			"stop_reason":        "compaction",
			"usage":              map[string]any{"input_tokens": 0, "output_tokens": 0},
			"context_management": compaction.ContextManagementResponse(cmApplied),
		})
		requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, true, "")
		return
	}

	// 管理员手动停用的模型在入口直接拒绝（不进列表，也不可点名调用）
	if req.Model != "" && routing.IsModelDisabled(req.Model) {
		msg := fmt.Sprintf("model %q is disabled", req.Model)
		requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, msg)
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"message": msg, "type": "invalid_request_error"},
		})
		return
	}

	// 模型组合：在普通路由之前分流（组合 ID 不是真实上游模型）。按配置顺序回退，
	// 默认不触发全局 ModelChain / free 链。
	if combo, ok := combos.FindCombo(req.Model); ok {
		if !combo.Enabled {
			msg := fmt.Sprintf("model combo %q is disabled", req.Model)
			requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, msg)
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]string{"message": msg, "type": "invalid_request_error"},
			})
			return
		}
		sessionID := compaction.RequestSessionID(map[string]any{"session_id": r.Header.Get("x-opencode-session")}, nil)
		reqLog.ToolNames = clampParamsToolNames(openAIReq)

		if req.Stream {
			StreamAnthropicCombo(r.Context(), w, openAIReq, combo, &reqLog, sessionID, cmCompactionSummary, cmApplied)
			return
		}

		run := combos.RunCombo(openAIReq, combo, false, sessionID)
		combos.ApplyTrace(&reqLog, combo, run)
		stampUpstream(&reqLog, openAIReq)
		if run.Acc != nil {
			reqLog.AccountID = run.Acc.AccountID
			reqLog.AccountEmail = run.Acc.Email
		}
		if run.Err != nil {
			log.Printf("  anthropic combo %q error: %v", combo.ID, run.Err)
			msg := combos.TraceSummary(combo, run)
			requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, msg)
			httpx.WriteJSON(w, clineErrorHTTPStatus(run.Err), map[string]any{
				"error": map[string]string{"message": msg, "type": "api_error"},
			})
			return
		}
		defer run.Resp.Body.Close()
		copyPassthroughHeaders(w, run.Resp.Header)
		var raw map[string]any
		if err := json.NewDecoder(run.Resp.Body).Decode(&raw); err != nil {
			requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, "decode response: "+err.Error())
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"error": map[string]string{"message": err.Error(), "type": "parse_error"},
			})
			return
		}
		out := NormalizeOpenAIResponse(unwrapDataEnvelope(raw))
		usage := ParseTokenUsage(out["usage"])
		recordUsage(run.Acc, run.Resp, &reqLog, reqLog.Model, usage)
		requestlog.Finalize(&reqLog, usage, time.Time{}, reqLog.StartedAt, true, "")
		restoreToolCallsInResponse(out, reqLog.ToolNames)
		anthropicResp := openAIToAnthropic(out)
		compaction.ApplyCMToResponse(anthropicResp, cmResult)
		if openaichat.HasToolUseBlocks(anthropicResp["content"]) {
			anthropicResp["stop_reason"] = "tool_use"
		}
		httpx.WriteJSON(w, http.StatusOK, anthropicResp)
		return
	}

	// 按 model 自动分流（与 chat 端点一致）：zen 免费/付费拒绝/Cline 池
	switch zen.RouteModel(req.Model) {
	case "reject":
		msg := fmt.Sprintf("model %q is a paid opencode model; only free models are proxied", req.Model)
		requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, msg)
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"message": msg, "type": "invalid_request_error"},
		})
		return
	case "zen":
		reqLog.Upstream = zen.UpstreamOpenCode
		zm, _ := zen.ResolveInfo(req.Model)
		out := compaction.MaybeCompact(openAIReq, zm, compaction.RequestSessionID(map[string]any{"session_id": r.Header.Get("x-opencode-session")}, nil))
		if out.Changed {
			log.Printf("  anthropic %s", out.Note)
		}
		resp, err := zen.Call(openAIReq, req.Stream)
		// 上游返回「上下文超限」400：压缩后重试一次
		if err != nil && routing.IsContextLengthError(err) {
			log.Printf("  anthropic: context length exceeded, compacting and retrying")
			out2 := compaction.FallbackTruncate(openAIReq, zm)
			if out2.Changed {
				log.Printf("  anthropic %s", out2.Note)
				resp, err = zen.Call(openAIReq, req.Stream)
			}
		}
		if err != nil {
			// zen 故障转移到 Cline 池：先截短超长工具名（Meta 后端硬限 ≤64）
			reqLog.ToolNames = clampParamsToolNames(openAIReq)
			if fbResp, fbAcc, fbErr, attempted := routing.ZenFailoverToCline(openAIReq, req.Stream); attempted {
				if fbErr == nil {
					log.Printf("  anthropic failover: serving %q via cline pool", req.Model)
					stampUpstream(&reqLog, openAIReq) // 归因实际服务方（provider / cline）
					if fm, ok := openAIReq["model"].(string); ok && fm != "" {
						reqLog.Model = fm // zen 故障转移后记录实际服务模型
					}
					if fbAcc != nil {
						reqLog.AccountID = fbAcc.AccountID
						reqLog.AccountEmail = fbAcc.Email
					}
					defer fbResp.Body.Close()
					copyPassthroughHeaders(w, fbResp.Header)
					if req.Stream {
						sw := NewSSEWriter(w)
						// 这条降级路径没有重试，所以用宽容的上限，尽量把慢但能出的流交给客户端。
						if !HandleAnthropicStream(r.Context(), sw, fbResp, fbAcc, &reqLog, lastAttemptFirstContentTimeout, cmCompactionSummary, cmApplied) {
							log.Printf("  anthropic stream: failover upstream failed before any content")
							finishAnthropicStreamFailure(sw, w, &reqLog, http.StatusBadGateway,
								"stream failed before first token", "upstream stream failed before first token")
						}
					} else {
						var raw map[string]any
						if err := json.NewDecoder(fbResp.Body).Decode(&raw); err != nil {
							requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, "decode response: "+err.Error())
							httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
								"error": map[string]string{"message": err.Error(), "type": "parse_error"},
							})
							return
						}
						out2 := NormalizeOpenAIResponse(unwrapDataEnvelope(raw))
						usage := ParseTokenUsage(out2["usage"])
						recordUsage(fbAcc, fbResp, &reqLog, reqLog.Model, usage)
						requestlog.Finalize(&reqLog, usage, time.Time{}, reqLog.StartedAt, true, "")
						restoreToolCallsInResponse(out2, reqLog.ToolNames)
						anthropicResp := openAIToAnthropic(out2)
						compaction.ApplyCMToResponse(anthropicResp, cmResult)
						if openaichat.HasToolUseBlocks(anthropicResp["content"]) {
							anthropicResp["stop_reason"] = "tool_use"
						}
						httpx.WriteJSON(w, http.StatusOK, anthropicResp)
					}
					return
				}
				// 兜底也失败：zen 的真实失败原因不能被吞 —— 最终错误带上两段，
				// 日志/UI 能直接看到 zen 为什么挂、Cline 兜底为什么也没接住
				err = fmt.Errorf("zen: %v; cline free chain: %w", err, fbErr)
			}
			log.Printf("  anthropic api error: %v", err)
			requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, err.Error())
			httpx.WriteJSON(w, http.StatusBadGateway, map[string]any{
				"error": map[string]string{"message": err.Error(), "type": "api_error"},
			})
			return
		}
		defer resp.Body.Close()
		copyPassthroughHeaders(w, resp.Header)
		if req.Stream {
			sw := NewSSEWriter(w)
			// 同样是单次尝试（无重试），用宽容的上限。
			if !HandleAnthropicStream(r.Context(), sw, resp, nil, &reqLog, lastAttemptFirstContentTimeout, cmCompactionSummary, cmApplied) {
				log.Printf("  anthropic stream: zen upstream failed before any content")
				finishAnthropicStreamFailure(sw, w, &reqLog, http.StatusBadGateway,
					"stream failed before first token", "zen stream failed before first token")
			}
		} else {
			var raw map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
				requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, "decode response: "+err.Error())
				httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
					"error": map[string]string{"message": err.Error(), "type": "parse_error"},
				})
				return
			}
			out2 := NormalizeOpenAIResponse(unwrapDataEnvelope(raw))
			usage := ParseTokenUsage(out2["usage"])
			requestlog.Finalize(&reqLog, usage, time.Time{}, reqLog.StartedAt, true, "")
			anthropicResp := openAIToAnthropic(out2)
			compaction.ApplyCMToResponse(anthropicResp, cmResult)
			if openaichat.HasToolUseBlocks(anthropicResp["content"]) {
				anthropicResp["stop_reason"] = "tool_use"
			}
			httpx.WriteJSON(w, http.StatusOK, anthropicResp)
		}
		return
	}

	activeCount := 0
	p := accounts.LoadPool()
	for _, a := range p.Accounts {
		if a.Status == "active" {
			activeCount++
		}
	}

	if activeCount == 0 && len(p.Accounts) == 0 {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]string{
				"message": "No accounts in pool",
				"type":    "auth_error",
			},
		})
		return
	}

	// Cline 池 / provider 路由也要主动压缩：此前仅 zen 路径会压，大上下文
	// 请求会直接撞上游窗口失败。
	zm := compaction.ModelForCompaction(req.Model)
	compaction.CompactForRequest(openAIReq, zm, compaction.RequestSessionID(map[string]any{"session_id": r.Header.Get("x-opencode-session")}, nil), "anthropic")

	if req.Stream {
		contextRetried := false
		streamAnthropicWithRetry(r.Context(), w, func() (*http.Response, *domain.Account, error) {
			resp, acc, err := routing.CallClineAPI(openAIReq, req.Stream)
			if err != nil && !contextRetried && routing.IsContextLengthError(err) {
				contextRetried = true
				if out := compaction.FallbackTruncate(openAIReq, zm); out.Changed {
					log.Printf("  anthropic %s", out.Note)
					resp, acc, err = routing.CallClineAPI(openAIReq, req.Stream)
				}
			}
			if err == nil {
				if effectiveModel, ok := openAIReq["model"].(string); ok && effectiveModel != "" {
					reqLog.Model = effectiveModel // 含回退后的实际服务模型
				}
				stampUpstream(&reqLog, openAIReq) // 归因实际服务方（provider / opencode / cline）
			}
			return resp, acc, err
		}, &reqLog, cmCompactionSummary, cmApplied)
		return
	}

	// Cline 通道：截短超长工具名并记录映射，回程由响应处理器还原（Meta 后端硬限 ≤64）
	reqLog.ToolNames = clampParamsToolNames(openAIReq)
	resp, acc, err := routing.CallClineAPI(openAIReq, req.Stream)
	if err != nil && routing.IsContextLengthError(err) {
		if out := compaction.FallbackTruncate(openAIReq, zm); out.Changed {
			log.Printf("  anthropic %s", out.Note)
			resp, acc, err = routing.CallClineAPI(openAIReq, req.Stream)
		}
	}
	if effectiveModel, ok := openAIReq["model"].(string); ok && effectiveModel != "" {
		reqLog.Model = effectiveModel // 含回退后的实际服务模型
	}
	stampUpstream(&reqLog, openAIReq) // 归因实际服务方（provider / opencode / cline）
	if acc != nil {
		reqLog.AccountID = acc.AccountID
		reqLog.AccountEmail = acc.Email // 失败也要记账号
	}
	if err != nil {
		log.Printf("  anthropic api error: %v", err)
		requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, err.Error())
		httpx.WriteJSON(w, clineErrorHTTPStatus(err), map[string]any{
			"error": map[string]string{"message": err.Error(), "type": "api_error"},
		})
		return
	}
	defer resp.Body.Close()
	copyPassthroughHeaders(w, resp.Header)
	if acc != nil {
		reqLog.AccountID = acc.AccountID
		reqLog.AccountEmail = acc.Email
	}

	{
		var raw map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, "decode response: "+err.Error())
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"error": map[string]string{"message": err.Error(), "type": "parse_error"},
			})
			return
		}
		out := raw
		if data, ok := raw["data"]; ok {
			if d, ok := data.(map[string]any); ok {
				out = d
			}
		}
		out = NormalizeOpenAIResponse(out)
		usage := ParseTokenUsage(out["usage"])
		recordUsage(acc, resp, &reqLog, reqLog.Model, usage)
		requestlog.Finalize(&reqLog, usage, time.Time{}, reqLog.StartedAt, true, "")
		restoreToolCallsInResponse(out, reqLog.ToolNames)
		anthropicResp := openAIToAnthropic(out)

		compaction.ApplyCMToResponse(anthropicResp, cmResult)
		if openaichat.HasToolUseBlocks(anthropicResp["content"]) {
			anthropicResp["stop_reason"] = "tool_use"
		}

		httpx.WriteJSON(w, http.StatusOK, anthropicResp)
	}
}
