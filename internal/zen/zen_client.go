package zen

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"routenest/internal/dispatch"
	"routenest/internal/domain"
	"routenest/internal/platform"
	"routenest/internal/protocol/openaichat"
	"routenest/internal/provideradapters"
	"routenest/internal/providerstate"
	zenupstream "routenest/internal/upstream/zen"
)

// marshalZenJSON encodes a zen body without HTML escaping: the free-tier gate
// compares tool descriptions and the <env> marker literally, so "<" and ">"
// must reach the wire as-is (Go's default encoder rewrites them to \u003c).
func marshalZenJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// zenHeaderWatchdogTimeout 限制 zen 上游"发出请求 → 收到响应头"的最长等待。
// 看门狗 30s：黑洞连接（抖动代理节点）最多挂 30s 即掐，重试/故障转移更快启动。
// 更长的真实 TTFT（免费层排队 4~11s）不受影响 —— 看门狗只管响应头之前。
const zenHeaderWatchdogTimeout = 30 * time.Second

// zenMaxRetryWait 单次重试的最大等待（上游 Retry-After 可达 13h，绝不能在请求里睡数小时）。
const zenMaxRetryWait = 60 * time.Second

// zenMaxProxyCooldown 出口代理冷却的上限（本地代理端口背后可切换节点，长冷却有害）。
const zenMaxProxyCooldown = 30 * time.Minute

// withCancelOnClose 包装响应 body：调用方关闭 body 时同步释放请求 ctx，
// 避免长生命周期流式响应泄漏 cancel 函数。
func withCancelOnClose(resp *http.Response, cancel context.CancelFunc) *http.Response {
	resp.Body = &cancelOnCloseBody{ReadCloser: resp.Body, cancel: cancel}
	return resp
}

type cancelOnCloseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
	once   sync.Once
}

func (b *cancelOnCloseBody) Close() error {
	b.once.Do(b.cancel)
	return b.ReadCloser.Close()
}

// isRateLimited 限流信号识别：429/503 直接命中；502/403 按错误体关键词。
func isRateLimited(status int, body string) bool {
	return dispatch.IsRateLimited(status, body)
}

// parseRetryAfter parses Retry-After (seconds or HTTP date); invalid input is 0.
func parseRetryAfter(h string) time.Duration {
	return dispatch.ParseRetryAfter(h)
}

func randIntn(n int) int {
	return zenupstream.RandIntn(n)
}

// withRetryJitter adds 0-25% jitter to a backoff delay.
func withRetryJitter(delay time.Duration) time.Duration {
	return zenupstream.WithRetryJitter(delay)
}

// ============ zen 上游调用 ============

// anonymousCoreTools are the tool names expected by the anonymous free tier.
var anonymousCoreTools = zenupstream.AnonymousCoreTools

// ensureAnonymousTools tops up missing core tool definitions.
func ensureAnonymousTools(body map[string]any) {
	zenupstream.EnsureAnonymousTools(body)
}

// buildZenBody constructs a zen chat request body.
func buildZenBody(params map[string]any, stream bool, anonymous bool) map[string]any {
	opts := BodyOptionsProvider()
	return zenupstream.BuildChatBody(params, stream, anonymous, zenupstream.BodyOptions{
		PassThroughKeys: opts.PassThroughKeys,
		ResolveModel: func(model string) (string, bool) {
			if m, ok := ResolveInfo(model); ok {
				return m.ID, true
			}
			return "", false
		},
	})
}

// zenMaxSessionRotations caps how many registered sessions one upstream call
// will try before giving up.
const zenMaxSessionRotations = 4

// zenAPIKeyFor picks the outgoing Authorization identity: free zen models are
// served anonymously (Bearer public) even when a real key is configured.
func zenAPIKeyFor(params map[string]any, cfgKey string) string {
	if m, ok := ResolveInfo(zenUpstreamModelID(params)); ok && IsFreeModel(m) {
		return "public"
	}
	return cfgKey
}

