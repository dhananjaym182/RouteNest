package proxycore

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"routenest/internal/compaction"
	"routenest/internal/config"
	"routenest/internal/domain"
	"routenest/internal/httpx"
	"routenest/internal/requestlog"
	"routenest/internal/routing"
	"routenest/internal/zen"
	"strings"
	"time"
)

func unifiedDispatchHTTPStatus(model string, err error) int {
	if providerUnavailable(err) {
		return http.StatusServiceUnavailable
	}
	message := err.Error()
	if strings.Contains(message, " is disabled") || strings.Contains(message, "paid opencode model") || strings.Contains(message, "provider pin") {
		return http.StatusBadRequest
	}
	if zen.RouteModel(model) == "zen" {
		return http.StatusBadGateway
	}
	return clineErrorHTTPStatus(err)
}

// newChatCompletionsHandler returns the handler for POST /v1/chat/completions.
// Auth and the body limit are applied at registration; unified planning owns
// provider and credential eligibility.
func newChatCompletionsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]string{"message": err.Error(), "type": "parse_error"},
			})
			return
		}

		var params map[string]any
		if err := json.Unmarshal(body, &params); err != nil {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
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
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
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
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]string{"message": msg, "type": "invalid_request_error"},
			})
			return
		}

		reqLog := domain.RequestLog{StartedAt: time.Now(), Protocol: "openai", Model: model, Stream: isStream}

		// Free-only routing rejects models whose route access is not verified
		// free (or free_tier with an active allowance).
		if msg := legacyIngressFreeOnlyRejection(model); msg != "" {
			requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, msg)
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
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

		sessionID := compaction.RequestSessionID(params, r.Header)
		call, err := startUnifiedDispatch(r.Context(), params, isStream, "chat", sessionID, &reqLog)
		if err != nil {
			log.Printf("  api error: %v", err)
			requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, err.Error())
			status := unifiedDispatchHTTPStatus(model, err)
			errType := "api_error"
			if status == http.StatusBadRequest {
				errType = "invalid_request_error"
			}
			httpx.WriteJSON(w, status, map[string]any{
				"error": map[string]string{"message": err.Error(), "type": errType},
			})
			return
		}
		resp, acc := call.result.Response, call.result.Account
		if resp == nil || resp.Body == nil {
			err := fmt.Errorf("upstream returned no response")
			requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, err.Error())
			httpx.WriteJSON(w, http.StatusBadGateway, map[string]any{
				"error": map[string]string{"message": err.Error(), "type": "api_error"},
			})
			return
		}
		defer resp.Body.Close()
		if isStream {
			serveChatStream(r.Context(), w, resp, acc, &reqLog, func() (*http.Response, *domain.Account, error) {
				return call.Retry(r.Context(), "empty_stream")
			}, call.MarkCommitted)
			return
		}
		handleNonStreamResponse(w, resp, acc, &reqLog)
	}
}
