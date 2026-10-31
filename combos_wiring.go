package main

import (
	"cline-go-proxy/internal/routing"
	"context"
	"errors"
	"net/http"

	"cline-go-proxy/internal/combos"
	"cline-go-proxy/internal/config"
	"cline-go-proxy/internal/domain"
)

// init wires the combos package to root-owned config, dispatch and error state.
func init() {
	combos.ConfigMu = &proxyConfigMu
	combos.Config = func() *config.Proxy { return proxyConfig }
	combos.GetConfig = getProxyConfig
	combos.EnsureConfig = func() *config.Proxy {
		if proxyConfig == nil {
			proxyConfig = defaultProxyConfig()
		}
		return proxyConfig
	}
	combos.SaveConfig = saveProxyConfigLocked

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
}
