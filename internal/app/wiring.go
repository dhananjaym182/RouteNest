// Package app composes RouteNest's internal services for both CLI and desktop
// entry points.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/admin"
	"cline-go-proxy/internal/buildinfo"
	"cline-go-proxy/internal/combos"
	"cline-go-proxy/internal/compaction"
	"cline-go-proxy/internal/config"
	"cline-go-proxy/internal/connections"
	"cline-go-proxy/internal/domain"
	"cline-go-proxy/internal/egress"
	"cline-go-proxy/internal/platform"
	"cline-go-proxy/internal/providers"
	"cline-go-proxy/internal/proxycore"
	"cline-go-proxy/internal/requestlog"
	"cline-go-proxy/internal/routing"
	requestlogstore "cline-go-proxy/internal/store/requestlog"
	"cline-go-proxy/internal/upstream/openaicompat"
	"cline-go-proxy/internal/zen"
)

func init() {
	combos.ConfigMu = &admin.ConfigMu
	combos.Config = func() *config.Proxy { return admin.Config }
	combos.GetConfig = admin.GetProxyConfig
	combos.EnsureConfig = func() *config.Proxy {
		if admin.Config == nil {
			admin.Config = admin.DefaultProxyConfig()
		}
		return admin.Config
	}
	combos.SaveConfig = admin.SaveProxyConfigLocked
	combos.IsAccountUnavailable = func(err error) bool {
		var e *routing.ClineAccountUnavailableError
		return errors.As(err, &e)
	}
	combos.NewUnavailableError = func(message string) error {
		return &routing.FreeModelUnavailableError{Message: message}
	}
	combos.CallClineAPIWithAccount = func(ctx context.Context, acc *domain.Account, params map[string]any, stream bool) (*http.Response, *domain.Account, error) {
		return routing.CallClineAPIWithAccountCtx(ctx, acc, params, stream)
	}
	combos.IsDeterministicRequestError = routing.IsDeterministicRequestError
	combos.SetModelCooldown = routing.SetModelCooldown
	combos.TruncateEmail = routing.TruncateEmail
	combos.CallFreeClineAPI = func(params map[string]any, stream bool) (*http.Response, *domain.Account, error) {
		return routing.CallFreeClineAPI(params, stream)
	}
	combos.WithModel = routing.WithModel

	routing.IsContextLengthError = compaction.IsContextLengthError
	compaction.CallClineAPI = func(params map[string]any, stream bool) (*http.Response, error) {
		resp, _, err := routing.CallClineAPI(params, stream)
		return resp, err
	}
	compaction.AnthropicToOpenAI = func(body []byte) (map[string]any, error) {
		var req proxycore.AnthropicReq
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return proxycore.AnthropicToOpenAI(req), nil
	}

	egress.DataPathResolver = func(name string) string { return platform.ResolveDataPath(name, nil) }
	egress.ConfigProvider = admin.GetProxyConfig
	requestlog.Path = platform.ResolveDataPath(requestlogstore.FileName, nil)
	zen.DataPathResolver = func(name string) string { return platform.ResolveDataPath(name, nil) }
	zen.IsModelGoneError = routing.IsModelGoneError
	zen.MarkModelGone = routing.MarkModelGone
	routing.ConfigProvider = admin.GetProxyConfig
	routing.FindCombo = combos.FindCombo
	routing.ModelsProvider = routing.AllModels
	routing.ModelDisabled = routing.IsModelDisabled
	routing.EnabledCombosProvider = combos.EnabledList

	buildinfo.HTTPClient = egress.Client
	accounts.HTTPClient = egress.Client
	connections.HTTPClient = egress.Client
	connections.IsFreeModel = routing.IsFreeModelID

	providers.TransportProvider = func() http.RoundTripper { return egress.Client.Transport }
	bodyOptionsProvider := func() openaicompat.BodyOptions {
		return openaicompat.BodyOptions{
			PassThroughKeys:     routing.PassThroughKeys,
			MinMaxTokens:        routing.MinUpstreamMaxTokens,
			DefaultMaxTokens:    routing.DefaultMaxTokens,
			ModelMaxOutputLimit: routing.ModelMaxOutputLimit,
		}
	}
	providers.BodyOptionsProvider = bodyOptionsProvider
	zen.BodyOptionsProvider = bodyOptionsProvider
	providers.ConfigProvider = admin.GetProxyConfig
	providers.RequestLogsSnapshot = func() []domain.RequestLog {
		requestlog.Mu.Lock()
		defer requestlog.Mu.Unlock()
		return append([]domain.RequestLog(nil), requestlog.Logs...)
	}
	freeIDs := make([]string, 0, len(zen.SeedModels)*2)
	for _, seed := range zen.SeedModels {
		freeIDs = append(freeIDs, strings.ToLower(seed.ID))
		for _, alias := range seed.Aliases {
			freeIDs = append(freeIDs, strings.ToLower(alias))
		}
	}
	providers.SetFreeModelIDs(freeIDs)

	proxycore.ConfigProvider = admin.GetProxyConfig
	proxycore.RegisterAdminRoutes = admin.RegisterRoutes
	proxycore.RecordUsage = RecordUsage
}

// RecordUsage attributes finalized usage to either a Cline account or the
// provider connection tagged on the upstream response.
func RecordUsage(acc *domain.Account, upstream *http.Response, reqLog *domain.RequestLog, model string, usage proxycore.TokenUsage) {
	if acc != nil {
		routing.RecordTokenUsage(acc, model, usage)
		return
	}
	conn := connections.ResponseConnection(upstream)
	if conn == nil {
		return
	}
	if reqLog != nil {
		reqLog.ConnectionID = conn.ID
		reqLog.ConnectionEmail = conn.Email
	}
	if usage.Valid {
		connections.RecordConnectionUsage(conn.ID, model, usage)
	}
}