// Call 调用 zen 上游：并发信号量 + 指数退避重试 + 代理冷却 + 故障转移计数。
// 匿名（public key）免费层自 2026-09 起只接受 agent 形态的流式请求：
// 上游强制 stream:true，下游要 JSON 时把 SSE 折叠回单个 chat.completion 响应。
// 返回的响应由调用方关闭。
func Call(params map[string]any, stream bool) (*http.Response, error) {
	cfg := GetConfig()
	zenEnsureSessionPool()
	// 免费模型统一用匿名身份（Bearer public），与官方 CLI 无 key 时的行为一致；
	// 付费模型保留配置的 key。
	apiKey := zenAPIKeyFor(params, cfg.Key)
	anonymous := apiKey == "public"
	// muse-spark 系走 OpenAI Responses 协议（/responses），其余走 chat/completions；
	// 两条路径统一在返回前转回 chat 协议，调用方无感
	useResponses := zenUpstreamNeedsResponses(params)
	protoName := "chat"
	var bodyJSON []byte
	var err error
	if useResponses {
		protoName = "responses"
		bodyJSON, err = marshalZenJSON(buildZenResponsesBody(params))
	} else {
		bodyJSON, err = marshalZenJSON(buildZenBody(params, stream, anonymous))
	}
	if err != nil {
		return nil, fmt.Errorf("marshal zen body: %w", err)
	}
	bundle, ok := provideradapters.Default().Get("opencodezen")
	if !ok {
		return nil, fmt.Errorf("opencode adapter is unavailable")
	}

	StateMu.Lock()
	sem := zenSem
	if sem == nil {
		rebuildZenSemLocked()
		sem = zenSem
	}
	StateMu.Unlock()
	sem <- struct{}{}
	defer func() { <-sem }()

	retries := cfg.Retries
	if retries <= 0 {
		retries = 3
	}
	delay := time.Second
	sessionRotations := 0

	for attempt := 0; ; attempt++ {
		// 响应头看门狗：黑洞场景（TCP 通、握手/响应头静默丢弃）请求会永久挂起，
		// 且 Call 不返回则 MarkFail 不触发、故障转移永远无法激活。
		// 60s 内未收到响应头则取消本次尝试（计为网络错误、走重试/故障转移）；
		// 响应头到达后立即停掉看门狗，流式传输时长不受限制。
		watchCtx, watchCancel := context.WithCancel(context.Background())
		watchdog := time.AfterFunc(zenHeaderWatchdogTimeout, watchCancel)

		modelID := ""
		if model, _ := params["model"].(string); model != "" {
			if m, ok := ResolveInfo(model); ok {
				modelID = m.ID
			}
		}
		sess := zenSessionID(params)
		credentialID := domain.CredentialID("opencode:public")
		if anonymous && zenSessions.PoolLen() > 0 {
			credentialID = SessionCredentialID(sess)
		}
		log.Printf("  zen upstream: model=%v proto=%s stream=%v(下游=%v) msgs=%d via=%s attempt=%d session=%s",
			bodyParamsModel(params), protoName, anonymous, stream, getMsgCount(params), describeZenProxy(), attempt+1, platform.Truncate(sess, 30))

		resp, err := bundle.Invoker.Invoke(watchCtx, provideradapters.Invocation{
			ProviderID: "opencode", HTTP: getZenHTTPClient(), BaseURL: cfg.BaseURL,
			APIKey: apiKey, Headers: cfg.ZenHeaders,
			Credential: provideradapters.Credential{ID: credentialID},
			Body:       bodyJSON, Params: params, Stream: stream, Protocol: protoName,
			ModelID: modelID, SessionID: sess, SessionIDs: zenSessions.PoolSessions(),
		})
		if err != nil {
			watchdog.Stop()
			watchCancel()
			// 网络错误：退避重试；重试耗尽计一次失败（网络类故障也参与故障转移，
			// 持续网络不可达时才会切 Cline 池）
			if attempt < retries {
				log.Printf("  zen network error (%v), retry %d/%d after %v", err, attempt+1, retries, delay)
				time.Sleep(withRetryJitter(delay))
				delay *= 2
				continue
			}
			MarkFail()
			msg := fmt.Errorf("zen request: %w", err)
			if strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "handshake") ||
				strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "unreachable") ||
				strings.Contains(err.Error(), "context canceled") {
				return nil, fmt.Errorf("%w（opencode.ai 网络不可达，可在管理页「上游服务 → opencode 出口代理」配置代理）", msg)
			}
			return nil, msg
		}
		watchdog.Stop()
		if resp.StatusCode == http.StatusOK {
			markZenSuccess()
			if useResponses {
				// Responses 上游统一流式：下游要 SSE 时转写成 chat chunk，要 JSON 时折叠
				if stream {
					return streamZenResponsesAsChat(resp, zenUpstreamModelID(params), watchCancel), nil
				}
				return collapseZenResponsesStream(resp, zenUpstreamModelID(params), watchCancel)
			}
			if anonymous && !stream {
				// 折叠路径内部会关闭 body（连带释放 watchCtx）
				return collapseZenStreamResponse(resp, bodyParamsModel(params))
			}
			return withCancelOnClose(resp, watchCancel), nil
		}

		watchCancel()
		bodyBytes := readAllLimited(resp.Body, 64<<10)
		resp.Body.Close()
		observeZenFailure(bundle, credentialID, bodyParamsModel(params), resp, bodyBytes)
		reason := fmt.Sprintf("zen API %d: %s", resp.StatusCode, platform.Truncate(string(bodyBytes), 500))

		// 上游明确报「模型不存在」时清理下架残留（同步标记 Delisted 保留的模型）
		if model := bodyParamsModel(params); model != "" && IsModelGoneError(resp.StatusCode, string(bodyBytes)) {
			MarkModelGone(model)
		}

		// 上游 500/502/504 多为瞬时故障，退避重试（503 走限流分支）
		if resp.StatusCode == 500 || resp.StatusCode == 502 || resp.StatusCode == 504 {
			if attempt < retries {
				log.Printf("  zen server error (%d), retry %d/%d after %v", resp.StatusCode, attempt+1, retries, delay)
				time.Sleep(withRetryJitter(delay))
				delay *= 2
				continue
			}
			MarkFail()
			return nil, fmt.Errorf("%s", reason)
		}

		if isRateLimited(resp.StatusCode, string(bodyBytes)) {
			ra := parseRetryAfter(resp.Header.Get("Retry-After"))
			// 免费层按 (session, ip) 计量：换一个已注册会话即可立即恢复，
			// 不需要冷却出口代理。只有匿名通道适用。
			if anonymous && sessionRotations < zenMaxSessionRotations {
				rotated := false
				if zenSessions.PoolLen() > 1 {
					_, rotated = zenSessions.Rotate()
				} else if zenRefreshSessionPool(true) > 1 {
					_, rotated = zenSessions.Rotate()
				}
				if rotated {
					sessionRotations++
					log.Printf("  zen free-tier limit, rotating registered session %d/%d", sessionRotations, zenMaxSessionRotations)
					continue
				}
			}
			// 冷却当前出口代理（Retry-After 优先，默认 10 分钟，封顶 30 分钟）：
			// 单个本地代理端口背后可切换节点（出口 IP 变化），长冷却有害无益
			if idx := lastZenProxyIdx(); idx >= 0 {
				d := ra
				if d <= 0 {
					d = 10 * time.Minute
				}
				if d > zenMaxProxyCooldown {
					d = zenMaxProxyCooldown
				}
				cooldownZenProxy(idx, d)
			}
			// 短限流（≤60s）值得按 Retry-After 等待重试；
			// 长限流（实测可达 13h）重试无意义，立即失败触发故障转移
			if attempt < retries && ra > 0 && ra <= zenMaxRetryWait {
				log.Printf("  zen rate limited (%d), retry %d/%d after %v", resp.StatusCode, attempt+1, retries, ra)
				time.Sleep(withRetryJitter(ra))
				delay *= 2
				continue
			}
			if attempt < retries && ra <= 0 {
				log.Printf("  zen rate limited (%d), retry %d/%d after %v", resp.StatusCode, attempt+1, retries, delay)
				time.Sleep(withRetryJitter(delay))
				delay *= 2
				continue
			}
			MarkFail()
			if anonymous && zenSessions.PoolLen() == 0 {
				return nil, fmt.Errorf("%s（未找到已注册的 opencode 会话：免费层只服务上游已登记的 session；请在管理页填入 sessionIds，或在本机用 opencode/wu 正常对话一次以生成）", reason)
			}
			if ra > 10*time.Minute {
				return nil, fmt.Errorf("%s（opencode 免费层出口 IP 限流 ~%s，切换代理节点后可立即重试）", reason, ra.Truncate(time.Minute))
			}
			return nil, fmt.Errorf("%s", reason)
		}

		MarkFail()
		return nil, fmt.Errorf("%s", reason)
	}
}

