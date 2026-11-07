package proxycore

import (
	"context"
	"encoding/json"
	"net/http"
	"routenest/internal/domain"
	"routenest/internal/protocol"
)

// chatChunkHasOutput reports whether a chat.completion chunk carries generated
// content (text, reasoning, or tool calls) that a retry would duplicate.
func chatChunkHasOutput(obj map[string]any) bool {
	if hasFirstOutput(obj) {
		return true
	}
	if delta, ok := protocol.GetNested(obj, "choices", 0, "delta").(map[string]any); ok {
		if r, _ := delta["reasoning_content"].(string); r != "" {
			return true
		}
	}
	if msg, ok := protocol.GetNested(obj, "choices", 0, "message").(map[string]any); ok {
		if r, _ := msg["reasoning_content"].(string); r != "" {
			return true
		}
	}
	return false
}

// chatChunkFinishReason returns the first non-empty finish_reason in a chunk.
func chatChunkFinishReason(obj map[string]any) string {
	choices, _ := protocol.GetNested(obj, "choices").([]any)
	for _, c := range choices {
		cm, _ := c.(map[string]any)
		if cm == nil {
			continue
		}
		if fr, _ := cm["finish_reason"].(string); fr != "" {
			return fr
		}
	}
	return ""
}

// emitChatStreamError reports a post-commit failure in-band. The client sees an
// error object instead of a silent clean stop, and no [DONE] is sent.
func emitChatStreamError(w http.ResponseWriter, message string) {
	payload, _ := json.Marshal(map[string]any{
		"error": map[string]string{"message": message, "type": "api_error"},
	})
	_ = newSSESink(w).write("data: " + string(payload) + "\n\n")
}

// serveChatStream forwards a chat SSE stream with bootstrap retries: an
// attempt that ends without any semantic content (upstream break, empty
// completion, first-content stall) discards its buffered frames and retries
// through the retry callback up to Stream.BootstrapRetries times (default 1,
// the historic behavior). A nil retry means the caller has no failover path;
// the failure is then reported in-band.
func serveChatStream(ctx context.Context, w http.ResponseWriter, resp *http.Response, acc *domain.Account, reqLog *domain.RequestLog, retry func() (*http.Response, *domain.Account, error), onCommit ...func()) {
	copyPassthroughHeaders(w, resp.Header)
	newSSESink(w).commitSSEHeaders()
	run := func(ctx context.Context, bw *bootstrapBuffer, current *http.Response, currentAcc *domain.Account) streamResult {
		return handleStreamResponse(ctx, bw, current, currentAcc, reqLog)
	}
	serveWithBootstrap(ctx, w, resp, acc, reqLog, retry, "chat stream",
		"upstream returned an empty stream", emitChatStreamError, run, firstCallback(onCommit))
}
