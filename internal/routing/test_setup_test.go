package routing

import (
	"strings"

	"routenest/internal/accounts"
	"routenest/internal/config"
	"routenest/internal/domain"
	"routenest/internal/provideradapters"
	clineadapter "routenest/internal/provideradapters/cline"
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
	ConfigProvider = getProxyConfig
}

func init() {
	ConfigProvider = getProxyConfig
	ModelsProvider = func() []domain.Model {
		return accounts.LoadPool().Models
	}
	SetComboCatalog(testCombos{})
	adapter := clineadapter.New()
	registry, err := provideradapters.NewRegistry(provideradapters.Bundle{
		ID: "cline", Invoker: adapter, FailureClassifier: adapter, ModelDiscoverer: adapter,
	})
	if err != nil {
		panic(err)
	}
	if err := provideradapters.Publish(registry); err != nil {
		panic(err)
	}
}

// testCombos finds combos in the test proxy config. The enabled list stays empty,
// as it did when no combo provider was installed.
type testCombos struct{}

func (testCombos) FindCombo(id string) (domain.ModelCombo, bool) {
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

func (testCombos) EnabledCombos() []domain.ModelCombo { return nil }
