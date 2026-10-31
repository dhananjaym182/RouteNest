package proxycore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"routenest/internal/accounts"
	"routenest/internal/buildinfo"
	"routenest/internal/combos"
	"routenest/internal/compaction"
	"routenest/internal/config"
	"routenest/internal/domain"
	"routenest/internal/httpapi"
	"routenest/internal/platform"
	"routenest/internal/providers"
	"routenest/internal/requestlog"
	"routenest/internal/routing"
	"routenest/internal/zen"
	"strings"
	"sync"
	"time"
)

// Listener lifecycle: the listen address, the HTTP server and its route table, and
// Start. Route handlers are registered in Start; see proxy.go for their bodies.

// 当前监听地址（Start 启动时赋值，供管理后台展示）。
var (
	ListenHost string
	ListenPort int
)

// HTTP server 实例与路由表（RestartListener 换地址重启时复用）。
var (
	ServerMux     *http.ServeMux
	CurrentServer *http.Server
	ServerMu      sync.Mutex
)

// RestartListener 用新地址重启 HTTP 监听。
// 注意：必须在 goroutine 中调用——Shutdown 会等待当前 HTTP 请求完成，
// 若在 admin handler 内同步调用会死锁。
func RestartListener(host string, port int) error {
	if host == "" {
		host = "127.0.0.1"
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	ListenHost = host
	ListenPort = port

	ServerMu.Lock()
	old := CurrentServer
	server := &http.Server{Addr: addr, Handler: ServerMux}
	CurrentServer = server
	ServerMu.Unlock()

	if old != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = old.Shutdown(ctx)
		cancel()
	}

	fmt.Println("")
	fmt.Println(strings.Repeat("=", 58))
	fmt.Printf("  Listener restarted: %s\n", addr)
	if !IsLoopbackHost(host) {
		for _, ip := range DetectLocalIPs() {
			fmt.Printf("  http://%s:%d (LAN)\n", ip, port)
		}
		fmt.Println("  !!! 监听非本机地址，管理后台无鉴权，请确认网络环境安全")
	}
	fmt.Println(strings.Repeat("=", 58))
	return server.ListenAndServe()
}

// EffectiveAdminHost 返回管理后台/浏览器实际可用的访问地址：
// host 为空或通配地址（0.0.0.0 / ::）时展示回环 127.0.0.1，否则返回 host 本身。
func EffectiveAdminHost(host string) string {
	switch host {
	case "", "0.0.0.0", "::":
		return "127.0.0.1"
	}
	return host
}

// DetectLocalIPs 检测本机所有可用 IPv4 地址（排除回环、链路本地和未启用的网卡）。
func DetectLocalIPs() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	result := make([]string, 0, len(addrs))
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipNet.IP
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		if v4 := ip.To4(); v4 != nil {
			result = append(result, v4.String())
		}
	}
	return result
}

