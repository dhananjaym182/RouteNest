package main

import (
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
		var e *clineAccountUnavailableError
		return errors.As(err, &e)
	}
	combos.NewUnavailableError = func(message string) error {
		return &freeModelUnavailableError{message: message}
	}
	combos.CallClineAPIWithAccount = func(ctx context.Context, acc *domain.Account, params map[string]any, stream bool) (*http.Response, *domain.Account, error) {
		return callClineAPIWithAccountCtx(ctx, acc, params, stream)
	}
	combos.IsDeterministicRequestError = isDeterministicRequestError
	combos.SetModelCooldown = setModelCooldown
	combos.TruncateEmail = truncateEmail
	combos.CallFreeClineAPI = func(params map[string]any, stream bool) (*http.Response, *domain.Account, error) {
		return callFreeClineAPI(params, stream)
	}
	combos.WithModel = withModel
}
