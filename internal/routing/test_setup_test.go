package routing

import (
	"strings"

	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/config"
	"cline-go-proxy/internal/domain"
)

type Model = domain.Model
type AccountPool = domain.AccountPool
type Account = domain.Account

var testProxyConfig = config.DefaultProxy()

func defaultProxyConfig() *config.Proxy {
	return config.DefaultProxy()
}

func getProxyConfig() *config.Proxy {
	return testProxyConfig
}

func setProxyConfig(cfg *config.Proxy) {
	testProxyConfig = cfg
}

func init() {
	ConfigProvider = getProxyConfig
	ModelsProvider = func() []domain.Model {
		return accounts.LoadPool().Models
	}
	FindCombo = func(id string) (domain.ModelCombo, bool) {
		id = strings.TrimSpace(id)
		if id == "" {
			return domain.ModelCombo{}, false
		}
		for _, combo := range testProxyConfig.Combos {
			if combo.ID == id {
				return combo, true
			}
		}
		return domain.ModelCombo{}, false
	}
}
