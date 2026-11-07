package proxycore

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"routenest/internal/compaction"
	"routenest/internal/domain"
	"routenest/internal/httpx"
	anthropicconv "routenest/internal/protocol/anthropic"
	"routenest/internal/protocol/openaichat"
	"routenest/internal/requestlog"
	"routenest/internal/routing"
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

func anthropicDispatchError(model string, err error) (int, string) {
	status := unifiedDispatchHTTPStatus(model, err)
	if status == http.StatusBadRequest {
		return status, "invalid_request_error"
	}
	return status, "api_error"
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
	if msg := legacyIngressFreeOnlyRejection(req.Model); msg != "" {
		requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, msg)
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"message": msg, "type": "invalid_request_error"},
		})
		return
	}

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

	if req.Model != "" && routing.IsModelDisabled(req.Model) {
		msg := fmt.Sprintf("model %q is disabled", req.Model)
		requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, msg)
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"message": msg, "type": "invalid_request_error"},
		})
		return
	}

	sessionID := compaction.RequestSessionID(map[string]any{"session_id": r.Header.Get("x-opencode-session")}, nil)
	call, err := startUnifiedDispatch(r.Context(), openAIReq, req.Stream, "anthropic", sessionID, &reqLog)
	if err != nil {
		status, errorType := anthropicDispatchError(req.Model, err)
		log.Printf("  anthropic api error: %v", err)
		requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, err.Error())
		httpx.WriteJSON(w, status, map[string]any{
			"error": map[string]string{"message": err.Error(), "type": errorType},
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
	copyPassthroughHeaders(w, resp.Header)

	if req.Stream {
		streamUnifiedAnthropic(r.Context(), w, call, resp, acc, &reqLog, cmCompactionSummary, cmApplied)
		return
	}

	defer resp.Body.Close()
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		requestlog.Finalize(&reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, "decode response: "+err.Error())
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]string{"message": err.Error(), "type": "parse_error"},
		})
		return
	}
	out := NormalizeOpenAIResponse(unwrapDataEnvelope(raw))
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
