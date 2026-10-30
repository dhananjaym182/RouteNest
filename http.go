package main

import (
	"net/http"
	"os/exec"
	"strings"
	"time"

	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/buildinfo"
	"cline-go-proxy/internal/connections"
	"cline-go-proxy/internal/providers"
	"cline-go-proxy/internal/upstream/openaicompat"
)

var execCommand = exec.Command

// 全局出站 transport：经 cline_proxy.go 的钩子支持应用内出口代理池
// （Cline 对话/认证/模型同步与复用此 transport 的自定义 Provider 共同生效）。
var httpTransport = &http.Transport{
	Proxy:               clineOutboundProxy,
	DialContext:         clineDialContext,
	MaxIdleConns:        100,
	MaxIdleConnsPerHost: 10,
	IdleConnTimeout:     90 * time.Second,
	DisableCompression:  false,
	// 拨号由 clineDialContext 负责（出口代理池 + 内置 net.Dialer 超时），
	// 不要在这里再设 DialContext：Go 结构体字面量不允许重复字段。
	TLSHandshakeTimeout:   10 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
	// 响应头限时：非流式请求在生成完成前不返回响应头，等价于旧的兜底超时；
	// 流式响应的响应头几乎立即到达，不限制流的后续读取时长。
	ResponseHeaderTimeout: 5 * time.Minute,
}

var httpClient = &http.Client{
	Transport: httpTransport,
	// 注意：绝不能设置 Client.Timeout —— Go 的 Client.Timeout 包含整个响应体读取时间，
	// 会把超过 5 分钟的流式响应拦腰截断（表现为「流式中途断掉」）。
	// 超时改为 transport 级：拨号/TLS/响应头限时，body 读取不限时，
	// 由客户端断开（request ctx）或上游错误/关闭自然终止。
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	return cmd.Start()
}

// init 注入更新检查（internal/buildinfo）使用的出站客户端，
// 与其它出站请求一致地走 cline 后端出口代理池（保持与拆包前相同的行为）。
func init() {
	buildinfo.HTTPClient = httpClient
	accounts.HTTPClient = httpClient
	connections.HTTPClient = httpClient
	connections.IsFreeModel = isFreeModelID

	providers.TransportProvider = func() http.RoundTripper { return httpClient.Transport }
	providers.BodyOptionsProvider = func() openaicompat.BodyOptions {
		return openaicompat.BodyOptions{
			PassThroughKeys:     passThroughKeys,
			MinMaxTokens:        minUpstreamMaxTokens,
			DefaultMaxTokens:    defaultMaxTokens,
			ModelMaxOutputLimit: modelMaxOutputLimit,
		}
	}
	providers.ConfigProvider = getProxyConfig
	providers.APIErrorFactory = func(status int, message string) error {
		return &clineAPIError{statusCode: status, message: message}
	}
	providers.ErrorRuleDecision = ruleDecision
	providers.ErrorStatus = clineErrorStatus
	providers.RuleCooldownUntil = ruleCooldownUntil
	providers.StopError = func(err error) error { return &requestErrorStop{err: err} }
	providers.LogRuleHit = logRuleHit
	providers.RequestLogsSnapshot = func() []RequestLog {
		requestLogsMu.Lock()
		defer requestLogsMu.Unlock()
		return append([]RequestLog(nil), requestLogs...)
	}
	freeIDs := make([]string, 0, len(zenSeedModels)*2)
	for _, seed := range zenSeedModels {
		freeIDs = append(freeIDs, strings.ToLower(seed.ID))
		for _, alias := range seed.Aliases {
			freeIDs = append(freeIDs, strings.ToLower(alias))
		}
	}
	providers.SetFreeModelIDs(freeIDs)
}