func observeZenFailure(bundle provideradapters.Bundle, credentialID domain.CredentialID, modelID string, response *http.Response, body []byte) {
	if bundle.FailureClassifier == nil || !providerstate.HasDefault() {
		return
	}
	scope := domain.AvailabilityScope{ProviderID: "opencode", CredentialID: credentialID, ModelID: modelID}
	for _, observation := range bundle.FailureClassifier.ClassifyFailure(provideradapters.FailureInput{
		Scope: scope, StatusCode: response.StatusCode, Header: response.Header, Body: body,
	}) {
		if err := providerstate.Default().Apply(observation); err != nil {
			log.Printf("opencode: apply availability observation: %v", err)
		}
	}
}

func bodyParamsModel(params map[string]any) string {
	m, _ := params["model"].(string)
	return m
}

func getMsgCount(params map[string]any) int {
	if msgs, ok := params["messages"].([]any); ok {
		return len(msgs)
	}
	return 0
}

// collapseZenStreamResponse 消费匿名免费层强制返回的 SSE 流，
// 折叠成等价的单个 chat.completion JSON 响应（下游要 JSON 时保持透明）。
func collapseZenStreamResponse(resp *http.Response, model string) (*http.Response, error) {
	defer resp.Body.Close()
	acc := &zenCollapseAcc{Model: model, Created: time.Now().Unix()}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(line[5:])
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk struct {
			ID      string `json:"id"`
			Model   string `json:"model"`
			Created int64  `json:"created"`
			Choices []struct {
				FinishReason any `json:"finish_reason"`
				Delta        struct {
					Content          string `json:"content"`
					ReasoningContent any    `json:"reasoning_content"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Name     string `json:"name"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Usage map[string]any `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if chunk.ID != "" {
			acc.ID = chunk.ID
		}
		if chunk.Model != "" {
			acc.Model = chunk.Model
		}
		if chunk.Created != 0 {
			acc.Created = chunk.Created
		}
		if len(chunk.Choices) > 0 {
			choice := chunk.Choices[0]
			acc.Content += choice.Delta.Content
			if rc, ok := choice.Delta.ReasoningContent.(string); ok {
				acc.Reasoning += rc
			}
			if choice.FinishReason != nil {
				switch v := choice.FinishReason.(type) {
				case string:
					acc.FinishReason = v
				}
			}
			for _, tc := range choice.Delta.ToolCalls {
				for len(acc.ToolCalls) <= tc.Index {
					acc.ToolCalls = append(acc.ToolCalls, zenCollapseTool{})
				}
				t := &acc.ToolCalls[tc.Index]
				if tc.ID != "" {
					t.ID = tc.ID
				}
				if tc.Function.Name != "" {
					t.Name += tc.Function.Name
				}
				t.Arguments += tc.Function.Arguments
			}
		}
		if chunk.Usage != nil {
			acc.Usage = openaichat.MergeUsage(acc.Usage, openaichat.ParseUsage(chunk.Usage))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("zen stream read: %w", err)
	}
	log.Printf("  zen collapse: stream -> json (content_len=%d tool_calls=%d)", len(acc.Content), len(acc.ToolCalls))
	return zenCollapseToChatResponse(acc, resp.Request)
}

// readAllLimited 读取响应体，最多 limit 字节（防御异常大的错误页）。
func readAllLimited(r io.Reader, limit int64) []byte {
	data, _ := io.ReadAll(io.LimitReader(r, limit))
	return data
}

func describeZenProxy() string {
	cfg := GetConfig()
	if len(cfg.Proxies) == 0 {
		return "direct"
	}
	idx := lastZenProxyIdx()
	if idx < 0 {
		idx = 0
	}
	idx %= len(cfg.Proxies)
	return fmt.Sprintf("proxy[%d]=%s", idx+1, platform.Truncate(MaskProxyURL(cfg.Proxies[idx]), 60))
}