// IsLoopbackHost 判断监听地址是否为回环（127.x / localhost），用于安全提示。
func IsLoopbackHost(host string) bool {
	switch host {
	case "", "localhost", "127.0.0.1":
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type chatRequest struct {
	Model               string          `json:"model"`
	Messages            json.RawMessage `json:"messages"`
	Stream              bool            `json:"stream,omitempty"`
	MaxTokens           int             `json:"max_tokens,omitempty"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	Tools               json.RawMessage `json:"tools,omitempty"`
	ToolChoice          json.RawMessage `json:"tool_choice,omitempty"`
	ReasoningEffort     string          `json:"reasoning_effort,omitempty"`
	ReasoningEffortAlt  string          `json:"routing.ReasoningEffort,omitempty"`
	Extra               map[string]any  `json:"-"`
}

// IsFreeModelEntry 判断模型是否免费（/models 过滤用）：
// Cost 直接标记 free，或 zen 来源且命中免费判定（种子白名单兜底）。

// Start serves the proxy on host:port. registerAdmin adds the admin console routes
// to the mux; the caller supplies it so proxycore does not depend on admin.
func Start(host string, port int, registerAdmin func(*http.ServeMux)) error {
	log.Printf("Data directory: %s (set %s to override)", platform.CurrentDataDir(), platform.DataDirEnv)
	p := accounts.LoadPool()
	requestlog.Load()
	// 自定义 provider 声明的模型同步进池：模型管理页可见、可启停、参与自动回退
	providers.SyncModels()
	activeCount := 0
	for _, a := range p.Accounts {
		if a.Status == "active" {
			// Try to pre-warm tokens
			if a.AccessToken == "" || time.Now().UnixMilli() >= a.ExpiresAt {
				if err := accounts.RefreshAccountToken(a); err != nil {
					log.Printf("  Pre-warm failed for %s: %v", a.Email, err)
					continue
				}
			}
			activeCount++
		}
	}
	log.Printf("Loaded %d active accounts from pool", activeCount)

	// 启动时异步同步一次 Cline 官方推荐模型（不阻塞启动）
	routing.StartModelSync()

	// §W3-C：已启用 provider 的上游模型清单后台自动检测（下架标记 + 新模型入队待审）
	providers.StartModelRecheck()

	// opencode zen：定时同步免费模型列表 + 压缩会话状态清理
	if zen.GetConfig().Enabled {
		zen.StartModelsRefresher()
	}
	compaction.StartCleanup()

	freePort(port)

	mux := http.NewServeMux()

	health := httpapi.Health{
		Version:        func() string { return buildinfo.AppVersion },
		ActiveAccounts: func() int { return activeCount },
	}
	mux.HandleFunc("/v1/health", CorsHandler(health.Handler))
	mux.HandleFunc("/health", CorsHandler(health.Handler))

	// Admin API (frontend + REST)
	if registerAdmin != nil {
		registerAdmin(mux)
	}

	apiKeyHandler := (&httpapi.APIKeyAuth{
		Keys: func() []string { return accounts.LoadPool().Keys },
		Unauthorized: func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": map[string]string{
					"message": "invalid API key. Generate one at /admin/ or set x-api-key header",
					"type":    "auth_error",
				},
			})
		},
	}).Wrap

	modelsHandler := apiKeyHandler(func(w http.ResponseWriter, r *http.Request) {
		list := routing.BuildModelsList(ConfigProvider().OnlyFree)
		writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": list})
	})
	mux.HandleFunc("/v1/models", modelsHandler)
	mux.HandleFunc("/models", modelsHandler)

	chatHandler := apiKeyHandler(func(w http.ResponseWriter, r *http.Request) {
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
		if activeCount == 0 && len(accounts.LoadPool().Accounts) == 0 {
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
	})
	mux.HandleFunc("/v1/chat/completions", withBodyLimit(chatHandler))
	mux.HandleFunc("/chat/completions", withBodyLimit(chatHandler))

	// Anthropic Messages API support
	anthropicHandler := apiKeyHandler(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		handleAnthropicMessages(w, r)
	})
	mux.HandleFunc("/v1/messages", withBodyLimit(anthropicHandler))
	mux.HandleFunc("/messages", withBodyLimit(anthropicHandler))

	// Anthropic Messages API token counting (polyfilled)
	countTokensHandler := apiKeyHandler(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		compaction.HandleCountTokens(w, r)
	})
	mux.HandleFunc("/v1/messages/count_tokens", withBodyLimit(countTokensHandler))
	mux.HandleFunc("/messages/count_tokens", withBodyLimit(countTokensHandler))

	// OpenAI Responses API support（所有上游：zen 免费模型 + Cline 账号池）
	responsesHandler := apiKeyHandler(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		HandleResponses(w, r)
	})
	mux.HandleFunc("/v1/responses", withBodyLimit(responsesHandler))
	mux.HandleFunc("/responses", withBodyLimit(responsesHandler))

	if host == "" {
		host = "127.0.0.1"
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	ListenHost = host
	ListenPort = port
	ServerMux = mux
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}
	ServerMu.Lock()
	CurrentServer = server
	ServerMu.Unlock()

	// 启动后台冷却恢复巡检
	routing.StartCooldownRecovery()

	fmt.Println("")
	fmt.Println(strings.Repeat("=", 58))
	fmt.Printf("  RouteNest %s - No CLI Required\n", buildinfo.AppVersion)
	fmt.Println(strings.Repeat("=", 58))
	fmt.Printf("  http://%s\n", addr)
	fmt.Printf("  http://%s/v1\n", addr)
	if !IsLoopbackHost(host) {
		for _, ip := range DetectLocalIPs() {
			fmt.Printf("  http://%s:%d (LAN)\n", ip, port)
		}
		fmt.Println("  !!! 监听非本机地址，管理后台无鉴权，请确认网络环境安全")
	}
	fmt.Println("  API Key: any value")
	fmt.Printf("  Model:   %s\n", routing.DefaultModel())
	fmt.Printf("  Accounts: %d total, %d active\n", len(accounts.LoadPool().Accounts), activeCount)
	if zc := zen.GetConfig(); zc.Enabled {
		fmt.Printf("  OpenCode: enabled (%s free models)\n", strings.TrimRight(zc.BaseURL, "/"))
	} else {
		fmt.Println("  OpenCode: disabled")
	}
	fmt.Println(strings.Repeat("=", 58))

	return server.ListenAndServe()
}
