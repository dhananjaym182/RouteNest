package routing

import (
	"fmt"
	"log"
	"time"

	"routenest/internal/protocol/openaichat"
)

var PassThroughKeys = []string{
	"tools", "tool_choice", "parallel_tool_calls", "functions", "function_call",
	"temperature", "top_p", "top_k", "stop", "presence_penalty", "frequency_penalty",
	"response_format", "user", "n", "logit_bias", "seed", "logprobs", "top_logprobs",
	"stream_options", "metadata",
}

func BuildUpstreamBody(params map[string]any, stream bool) map[string]any {
	sessionID := fmt.Sprintf("sess_%d", time.Now().UnixMilli())

	maxTokens := MaxOutputTokens()
	source := ""
	if mt, ok := openaichat.NumericInt(params["max_tokens"]); ok {
		maxTokens, source = ClampMaxTokens(mt), "max_tokens"
	} else if mt, ok := openaichat.NumericInt(params["max_completion_tokens"]); ok {
		maxTokens, source = ClampMaxTokens(mt), "max_completion_tokens"
	}

	if maxTokens < MinUpstreamMaxTokens {
		if source != "" {
			log.Printf("  clamp %s=%d -> %d (upstream requires >= %d)", source, maxTokens, MaxOutputTokens(), MinUpstreamMaxTokens)
		}
		maxTokens = MaxOutputTokens()
	}

	model := DefaultModel()
	if m, ok := params["model"].(string); ok && m != "" {
		model = m
	}

	if limit := ModelMaxOutputLimit(model); limit > 0 && maxTokens > limit {
		log.Printf("  clamp max_tokens=%d -> %d (model %q output limit)", maxTokens, limit, model)
		maxTokens = limit
	}

	body := map[string]any{
		"model":            model,
		"max_tokens":       maxTokens,
		"session_id":       sessionID,
		"reasoning_effort": ReasoningEffort(),
	}

	if msgsRaw, ok := params["messages"]; ok {
		if msgsArr, ok := msgsRaw.([]any); ok {
			body["messages"] = openaichat.SanitizeMessages(msgsArr)
		} else {
			body["messages"] = msgsRaw
		}
	}

	if stream {
		body["stream"] = true
	}

	if re, ok := params["reasoning_effort"].(string); ok && re != "" {
		body["reasoning_effort"] = re
	} else if re, ok := params["ReasoningEffort"].(string); ok && re != "" {
		body["reasoning_effort"] = re
	}

	if re, _ := body["reasoning_effort"].(string); re == "none" || re == "" {
		delete(body, "reasoning_effort")
	}

	for _, key := range PassThroughKeys {
		if val, ok := params[key]; ok {
			body[key] = val
		}
	}

	openaichat.SanitizeTools(body)

	return body
}

// ServedByParam 是 CallClineAPI 族回写在 params 上的「实际服务方」标记：
// 自定义 provider 成功服务时写入 zen.UpstreamProvider。仅用于请求日志归因——
// BuildUpstreamBody / providers.Call / zen.Call 只拷贝白名单键，不会外发。
const ServedByParam = "__served_by"

// ProviderNameParam 与 ServedByParam 配套：自定义 provider 成功服务时回写实际
// 命中之家的显示名，供 stampUpstream 归因到请求日志的 providerName 字段。
const ProviderNameParam = "__served_by_name"

// WithModel 返回带指定模型名的参数副本（避免污染调用方 map）。
func WithModel(params map[string]any, model string) map[string]any {
	cp := make(map[string]any, len(params)+1)
	for k, v := range params {
		cp[k] = v
	}
	cp["model"] = model
	return cp
}

func getMsgCount(params map[string]any) int {
	if msgs, ok := params["messages"].([]any); ok {
		return len(msgs)
	}
	return 0
}
