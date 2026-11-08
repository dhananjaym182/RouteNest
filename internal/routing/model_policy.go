package routing

import (
	"log"
	"routenest/internal/config"
	"routenest/internal/domain"
	"routenest/internal/providers"
	"strings"
	"time"
)

// ComboCatalog is the read-only view of named combos that routing consults. The
// combos package implements it and the composition root installs it, so routing
// never imports combos.
type ComboCatalog interface {
	FindCombo(id string) (domain.ModelCombo, bool)
	EnabledCombos() []domain.ModelCombo
}

type noCombos struct{}

func (noCombos) FindCombo(string) (domain.ModelCombo, bool) { return domain.ModelCombo{}, false }
func (noCombos) EnabledCombos() []domain.ModelCombo         { return nil }

var comboCatalog ComboCatalog = noCombos{}

// SetComboCatalog installs the combo source. A nil value restores the empty catalog.
func SetComboCatalog(c ComboCatalog) {
	if c == nil {
		c = noCombos{}
	}
	comboCatalog = c
}

// HasComboCatalog reports whether a combo source has been installed.
func HasComboCatalog() bool {
	_, empty := comboCatalog.(noCombos)
	return !empty
}

var (
	// ConfigProvider supplies the live proxy policy, installed by internal/app.
	// It is nil until then so a missing composition fails loudly.
	ConfigProvider func() *config.Proxy
	ModelsProvider = func() []domain.Model { return nil }
	ModelDisabled  = func(string) bool { return false }
)

func liveConfig() *config.Proxy {
	if ConfigProvider == nil {
		panic("routing: app.Configure() was not called (ConfigProvider unset)")
	}
	return ConfigProvider()
}

// Client-visible model policy: administrator aliases, wildcard exclusions, and
// per-model capability metadata. All three entry points (chat, Responses,
// Anthropic) funnel the requested id through ResolveClientModel before it
// reaches routing, so the policy is enforced in the backend rather than in UI
// labels.

// ResolveClientModel applies alias rewrites and exclusion patterns.
// It returns the id routing should use and a non-empty reason when the request
// must be rejected. The reserved "free" alias and named combos are never
// rewritten or excluded by policy (they are explicit admin constructs).
func ResolveClientModel(raw string) (resolved string, reject string) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return id, ""
	}
	cfg := liveConfig()

	if _, isCombo := comboCatalog.FindCombo(id); !isCombo && id != "free" {
		if alias, ok := cfg.AliasFor(id); ok && (alias.Force || !catalogHasModel(id)) {
			target := alias.Target
			bare, pin, err := ResolveModelReference(target)
			if err != nil {
				return id, err.Error()
			}
			if alias.Provider != "" {
				instances, err := providers.SnapshotInstances()
				if err != nil {
					return id, err.Error()
				}
				explicit, err := resolveCandidatePin(instances, alias.Provider)
				if err != nil {
					return id, err.Error()
				}
				if pin.active() && pin.ProviderID != explicit.ProviderID {
					return id, "model alias has conflicting provider references"
				} else if prefix := cfg.ProviderModelPrefix(string(explicit.ProviderID)); prefix != "" {
					target = prefix + "/" + bare
				} else {
					return id, "provider " + alias.Provider + " has no registered model namespace"
				}
			}
			id = target
		}
	}
	bare, _, err := ResolveModelReference(id)
	if err != nil {
		if _, _, qualified := cfg.SplitProviderModel(id); qualified {
			return id, err.Error()
		}
		bare, _ = providers.SplitPin(id)
	}

	if cfg.ExcludesModel(id) || cfg.ExcludesModel(bare) || (id != raw && cfg.ExcludesModel(raw)) {
		return id, "model " + raw + " is excluded by administrator policy"
	}
	return id, ""
}

// catalogHasModel reports whether id is a real catalog model (so a non-forced
// alias only shadows unknown ids).
func catalogHasModel(id string) bool {
	_, ok := ModelForReference(id)
	return ok
}

// modelEntryMeta returns the capability metadata stored for a model id.
func modelEntryMeta(id string) (model, bool) {
	return ModelForReference(id)
}

// ApplyThinkingPolicy drops reasoning-effort parameters the resolved model does
// not declare. A model without metadata keeps the client's request untouched.
func ApplyThinkingPolicy(params map[string]any, model string) {
	effort, _ := params["reasoning_effort"].(string)
	if effort == "" {
		if r, ok := params["reasoning"].(map[string]any); ok {
			effort, _ = r["effort"].(string)
		}
	}
	if effort == "" {
		return
	}
	m, ok := modelEntryMeta(model)
	if !ok || len(m.ThinkingLevels) == 0 {
		return
	}
	for _, level := range m.ThinkingLevels {
		if strings.EqualFold(level, effort) {
			return
		}
	}
	log.Printf("  thinking policy: model %q does not support effort %q, parameter dropped", model, effort)
	delete(params, "reasoning_effort")
	if r, ok := params["reasoning"].(map[string]any); ok {
		delete(r, "effort")
		if len(r) == 0 {
			delete(params, "reasoning")
		}
	}
}

// ExcludedForListing filters pool models, combos, and alias entries for
// /v1/models and admin listings.
func ExcludedForListing(id string) bool {
	return liveConfig().ExcludesModel(id)
}

// AliasListingEntries returns client-visible alias entries whose targets are
// not themselves excluded or disabled.
func AliasListingEntries(onlyFree bool) []map[string]any {
	cfg := liveConfig()
	out := make([]map[string]any, 0, len(cfg.ModelAliases))
	for _, a := range cfg.ModelAliases {
		resolved, reject := ResolveClientModel(a.Alias)
		if reject != "" || ExcludedForListing(a.Alias) || ExcludedForListing(resolved) {
			continue
		}
		if IsModelDisabled(resolved) || onlyFree && !ModelFreeOnlyEligible(resolved) {
			continue
		}
		out = append(out, map[string]any{
			"id":       a.Alias,
			"object":   "model",
			"created":  time.Now().UnixMilli(),
			"owned_by": "alias",
			"alias_of": a.Target,
		})
	}
	return out
}
