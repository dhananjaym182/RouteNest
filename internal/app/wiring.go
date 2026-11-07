// Package app composes RouteNest's internal services for both CLI and desktop
// entry points.
package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"routenest/internal/accounts"
	"routenest/internal/budget"
	"routenest/internal/buildinfo"
	"routenest/internal/combos"
	"routenest/internal/config"
	"routenest/internal/connections"
	"routenest/internal/credentials"
	"routenest/internal/dispatch"
	"routenest/internal/domain"
	"routenest/internal/egress"
	"routenest/internal/platform"
	"routenest/internal/provideradapters"
	clineadapter "routenest/internal/provideradapters/cline"
	openaicompatadapter "routenest/internal/provideradapters/openaicompat"
	opencodezenadapter "routenest/internal/provideradapters/opencodezen"
	qoderadapter "routenest/internal/provideradapters/qoder"
	"routenest/internal/providerreg"
	"routenest/internal/providers"
	"routenest/internal/providerstate"
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
	if err := configureRuntimeState(); err != nil {
		panic("app: configure provider runtime state: " + err.Error())
	}
	if err := configureCredentials(); err != nil {
		panic("app: publish credentials: " + err.Error())
	}
	if err := configureEgress(); err != nil {
		panic("app: publish egress resolver: " + err.Error())
	}
	bodyOptionsProvider := func() openaicompat.BodyOptions {
		return openaicompat.BodyOptions{
			PassThroughKeys:     routing.PassThroughKeys,
			MinMaxTokens:        routing.MinUpstreamMaxTokens,
			DefaultMaxTokens:    routing.DefaultMaxTokens,
			ModelMaxOutputLimit: routing.ModelMaxOutputLimit,
		}
	}
	openAIAdapter := openaicompatadapter.New(bodyOptionsProvider)
	qoderAdapter := qoderadapter.New()
	clineAdapter := clineadapter.New()
	openCodeAdapter := opencodezenadapter.New()
	adapterRegistry, err := provideradapters.NewRegistry(
		provideradapters.Bundle{ID: "openaicompat", Invoker: openAIAdapter, FailureClassifier: openAIAdapter},
		provideradapters.Bundle{ID: "qoder", Invoker: qoderAdapter, FailureClassifier: qoderAdapter},
		provideradapters.Bundle{ID: "cline", Invoker: clineAdapter, FailureClassifier: clineAdapter, ModelDiscoverer: clineAdapter},
		provideradapters.Bundle{ID: "opencodezen", Invoker: openCodeAdapter, FailureClassifier: openCodeAdapter, ModelDiscoverer: openCodeAdapter},
	)
	if err != nil {
		panic("app: build provider adapter registry: " + err.Error())
	}
	if err := adapterRegistry.ValidateDefinitions(providerreg.Builtin()); err != nil {
		panic("app: validate provider adapter registry: " + err.Error())
	}
	if err := provideradapters.Publish(adapterRegistry); err != nil {
		panic("app: publish provider adapter registry: " + err.Error())
	}

	combos.ConfigMu = &config.Mu
	combos.Config = config.Held
	combos.GetConfig = config.Live
	combos.EnsureConfig = config.Held
	combos.SaveConfig = config.SaveLocked
	combos.CallClineAPIWithAccount = func(ctx context.Context, acc *domain.Account, params map[string]any, stream bool) (*http.Response, *domain.Account, error) {
		return routing.CallClineAPIWithAccountCtx(ctx, acc, params, stream)
	}
	combos.SetModelCooldown = routing.SetModelCooldown
	combos.CallFreeClineAPI = func(params map[string]any, stream bool) (*http.Response, *domain.Account, error) {
		return routing.CallFreeClineAPI(params, stream)
	}

	egress.ConfigProvider = config.Live
	requestlog.Path = platform.ResolveDataPath(requestlogstore.FileName, nil)
	routing.ConfigProvider = config.Live
	routing.ModelsProvider = routing.AllModels
	routing.SetComboCatalog(combos.RoutingCombos{})

	// Cross-package seams that used to be installed by package init. Keeping
	// them here makes the dependency graph explicit and testable.
	accounts.SetProxyConfigProvider(config.Live)
	accounts.SetAffinityErrorClassifier(routing.ClassifyAffinityError)
	connections.RegisterProviderRefPruner(providers.PruneConnectionRefs)

	buildinfo.HTTPClient = egress.Client
	accounts.HTTPClient = egress.Client
	connections.HTTPClient = egress.Client
	connections.IsFreeModel = routing.IsFreeModelID

	providers.TransportProvider = func() http.RoundTripper { return egress.Client.Transport }
	zen.BodyOptionsProvider = bodyOptionsProvider
	providers.ConfigProvider = config.Live
	freeIDs := make([]string, 0, len(zen.SeedModels)*2)
	for _, seed := range zen.SeedModels {
		freeIDs = append(freeIDs, strings.ToLower(seed.ID))
		for _, alias := range seed.Aliases {
			freeIDs = append(freeIDs, strings.ToLower(alias))
		}
	}
	providers.SetFreeModelIDs(freeIDs)

	proxycore.ConfigProvider = config.Live
	proxycore.RecordUsage = RecordUsage
	if err := configureDispatch(); err != nil {
		panic("app: publish dispatch runtime: " + err.Error())
	}

	if err := ValidateComposition(); err != nil {
		panic(err)
	}
}

// ValidateComposition reports whether every required cross-package seam is
// installed. Configure calls it after wiring, so an omitted assignment fails at
// startup instead of turning into a nil accessor panic or a silent no-op on the
// request path.
func ValidateComposition() error {
	missing := make([]string, 0, 8)
	check := func(name string, ok bool) {
		if !ok {
			missing = append(missing, name)
		}
	}
	check("accounts proxy config provider", accounts.ProxyConfigProvider() != nil)
	check("egress.ConfigProvider", egress.ConfigProvider != nil)
	check("routing.ConfigProvider", routing.ConfigProvider != nil)
	check("providers.ConfigProvider", providers.ConfigProvider != nil)
	check("proxycore.ConfigProvider", proxycore.ConfigProvider != nil)
	check("proxycore.RecordUsage", proxycore.RecordUsage != nil)
	check("routing combo catalogue", routing.HasComboCatalog())
	check("provider adapter registry", provideradapters.HasDefault())
	check("provider budget store", budget.HasDefault())
	check("provider state store", providerstate.HasDefault())
	check("credentials registry", credentials.HasDefault())
	check("egress resolver", egress.HasDefault())
	check("dispatch runtime", dispatch.HasRuntime())
	if len(missing) > 0 {
		return fmt.Errorf("app: incomplete composition: %s", strings.Join(missing, ", "))
	}
	return nil
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
