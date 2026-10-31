package zen

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"routenest/internal/protocol/openaichat"
	responsesconv "routenest/internal/protocol/responses"
	zenupstream "routenest/internal/upstream/zen"
)

// ============ Responses 协议上游（muse-spark 系） ============
// 对齐 opencode 官方端点表（opencode.ai/docs/zen）：GPT/Grok/Muse Spark 走
// OpenAI Responses 协议（POST /responses），Claude/Qwen → Anthropic，其余 →
// chat/completions。当前免费层只有 muse-spark 系命中 Responses；
// 参考实现：sub2api DefaultOpenCodeZenProtocolRules。
// 上游统一流式（Responses SSE），下游要 chat SSE 时逐事件转写，要 JSON 时折叠。

// zenResponsesModelPrefixes 需要 Responses 协议的 zen 模型前缀（gpt-* / grok-* 预留）。
var zenResponsesModelPrefixes = []string{"muse-spark-"}

func zenModelUsesResponses(modelID string) bool {
	for _, p := range zenResponsesModelPrefixes {
		if strings.HasPrefix(modelID, p) {
			return true
		}
	}
	return false
}

// zenUpstreamModelID 返回请求模型对应的 zen 正式 ID（未解析到时去掉 opencode/ 前缀）。
func zenUpstreamModelID(params map[string]any) string {
	m, _ := params["model"].(string)
	if m == "" {
		return m
	}
	if zm, ok := ResolveInfo(m); ok {
		return zm.ID
	}
	return strings.TrimPrefix(m, "opencode/")
}

func zenUpstreamNeedsResponses(params map[string]any) bool {
	return zenModelUsesResponses(zenUpstreamModelID(params))
}

// buildZenResponsesBody 把下游 chat 参数构造成 Responses 请求体。
// 匿名免费层的 agent 形态要求（强制流式 + 核心工具）同样适用于该协议。
func buildZenResponsesBody(params map[string]any) map[string]any {
	opts := BodyOptionsProvider()
	body := map[string]any{"stream": true}
	body["model"] = zenUpstreamModelID(params)
	// max_tokens → max_output_tokens；Meta 系上游要求 >= 16，推理模型预算太小
	// 会全耗在 reasoning 上产出空内容，低于下限兜到默认值（同 buildUpstreamBody 约定）。
	// 客户端未指定时不带该字段，交由上游取模型默认。
	// chat 请求经 json.Unmarshal 是 float64，responses/anthropic 转换路径是 int，宽松读取。
	for _, k := range []string{"max_tokens", "max_completion_tokens"} {
		n := zenInt64(params[k])
		if n <= 0 {
			continue
		}
		if n < int64(opts.MinMaxTokens) {
			log.Printf("  zen clamp max_output_tokens=%d -> %d (upstream requires >= %d)", n, opts.DefaultMaxTokens, opts.MinMaxTokens)
			n = int64(opts.DefaultMaxTokens)
		}
		body["max_output_tokens"] = n
		break
	}
	if v, ok := params["temperature"]; ok {
		body["temperature"] = v
	}
	if v, ok := params["top_p"]; ok {
		body["top_p"] = v
	}

	// tools：先在 chat 嵌套形态上补齐核心工具（免费层 agent 形态校验对
	// 带 key 的请求同样生效），再转 Responses 扁平格式
	pseudo := map[string]any{}
	if raw, ok := params["tools"]; ok {
		pseudo["tools"] = raw
	}
	ensureAnonymousTools(pseudo)
	if rawTools, ok := pseudo["tools"].([]any); ok && len(rawTools) > 0 {
		flat := make([]any, 0, len(rawTools))
		for _, t := range rawTools {
			tm, ok := t.(map[string]any)
			if !ok {
				continue
			}
			fn, _ := tm["function"].(map[string]any)
			if fn == nil {
				continue
			}
			// description 仅在非空字符串时下发、parameters 仅在非 null 时下发：
			// 上游网关对 "description": null / "parameters": null 硬拒 400
			entry := map[string]any{"type": "function", "name": fn["name"]}
			if d, _ := fn["description"].(string); d != "" {
				entry["description"] = d
			}
			if fn["parameters"] != nil {
				entry["parameters"] = fn["parameters"]
			}
			flat = append(flat, entry)
		}
		if len(flat) > 0 {
			body["tools"] = flat
		}
	}
	switch tc := params["tool_choice"].(type) {
	case string:
		body["tool_choice"] = tc
	case map[string]any:
		fn, _ := tc["function"].(map[string]any)
		if name, _ := fn["name"].(string); name != "" {
			body["tool_choice"] = map[string]any{"type": "function", "name": name}
		}
	}

	// messages → instructions（system）+ input（对话与工具往返）
	var instr []string
	input := make([]any, 0, 16)
	if msgs, ok := params["messages"].([]any); ok {
		for _, m := range openaichat.SanitizeMessages(msgs) {
			mm, ok := m.(map[string]any)
			if !ok {
				continue
			}
			switch role, _ := mm["role"].(string); role {
			case "system", "developer":
				if s := responsesconv.StringifyContent(mm["content"]); s != "" {
					instr = append(instr, s)
				}
			case "tool":
				input = append(input, map[string]any{
					"type":    "function_call_output",
					"call_id": mm["tool_call_id"],
					"output":  responsesconv.StringifyContent(mm["content"]),
				})
			case "assistant":
				if tcs, ok := mm["tool_calls"].([]any); ok {
					for _, tc := range tcs {
						cm, ok := tc.(map[string]any)
						if !ok {
							continue
						}
						fn, _ := cm["function"].(map[string]any)
						if fn == nil {
							continue
						}
						args, _ := fn["arguments"].(string)
						if args == "" {
							if am, ok := fn["arguments"].(map[string]any); ok {
								if b, err := json.Marshal(am); err == nil {
									args = string(b)
								}
							}
						}
						input = append(input, map[string]any{
							"type":      "function_call",
							"call_id":   cm["id"],
							"name":      fn["name"],
							"arguments": args,
						})
					}
				}
				if s := responsesconv.StringifyContent(mm["content"]); s != "" {
					input = append(input, map[string]any{"type": "message", "role": "assistant", "content": s})
				}
			default: // user
				input = append(input, map[string]any{
					"type":    "message",
					"role":    "user",
					"content": zenChatContentToResponsesInput(mm["content"]),
				})
			}
		}
	}
	instrText := strings.Join(instr, "\n\n")
	// 免费层 agent 形态校验要求 system 提示里带 CLI 的 <env> 环境块；与 chat
	// 路径一致，缺失时补上，已有则不动。
	if !strings.Contains(instrText, "<env>") {
		env := zenupstream.EnvBlock()
		if instrText == "" {
			instrText = env
		} else {
			instrText += "\n\n" + env
		}
	}
	body["instructions"] = instrText
	body["input"] = input
	// 定稿前清洗 tools（扁平形态）：null 可选字段会被上游网关硬拒 400
	openaichat.SanitizeTools(body)
	return body
}

