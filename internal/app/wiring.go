// Package app composes RouteNest's internal services for both CLI and desktop
// entry points.
package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"routenest/internal/accounts"
	"routenest/internal/admin"
	"routenest/internal/buildinfo"
	"routenest/internal/combos"
	"routenest/internal/compaction"
	"routenest/internal/config"
	"routenest/internal/connections"
	"routenest/internal/domain"
	"routenest/internal/egress"
	"routenest/internal/platform"
	"routenest/internal/providers"
	"routenest/internal/proxycore"
	"routenest/internal/requestlog"
	"routenest/internal/routing"
	requestlogstore "routenest/internal/store/requestlog"
	"routenest/internal/upstream/openaicompat"
	"routenest/internal/zen"
)

// configureOnce guards Configure so repeated calls cannot re-register anything.
var configureOnce sync.Once

// configureCalls counts configure runs; tests use it to prove idempotence.
var configureCalls int

// Configure wires the composition root: it installs every cross-package hook
// that the packages expose as variables. It is idempotent and must run before
// any server or test that depends on the wiring. Entry points call it explicitly
// rather than relying on import side effects.
func Configure() {
	configureOnce.Do(func() {
		configureCalls++
		configure()
	})
}

func configure() {
	combos.ConfigMu = &config.Mu
	combos.Config = config.Held
	combos.GetConfig = admin.GetProxyConfig
	combos.EnsureConfig = config.Held
	combos.SaveConfig = admin.SaveProxyConfigLocked
	combos.CallClineAPIWithAccount = func(ctx context.Context, acc *domain.Account, params map[string]any, stream bool) (*http.Response, *domain.Account, error) {
		return routing.CallClineAPIWithAccountCtx(ctx, acc, params, stream)
	}
	combos.SetModelCooldown = routing.SetModelCooldown
	combos.CallFreeClineAPI = func(params map[string]any, stream bool) (*http.Response, *domain.Account, error) {
		return routing.CallFreeClineAPI(params, stream)
	}

	compaction.AnthropicToOpenAI = func(body []byte) (map[string]any, error) {
		var req proxycore.AnthropicReq
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return proxycore.AnthropicToOpenAI(req), nil
	}

	egress.ConfigProvider = admin.GetProxyConfig
	requestlog.Path = platform.ResolveDataPath(requestlogstore.FileName, nil)
	zen.IsModelGoneError = routing.IsModelGoneError
	zen.MarkModelGone = routing.MarkModelGone
	routing.ConfigProvider = admin.GetProxyConfig
	routing.ModelsProvider = routing.AllModels
	routing.ModelDisabled = routing.IsModelDisabled
	routing.SetComboCatalog(combos.RoutingCombos{})

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
