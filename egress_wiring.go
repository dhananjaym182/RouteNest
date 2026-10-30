package main

import (
	"net/http"
	"strings"

	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/buildinfo"
	"cline-go-proxy/internal/connections"
	"cline-go-proxy/internal/egress"
	"cline-go-proxy/internal/providers"
	"cline-go-proxy/internal/upstream/openaicompat"
)

// init connects the leaf egress package to root-owned data-path migration and
// injects its stable client into all outbound consumers.
func init() {
	egress.DataPathResolver = func(name string) string { return resolveDataPath(name) }
	egress.ConfigProvider = getProxyConfig

	buildinfo.HTTPClient = egress.Client
	accounts.HTTPClient = egress.Client
	connections.HTTPClient = egress.Client
	connections.IsFreeModel = isFreeModelID

	providers.TransportProvider = func() http.RoundTripper { return egress.Client.Transport }
	providers.BodyOptionsProvider = func() openaicompat.BodyOptions {
		return openaicompat.BodyOptions{
			PassThroughKeys:     passThroughKeys,
			MinMaxTokens:        minUpstreamMaxTokens,
			DefaultMaxTokens:    defaultMaxTokens,
			ModelMaxOutputLimit: modelMaxOutputLimit,
		}
	}
	providers.ConfigProvider = getProxyConfig
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