// zenChatContentToResponsesInput 把 chat content 转成 Responses 输入内容：
// 字符串原样；部件数组映射 text → input_text、image_url → input_image。
func zenChatContentToResponsesInput(content any) any {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		parts := make([]any, 0, len(v))
		for _, p := range v {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			switch pm["type"] {
			case "text":
				if t, _ := pm["text"].(string); t != "" {
					parts = append(parts, map[string]any{"type": "input_text", "text": t})
				}
			case "image_url":
				iu, _ := pm["image_url"].(map[string]any)
				if u, _ := iu["url"].(string); u != "" {
					parts = append(parts, map[string]any{"type": "input_image", "image_url": u})
				}
			}
		}
		if len(parts) > 0 {
			return parts
		}
	}
	return ""
}

// zenInt64 coerces a JSON-decoded numeric value to int64.
func zenInt64(v any) int64 { return zenupstream.Int64(v) }

// zenCollapseAcc / zenCollapseTool are the shared collapsed-response shapes.
type zenCollapseAcc = zenupstream.CollapseAcc
type zenCollapseTool = zenupstream.CollapseTool

// zenCollapseToChatResponse renders a collapsed accumulator as one chat.completion.
func zenCollapseToChatResponse(acc *zenCollapseAcc, req *http.Request) (*http.Response, error) {
	return zenupstream.CollapseToChatResponse(acc, req)
}

// streamZenResponsesAsChat rewrites upstream Responses SSE into chat chunk SSE.
func streamZenResponsesAsChat(resp *http.Response, model string, cancel context.CancelFunc) *http.Response {
	return zenupstream.StreamResponsesAsChat(resp, model, cancel)
}

// collapseZenResponsesStream folds an upstream Responses SSE into one JSON response.
func collapseZenResponsesStream(resp *http.Response, model string, cancel context.CancelFunc) (*http.Response, error) {
	return zenupstream.CollapseResponsesStream(resp, model, cancel)
}
