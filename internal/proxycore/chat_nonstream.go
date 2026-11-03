package proxycore

import (
	"encoding/json"
	"log"
	"net/http"
	"routenest/internal/domain"
	"routenest/internal/httpx"
	"routenest/internal/protocol"
	"routenest/internal/requestlog"
	"time"
)

func handleNonStreamResponse(w http.ResponseWriter, upstream *http.Response, acc *domain.Account, reqLog *domain.RequestLog) {
	copyPassthroughHeaders(w, upstream.Header)
	var raw map[string]any
	if err := json.NewDecoder(upstream.Body).Decode(&raw); err != nil {
		requestlog.Finalize(reqLog, TokenUsage{}, time.Time{}, reqLog.StartedAt, false, "decode response: "+err.Error())
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]string{"message": err.Error(), "type": "parse_error"},
		})
		return
	}

	// Some Cline responses wrap in {data: {...}}
	out := raw
	if data, ok := raw["data"]; ok {
		if d, ok := data.(map[string]any); ok {
			out = d
		}
	}

	out = NormalizeOpenAIResponse(out)
	restoreToolCallsInResponse(out, reqLog.ToolNames)
	usage := ParseTokenUsage(out["usage"])
	recordUsage(acc, upstream, reqLog, reqLog.Model, usage)
	requestlog.Finalize(reqLog, usage, time.Time{}, reqLog.StartedAt, true, "")

	if msg, ok := protocol.GetNested(out, "choices", 0, "message").(map[string]any); ok {
		tc, _ := msg["tool_calls"].([]any)
		content, _ := msg["content"].(string)
		log.Printf("  nonstream finish=%v tool_calls=%d content_len=%d",
			protocol.GetNested(out, "choices", 0, "finish_reason"),
			len(tc), len(content))
	}

	httpx.WriteJSON(w, http.StatusOK, out)
}
