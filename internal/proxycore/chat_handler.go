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
	"routenest/internal/config"
	"routenest/internal/domain"
	"routenest/internal/requestlog"
	"routenest/internal/routing"
	"routenest/internal/zen"
	"time"
)

// newChatCompletionsHandler returns the handler for POST /v1/chat/completions.
// activeAccounts is the startup count of enabled accounts; Start records it once,
// before any request is served. Auth and the body limit are applied at registration.
func newChatCompletionsHandler(activeAccounts int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]string{"message": err.Error(), "type": "parse_error"},
			})
			return
		}

		var params map[string]any
		if err := json.Unmarshal(body, &params); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]string{"message": err.Error(), "type": "parse_error"},
			})
			return
		}

		isStream, _ := params["stream"].(bool)
		toolCount := 0
		if tools, ok := params["tools"]; ok {
			if t, ok := tools.([]any); ok {
				toolCount = len(t)
			}
		}
		model, _ := params["model"].(string)
		log.Printf("  client: stream=%v tools=%d model=%s", isStream, toolCount, model)

		// Administrator model policy: alias rewrite + wildcard exclusions,
		// enforced before routing and before the disabled check.
		if resolved, reject := routing.ResolveClientModel(model); reject != "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]string{"message": reject, "type": "invalid_request_error"},
			})
			return
		} else if resolved != model {
			model = resolved
			params["model"] = resolved
		}
		routing.ApplyThinkingPolicy(params, model)

		// 管理员手动停用的模型在入口直接拒绝（不进列表，也不可点名调用）
		if model != "" && routing.IsModelDisabled(model) {
			msg := "model " + model + " is disabled"
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]string{"message": msg, "type": "invalid_request_error"},
			})
			return
		}

		reqLog := domain.RequestLog{StartedAt: time.Now(), Protocol: "openai", Model: model, Stream: isStream}

		// Free-only routing rejects models whose route access is not verified
		// free (or free_tier with an active allowance).
		if msg := routing.FreeOnlyRejection(model); msg != "" {
			requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, msg)
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]string{"message": msg, "type": "invalid_request_error"},
			})
			return
		}

		// Opt-in nonstream keepalive (NonstreamKeepaliveSeconds): a single
		// space every interval while the upstream call blocks, to outlive
		// byte-level read timeouts in intermediaries. The wrapped writer
		// serializes those writes with the eventual body write and stops the
		// keepalive once real bytes are committed, so the two never race.
		if !isStream {
			if kw := newKeepaliveWriter(w); kw != nil {
				w = kw
				defer kw.stop()
			}
		}

		// Override system prompt from override.md for OpenAI format
		if override := config.OverrideContent(); override != "" {
			if msgs, ok := params["messages"].([]any); ok {
				found := false
				for _, m := range msgs {
					if mm, ok := m.(map[string]any); ok {
						if mm["role"] == "system" {
							mm["content"] = override
							found = true
							break
						}
					}
				}
				if !found {
					params["messages"] = append([]any{map[string]any{"role": "system", "content": override}}, msgs...)
				}
			}
		}

		// 模型组合：具名有序回退。命中组合时在普通路由之前分流 —— 组合 ID 不是
		// 真实上游模型，必须在此拦截，按管理员配置的顺序逐个尝试；不会走全局
		// ModelChain / free 链（除非组合显式开启 emergencyFallback）。
		if combo, ok := combos.FindCombo(model); ok {
			if !combo.Enabled {
				msg := fmt.Sprintf("model combo %q is disabled", model)
				requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, msg)
				writeJSON(w, http.StatusBadRequest, map[string]any{
					"error": map[string]string{"message": msg, "type": "invalid_request_error"},
				})
				return
			}
			reqLog.ToolNames = clampParamsToolNames(params)
			run := combos.RunCombo(params, combo, isStream, compaction.RequestSessionID(params, r.Header))
			combos.ApplyTrace(&reqLog, combo, run)
			stampUpstream(&reqLog, params) // 归因实际服务方（provider / opencode / cline）
			if run.Acc != nil {
				reqLog.AccountID = run.Acc.AccountID
				reqLog.AccountEmail = run.Acc.Email
			}
			if run.Err != nil {
				log.Printf("  combo %q error: %v", combo.ID, run.Err)
				msg := combos.TraceSummary(combo, run)
				requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, msg)
				writeJSON(w, clineErrorHTTPStatus(run.Err), map[string]any{
					"error": map[string]string{"message": msg, "type": "api_error"},
				})
				return
			}
			defer run.Resp.Body.Close()
			// 非流式：完整按条目顺序回退。流式：仅在连接级回退（combos.RunCombo 在写任何
			// 字节给客户端前拿到 200 响应）；空流由统一契约补发 in-band 错误，不再
			// 伪装成正常结束（Anthropic 端点另有 StreamAnthropicCombo 的预提交回退）。
			if isStream {
				serveChatStream(r.Context(), w, run.Resp, run.Acc, &reqLog, nil)
			} else {
				handleNonStreamResponse(w, run.Resp, run.Acc, &reqLog)
			}
			return
		}

		// 按 model 自动分流：zen 免费模型 / zen 付费拒绝 / 其余走 Cline 池
		switch zen.RouteModel(model) {
		case "reject":
			msg := fmt.Sprintf("model %q is a paid opencode model; only free models are proxied", model)
			requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, msg)
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]string{"message": msg, "type": "invalid_request_error"},
			})
			return
		case "zen":
			reqLog.Upstream = zen.UpstreamOpenCode
			zm, _ := zen.ResolveInfo(model)
			out := compaction.MaybeCompact(params, zm, compaction.RequestSessionID(params, r.Header))
			if out.Changed {
				log.Printf("  chat %s", out.Note)
			}
			resp, err := zen.Call(params, isStream)
			// 上游返回「上下文超限」400：压缩后重试一次，避免 Claude Code 大上下文直接失败
			if err != nil && routing.IsContextLengthError(err) {
				log.Printf("  chat: context length exceeded, compacting and retrying")
				out2 := compaction.FallbackTruncate(params, zm)
				if out2.Changed {
					log.Printf("  chat %s", out2.Note)
					resp, err = zen.Call(params, isStream)
				}
			}
			if err != nil {
				// zen 故障转移到 Cline 池：先截短超长工具名（Meta 后端硬限 ≤64）
				reqLog.ToolNames = clampParamsToolNames(params)
				if fbResp, fbAcc, fbErr, attempted := routing.ZenFailoverToCline(params, isStream); attempted {
					if fbErr == nil {
						log.Printf("  chat failover: serving %q via cline pool", model)
						stampUpstream(&reqLog, params) // 归因实际服务方（provider / cline）
						if fbAcc != nil {
							reqLog.AccountID = fbAcc.AccountID
							reqLog.AccountEmail = fbAcc.Email
						}
						defer fbResp.Body.Close()
						if isStream {
							serveChatStream(r.Context(), w, fbResp, fbAcc, &reqLog, func() (*http.Response, *domain.Account, error) {
								rr, ra, rerr := routing.CallClineAPI(params, true)
								if rerr == nil {
									stampUpstream(&reqLog, params)
								}
								if m, ok := params["model"].(string); ok && m != "" {
									reqLog.Model = m
								}
								return rr, ra, rerr
							})
						} else {
							handleNonStreamResponse(w, fbResp, fbAcc, &reqLog)
						}
						return
					}
					// 兜底也失败：zen 的真实失败原因不能被吞 —— 最终错误带上两段，
					// 日志/UI 能直接看到 zen 为什么挂、Cline 兜底为什么也没接住
					err = fmt.Errorf("zen: %v; cline free chain: %w", err, fbErr)
				}
				log.Printf("  api error: %v", err)
				requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, err.Error())
				writeJSON(w, http.StatusBadGateway, map[string]any{
					"error": map[string]string{"message": err.Error(), "type": "api_error"},
				})
				return
			}
			defer resp.Body.Close()
			if isStream {
				serveChatStream(r.Context(), w, resp, nil, &reqLog, func() (*http.Response, *domain.Account, error) {
					fbResp, fbAcc, fbErr, attempted := routing.ZenFailoverToCline(params, true)
					if !attempted {
						return nil, nil, fmt.Errorf("no cline fallback available")
					}
					if fbErr == nil {
						stampUpstream(&reqLog, params)
						if m, ok := params["model"].(string); ok && m != "" {
							reqLog.Model = m
						}
					}
					return fbResp, fbAcc, fbErr
				})
			} else {
				handleNonStreamResponse(w, resp, nil, &reqLog)
			}
			return
		}

		// cline 池路径才要求账号；zen 免费模型不依赖本地账号池
		if activeAccounts == 0 && len(accounts.LoadPool().Accounts) == 0 {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": map[string]string{
					"message": "No accounts in accounts.Pool. Run with --add-account or POST /admin/login to add accounts.",
					"type":    "auth_error",
				},
			})
			return
		}

		// Cline 通道：截短超长工具名并记录映射，回程由响应处理器还原（Meta 后端硬限 ≤64）
		// 主动压缩同样适用于 cline/provider 路由 —— 此前只有 zen 路径会压缩，
		// 大上下文请求会直接撞上游窗口失败。
		zm := compaction.ModelForCompaction(model)
		compaction.CompactForRequest(params, zm, compaction.RequestSessionID(params, r.Header), "chat")
		reqLog.ToolNames = clampParamsToolNames(params)
		resp, acc, err := routing.CallClineAPI(params, isStream)
		if err != nil && routing.IsContextLengthError(err) {
			if out := compaction.FallbackTruncate(params, zm); out.Changed {
				log.Printf("  chat %s", out.Note)
				resp, acc, err = routing.CallClineAPI(params, isStream)
			}
		}
		if effectiveModel, ok := params["model"].(string); ok && effectiveModel != "" {
			reqLog.Model = effectiveModel // 含回退后的实际服务模型
		}
		stampUpstream(&reqLog, params) // 归因实际服务方（provider / opencode / cline）
		if acc != nil {
			reqLog.AccountID = acc.AccountID
			reqLog.AccountEmail = acc.Email // 失败也要记账号：否则日志显示不出实际被 402/429 的账号
		}
		if err != nil {
			log.Printf("  api error: %v", err)
			requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, err.Error())
			writeJSON(w, clineErrorHTTPStatus(err), map[string]any{
				"error": map[string]string{"message": err.Error(), "type": "api_error"},
			})
			return
		}
		defer resp.Body.Close()
		if acc != nil {
			reqLog.AccountID = acc.AccountID
			reqLog.AccountEmail = acc.Email
		}

		if isStream {
			serveChatStream(r.Context(), w, resp, acc, &reqLog, func() (*http.Response, *domain.Account, error) {
				rr, ra, rerr := routing.CallClineAPI(params, true)
				if rerr == nil {
					stampUpstream(&reqLog, params)
				}
				if m, ok := params["model"].(string); ok && m != "" {
					reqLog.Model = m
				}
				return rr, ra, rerr
			})
		} else {
			handleNonStreamResponse(w, resp, acc, &reqLog)
		}
	}
}
