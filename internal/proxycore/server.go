package proxycore

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"routenest/internal/accounts"
	"routenest/internal/buildinfo"
	"routenest/internal/compaction"
	"routenest/internal/httpapi"
	"routenest/internal/httpx"
	"routenest/internal/platform"
	"routenest/internal/providers"
	"routenest/internal/requestlog"
	"routenest/internal/routing"
	"routenest/internal/zen"
	"strconv"
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
	if !IsLoopbackHost(host) {
		return fmt.Errorf("non-loopback listener requires trusted-proxy/TLS configuration")
	}
	addr := listenerAddress(host, port)
	ServerMu.Lock()
	old := CurrentServer
	mux := ServerMux
	oldHost, oldPort := ListenHost, ListenPort
	ServerMu.Unlock()
	if mux == nil {
		return fmt.Errorf("listener route table is unavailable")
	}
	if old != nil && host == oldHost && port == oldPort {
		return nil
	}

	if old != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := old.Shutdown(ctx)
		cancel()
		if err != nil {
			return fmt.Errorf("stop old listener: %w", err)
		}
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		if rollbackErr := restoreListener(oldHost, oldPort, mux); rollbackErr != nil {
			return fmt.Errorf("bind new listener: %v; rollback failed: %w", err, rollbackErr)
		}
		return fmt.Errorf("bind new listener: %w", err)
	}
	server := &http.Server{Addr: addr, Handler: mux}
	ServerMu.Lock()
	CurrentServer = server
	ListenHost = host
	ListenPort = port
	ServerMu.Unlock()
	serveListener(server, listener)
	if err := probeListener(addr); err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = server.Shutdown(ctx)
		cancel()
		if rollbackErr := restoreListener(oldHost, oldPort, mux); rollbackErr != nil {
			return fmt.Errorf("new listener readiness failed: %v; rollback failed: %w", err, rollbackErr)
		}
		return fmt.Errorf("new listener readiness failed: %w", err)
	}

	fmt.Println("")
	fmt.Println(strings.Repeat("=", 58))
	fmt.Printf("  Listener restarted: %s\n", addr)
	fmt.Println(strings.Repeat("=", 58))
	return nil
}

func serveListener(server *http.Server, listener net.Listener) {
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("listener stopped unexpectedly: %v", err)
		}
	}()
}

func probeListener(addr string) error {
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		lastErr = err
		time.Sleep(25 * time.Millisecond)
	}
	return lastErr
}

func restoreListener(host string, port int, mux http.Handler) error {
	if host == "" || port <= 0 {
		ServerMu.Lock()
		CurrentServer = nil
		ServerMu.Unlock()
		return nil
	}
	addr := listenerAddress(host, port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		ServerMu.Lock()
		CurrentServer = nil
		ServerMu.Unlock()
		return err
	}
	server := &http.Server{Addr: addr, Handler: mux}
	ServerMu.Lock()
	CurrentServer = server
	ListenHost = host
	ListenPort = port
	ServerMu.Unlock()
	serveListener(server, listener)
	return probeListener(addr)
}

// EffectiveAdminHost 返回管理后台/浏览器实际可用的访问地址：
// host 为空或通配地址（0.0.0.0 / ::）时展示回环 127.0.0.1，否则返回 host 本身。
func EffectiveAdminHost(host string) string {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	switch host {
	case "", "0.0.0.0", "::":
		return "127.0.0.1"
	}
	return host
}

func listenerAddress(host string, port int) string {
	return net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))
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
	host = strings.Trim(strings.TrimSpace(host), "[]")
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
	return StartWithContext(context.Background(), host, port, registerAdmin)
}

// StartWithContext is Start with a caller-owned lifecycle for cancellable
// background workers. The listener remains independently restartable.
func StartWithContext(ctx context.Context, host string, port int, registerAdmin func(*http.ServeMux)) error {
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
	modelRecheckDone := providers.StartModelRecheck(ctx)
	defer func() {
		if ctx.Err() != nil {
			<-modelRecheckDone
		}
	}()

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
		RuntimeState:   providers.RuntimeStateHealth,
	}
	mux.HandleFunc("/v1/health", httpx.CORS(health.Handler))
	mux.HandleFunc("/health", httpx.CORS(health.Handler))

	// Admin API (frontend + REST)
	if registerAdmin != nil {
		registerAdmin(mux)
	}

	apiKeyHandler := (&httpapi.APIKeyAuth{
		Snapshot: accounts.ClientAuthSnapshot,
		Unauthorized: func(w http.ResponseWriter, r *http.Request) {
			httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{
				"error": map[string]string{
					"message": "invalid API key. Generate one at /admin/ or set x-api-key header",
					"type":    "auth_error",
				},
			})
		},
	}).Wrap

	modelsHandler := apiKeyHandler(func(w http.ResponseWriter, r *http.Request) {
		list := routing.BuildModelsList(liveConfig().OnlyFree)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"object": "list", "data": list})
	})
	mux.HandleFunc("/v1/models", modelsHandler)
	mux.HandleFunc("/models", modelsHandler)

	chatHandler := apiKeyHandler(newChatCompletionsHandler())
	mux.HandleFunc("/v1/chat/completions", withBodyLimit(chatHandler))
	mux.HandleFunc("/chat/completions", withBodyLimit(chatHandler))

	// Anthropic Messages API support
	anthropicHandler := apiKeyHandler(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		handleAnthropicMessages(w, r)
	})
	mux.HandleFunc("/v1/messages", withBodyLimit(anthropicHandler))
	mux.HandleFunc("/messages", withBodyLimit(anthropicHandler))

	// Anthropic Messages API token counting (polyfilled)
	countTokensHandler := apiKeyHandler(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		compaction.HandleCountTokens(w, r)
	})
	mux.HandleFunc("/v1/messages/count_tokens", withBodyLimit(countTokensHandler))
	mux.HandleFunc("/messages/count_tokens", withBodyLimit(countTokensHandler))

	// OpenAI Responses API support（所有上游：zen 免费模型 + Cline 账号池）
	responsesHandler := apiKeyHandler(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		HandleResponses(w, r)
	})
	mux.HandleFunc("/v1/responses", withBodyLimit(responsesHandler))
	mux.HandleFunc("/responses", withBodyLimit(responsesHandler))

	if host == "" {
		host = "127.0.0.1"
	}
	addr := listenerAddress(host, port)
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
		fmt.Println("  Non-loopback administration requires the configured canonical HTTPS and trusted-proxy boundary")
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
