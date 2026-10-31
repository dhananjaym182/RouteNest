package main

import (
	"net/http"
	"strings"

	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/buildinfo"
	"cline-go-proxy/internal/combos"
	"cline-go-proxy/internal/connections"
	"cline-go-proxy/internal/egress"
	"cline-go-proxy/internal/providers"
	"cline-go-proxy/internal/requestlog"
	"cline-go-proxy/internal/routing"
	requestlogstore "cline-go-proxy/internal/store/requestlog"
	"cline-go-proxy/internal/upstream/openaicompat"
	"cline-go-proxy/internal/zen"
)

// init connects the leaf egress package to root-owned data-path migration and
// injects its stable client into all outbound consumers.
func init() {
	egress.DataPathResolver = func(name string) string { return resolveDataPath(name) }
	egress.ConfigProvider = getProxyConfig
	requestlog.Path = resolveDataPath(requestlogstore.FileName)
	zen.DataPathResolver = func(name string) string { return resolveDataPath(name) }
	zen.IsModelGoneError = routing.IsModelGoneError
	zen.MarkModelGone = routing.MarkModelGone
	routing.ConfigProvider = getProxyConfig
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
	providers.ConfigProvider = getProxyConfig
	providers.RequestLogsSnapshot = func() []RequestLog {
		requestlog.Mu.Lock()
		defer requestlog.Mu.Unlock()
		return append([]RequestLog(nil), requestlog.Logs...)
	}
	freeIDs := make([]string, 0, len(zen.SeedModels)*2)
	for _, seed := range zen.SeedModels {
		freeIDs = append(freeIDs, strings.ToLower(seed.ID))
		for _, alias := range seed.Aliases {
			freeIDs = append(freeIDs, strings.ToLower(alias))
		}
	}
	providers.SetFreeModelIDs(freeIDs)
}
