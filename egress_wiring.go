package main

import (
	"net/http"
	"strings"

	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/buildinfo"
	"cline-go-proxy/internal/connections"
	"cline-go-proxy/internal/egress"
	"cline-go-proxy/internal/providers"
	"cline-go-proxy/internal/requestlog"
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
	zen.IsModelGoneError = isModelGoneError
	zen.MarkModelGone = markModelGone

	buildinfo.HTTPClient = egress.Client
	accounts.HTTPClient = egress.Client
	connections.HTTPClient = egress.Client
	connections.IsFreeModel = isFreeModelID

	providers.TransportProvider = func() http.RoundTripper { return egress.Client.Transport }
	bodyOptionsProvider := func() openaicompat.BodyOptions {
		return openaicompat.BodyOptions{
			PassThroughKeys:     passThroughKeys,
			MinMaxTokens:        minUpstreamMaxTokens,
			DefaultMaxTokens:    defaultMaxTokens,
			ModelMaxOutputLimit: modelMaxOutputLimit,
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
