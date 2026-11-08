package config

import (
	"encoding/json"
	"os"

	"routenest/internal/domain"
	"routenest/internal/platform"
)

// ProxyPath is the data file holding global proxy settings and combos.
const ProxyPath = ".routenest-config.json"

// DispatchMode controls the reversible Phase 2 routing cutover. Shadow builds
// and compares the unified plan while retaining the legacy execution path;
// enforce executes that plan; legacy is the explicit rollback mode.
type DispatchMode string

const (
	DispatchModeLegacy  DispatchMode = "legacy"
	DispatchModeShadow  DispatchMode = "shadow"
	DispatchModeEnforce DispatchMode = "enforce"
)

func (mode DispatchMode) Valid() bool {
	switch mode {
	case DispatchModeLegacy, DispatchModeShadow, DispatchModeEnforce:
		return true
	default:
		return false
	}
}

func (mode DispatchMode) normalized() DispatchMode {
	if mode.Valid() {
		return mode
	}
	return DispatchModeShadow
}

// Proxy is the persisted global proxy configuration.
type Proxy struct {
	Strategy     string            `json:"strategy"`
	DispatchMode DispatchMode      `json:"dispatchMode"`
	Headers      map[string]string `json:"headers"`
	// ModelChain is the administrator-configured fallback order used when a
	// model is cooling down. Empty means the built-in free chain.
	ModelChain []string `json:"modelChain,omitempty"`
	// OnlyFree limits model listings to free models.
	OnlyFree bool `json:"onlyFree"`
	// Combos are named ordered fallback combinations stored in this file.
	Combos []domain.ModelCombo `json:"combos,omitempty"`

	// MaxRequestBodyBytes rejects oversized client request bodies. 0 disables.
	MaxRequestBodyBytes int64 `json:"maxRequestBodyBytes,omitempty"`
	// PassthroughHeaders forwards safe upstream response headers to clients.
	PassthroughHeaders bool `json:"passthroughHeaders,omitempty"`
	// NonstreamKeepaliveSeconds emits blank lines while a non-stream response
	// waits on the upstream. 0 disables.
	NonstreamKeepaliveSeconds int `json:"nonstreamKeepaliveSeconds,omitempty"`

	Retry             RetryPolicy           `json:"retry,omitempty"`
	Stream            StreamPolicy          `json:"stream,omitempty"`
	SessionAffinity   SessionAffinityPolicy `json:"sessionAffinity,omitempty"`
	RuntimeState      RuntimeStatePolicy    `json:"runtimeState"`
	ModelAliases      []ModelAlias          `json:"modelAliases,omitempty"`
	ExcludedModels    []string              `json:"excludedModels,omitempty"`
	RequestErrorRules []RequestErrorRule    `json:"requestErrorRules,omitempty"`

	// ProviderModelPrefixes binds compact client model namespaces to stable
	// provider IDs. Deleted provider bindings remain reserved to prevent reuse.
	ProviderModelPrefixes map[string]string `json:"providerModelPrefixes,omitempty"`
}

// DefaultProxy returns the built-in proxy configuration.
func DefaultProxy() *Proxy {
	return &Proxy{
		Strategy:     "round_robin",
		DispatchMode: DispatchModeShadow,
		RuntimeState: DefaultRuntimeStatePolicy(),
		Headers: map[string]string{
			"User-Agent":         "Cline/3.0.47",
			"HTTP-Referer":       "https://cline.bot",
			"X-Title":            "Cline",
			"X-IS-MULTIROOT":     "false",
			"X-CLIENT-TYPE":      "cline-cli",
			"X-CLIENT-VERSION":   "3.0.47",
			"X-PLATFORM":         "terminal",
			"X-PLATFORM-VERSION": "3.0.47",
			"X-CORE-VERSION":     "0.0.66",
		},
	}
}

// Normalize restores an unknown strategy and clamps policy values.
func (c *Proxy) Normalize() {
	c.DispatchMode = c.DispatchMode.normalized()
	c.RuntimeState.Normalize()
	switch c.Strategy {
	case "round_robin", "weighted_round_robin", "fill", "random", "quality":
	default:
		c.Strategy = "round_robin"
	}
	if c.MaxRequestBodyBytes < 0 {
		c.MaxRequestBodyBytes = 0
	}
	if c.NonstreamKeepaliveSeconds < 0 {
		c.NonstreamKeepaliveSeconds = 0
	}
	if c.NonstreamKeepaliveSeconds > 300 {
		c.NonstreamKeepaliveSeconds = 300
	}
	c.Retry.Normalize()
	c.Stream.Normalize()
	c.SessionAffinity.Normalize()
	c.NormalizeModelPolicies()
}

// LoadProxy loads path over the built-in defaults so absent fields keep their
// default values. A decode error is returned with the partially decoded config
// so the caller can back up the corrupt file, matching the previous behavior.
func LoadProxy(path string) (*Proxy, error) {
	cfg := DefaultProxy()
	var decodeErr error
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			decodeErr = err
		}
	}
	cfg.Normalize()
	return cfg, decodeErr
}

// SaveProxy persists cfg atomically.
func SaveProxy(path string, cfg *Proxy) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return platform.WriteFileAtomic(path, data, 0600)
}
