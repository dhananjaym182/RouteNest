package adminapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	configpkg "routenest/internal/config"
	"routenest/internal/domain"
	"routenest/internal/httpx"
)

// Config serves proxy runtime configuration administration. All state lives in
// the composition layer; callbacks own locking, persistence, and listener life.
type Config struct {
	Proxy              func() *configpkg.Proxy
	SetProxy           func(*configpkg.Proxy)
	Models             func() []domain.Model
	DefaultModel       func() string
	SetDefaultModel    func(string)
	SetListenHost      func(string) error
	LocalIPs           func() []string
	ListenHost         func() string
	ListenPort         func() int
	EffectiveAdminHost func(string) string
	Version            string
	Build              func() map[string]any
	ZenHeaders         func() map[string]string
	PoolPath           string
	HasPassword        func() bool
	// RestartListener rebinds the HTTP listener to a new host on the same port.
	RestartListener func(host string, port int) error
	// FatalListenerFailure terminates the process when both a listener change
	// and its rollback fail, leaving no trustworthy administration endpoint.
	FatalListenerFailure func(error)
	ListenerOperations   *ListenerOperationStore
	Translate            func(*http.Request, string) string
	Logf                 func(string, ...any)
}

func (h *Config) msg(r *http.Request, key string) string {
	if h.Translate != nil {
		return h.Translate(r, key)
	}
	return key
}

func (h *Config) logf(format string, args ...any) {
	if h.Logf != nil {
		h.Logf(format, args...)
	}
}

func (h *Config) address(host string) string {
	port := 0
	if h.ListenPort != nil {
		port = h.ListenPort()
	}
	if h.EffectiveAdminHost != nil {
		host = h.EffectiveAdminHost(host)
	}
	return net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))
}

// Get handles GET /admin/api/config.
func (h *Config) Get(w http.ResponseWriter, r *http.Request) {
	cfg := h.Proxy()
	runtimeState := cfg.RuntimeState
	runtimeState.Normalize()
	host := ""
	if h.ListenHost != nil {
		host = h.ListenHost()
	}
	data := map[string]any{
		"address":      h.address(host),
		"host":         host,
		"strategy":     cfg.Strategy,
		"dispatchMode": cfg.DispatchMode,
		"modelChain":   cfg.ModelChain,
		"version":      h.Version,
		"poolPath":     h.PoolPath,
		"defaultModel": h.DefaultModel(),
		"headers":      cfg.Headers,
		"onlyFree":     cfg.OnlyFree,
		// Tuning policies surfaced for the admin UI (task #68).
		"retry":                     cfg.Retry,
		"stream":                    cfg.Stream,
		"sessionAffinity":           cfg.SessionAffinity,
		"runtimeState":              runtimeState,
		"modelAliases":              cfg.ModelAliases,
		"excludedModels":            cfg.ExcludedModels,
		"requestErrorRules":         cfg.RequestErrorRules,
		"maxRequestBodyBytes":       cfg.MaxRequestBodyBytes,
		"passthroughHeaders":        cfg.PassthroughHeaders,
		"nonstreamKeepaliveSeconds": cfg.NonstreamKeepaliveSeconds,
	}
	if h.Build != nil {
		if b := h.Build(); len(b) > 0 {
			data["build"] = b
		}
	}
	if h.ZenHeaders != nil {
		data["zenHeaders"] = h.ZenHeaders()
	}
	if h.LocalIPs != nil {
		data["localIPs"] = h.LocalIPs()
	}
	if h.HasPassword != nil {
		data["hasPassword"] = h.HasPassword()
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: data})
}

func (h *Config) ListenerOperation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteAPI(w, http.StatusMethodNotAllowed, httpx.Response{Error: h.msg(r, "method_not_allowed")})
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	operation, ok := h.ListenerOperations.Get(id)
	if !ok {
		httpx.WriteAPI(w, http.StatusNotFound, httpx.Response{Error: "listener operation not found"})
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"operation": operation}})
}

// Update handles POST /admin/api/config with a partial configuration patch.
func (h *Config) Update(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteAPI(w, http.StatusMethodNotAllowed, httpx.Response{Error: h.msg(r, "method_not_allowed")})
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: err.Error()})
		return
	}
	defer r.Body.Close()

	var chainUnverified []string

	var req struct {
		Strategy     string                  `json:"strategy"`
		DispatchMode *configpkg.DispatchMode `json:"dispatchMode"`
		Headers      map[string]string       `json:"headers"`
		DefaultModel string                  `json:"defaultModel"`
		Host         string                  `json:"host"`
		ModelChain   *[]string               `json:"modelChain"`
		OnlyFree     *bool                   `json:"onlyFree"`

		Retry           *configpkg.RetryPolicy           `json:"retry"`
		Stream          *configpkg.StreamPolicy          `json:"stream"`
		SessionAffinity *configpkg.SessionAffinityPolicy `json:"sessionAffinity"`
		RuntimeState    *struct {
			OutagePolicy          *configpkg.RuntimeStateOutagePolicy `json:"outagePolicy"`
			AcknowledgeFailOpen   *bool                               `json:"acknowledgeFailOpen"`
			DeploymentTopology    *configpkg.RuntimeStateTopology     `json:"deploymentTopology"`
			SnapshotMaxAgeSeconds *int                                `json:"snapshotMaxAgeSeconds"`
		} `json:"runtimeState"`
		ModelAliases        *[]configpkg.ModelAlias       `json:"modelAliases"`
		ExcludedModels      *[]string                     `json:"excludedModels"`
		RequestErrorRules   *[]configpkg.RequestErrorRule `json:"requestErrorRules"`
		MaxRequestBodyBytes *int64                        `json:"maxRequestBodyBytes"`
		PassthroughHeaders  *bool                         `json:"passthroughHeaders"`
		NonstreamKeepalive  *int                          `json:"nonstreamKeepaliveSeconds"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: h.msg(r, "invalid_json")})
		return
	}
	if req.DispatchMode != nil && !req.DispatchMode.Valid() {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: h.msg(r, "invalid_dispatch_mode")})
		return
	}

	cfg := h.Proxy().Clone()
	changed := false
	restarting := false

	// Validate the merged durability policy before applying any patch. Absent
	// fields retain their effective values; explicitly invalid fields are a 400.
	runtimeState := cfg.RuntimeState
	runtimeState.Normalize()
	if req.RuntimeState != nil {
		runtimeState.OutagePolicy = valueOr(req.RuntimeState.OutagePolicy, runtimeState.OutagePolicy)
		runtimeState.AcknowledgeFailOpen = valueOr(req.RuntimeState.AcknowledgeFailOpen, runtimeState.AcknowledgeFailOpen)
		runtimeState.DeploymentTopology = valueOr(req.RuntimeState.DeploymentTopology, runtimeState.DeploymentTopology)
		runtimeState.SnapshotMaxAgeSeconds = valueOr(req.RuntimeState.SnapshotMaxAgeSeconds, runtimeState.SnapshotMaxAgeSeconds)
		if err := runtimeState.Validate(); err != nil {
			httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: err.Error()})
			return
		}
		cfg.RuntimeState = runtimeState
		changed = true
	}

	// Validate model policies before touching the live config: a bad alias,
	// duplicate, or unusable error rule must be a 400, not a silent drop.
	// scratch is built from fresh slice copies so validation + in-place
	// normalization never mutate the live config's backing arrays.
	if req.ModelAliases != nil || req.ExcludedModels != nil || req.RequestErrorRules != nil {
		scratch := configpkg.Proxy{
			ModelAliases:      copyAliases(valueOr(req.ModelAliases, cfg.ModelAliases)),
			ExcludedModels:    copyStrings(valueOr(req.ExcludedModels, cfg.ExcludedModels)),
			RequestErrorRules: copyRules(valueOr(req.RequestErrorRules, cfg.RequestErrorRules)),
		}
		if err := scratch.ValidateModelPolicies(); err != nil {
			httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: err.Error()})
			return
		}
		scratch.NormalizeModelPolicies()
		if req.ModelAliases != nil {
			cfg.ModelAliases = scratch.ModelAliases
			changed = true
		}
		if req.ExcludedModels != nil {
			cfg.ExcludedModels = scratch.ExcludedModels
			changed = true
		}
		if req.RequestErrorRules != nil {
			cfg.RequestErrorRules = scratch.RequestErrorRules
			changed = true
		}
	}

	if req.Strategy != "" {
		switch req.Strategy {
		case "round_robin", "weighted_round_robin", "fill", "random", "quality":
			cfg.Strategy = req.Strategy
			changed = true
		default:
			httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: h.msg(r, "invalid_strategy")})
			return
		}
	}

	if req.DispatchMode != nil {
		cfg.DispatchMode = *req.DispatchMode
		changed = true
	}

	if req.Headers != nil {
		for k, v := range req.Headers {
			cfg.Headers[k] = v
		}
		changed = true
	}

	if req.ModelChain != nil {
		known := map[string]bool{"free": true}
		if h.Models != nil {
			for _, m := range h.Models() {
				known[m.ID] = true
			}
		}
		var chain []string
		var unverified []string
		seen := map[string]bool{}
		for _, raw := range *req.ModelChain {
			id := strings.TrimSpace(raw)
			if id == "" || seen[id] {
				continue
			}
			if !known[id] {
				unverified = append(unverified, id)
			}
			seen[id] = true
			chain = append(chain, id)
		}
		cfg.ModelChain = chain
		if len(unverified) > 0 {
			h.logf("  model chain: kept out-of-list model(s) %v (not in synced list, may still work)", unverified)
		}
		changed = true
		chainUnverified = unverified
	}

	if req.OnlyFree != nil {
		cfg.OnlyFree = *req.OnlyFree
		changed = true
	}

	if req.Retry != nil {
		p := *req.Retry
		p.Normalize()
		cfg.Retry = p
		changed = true
	}
	if req.Stream != nil {
		p := *req.Stream
		p.Normalize()
		cfg.Stream = p
		changed = true
	}
	if req.SessionAffinity != nil {
		p := *req.SessionAffinity
		p.Normalize()
		cfg.SessionAffinity = p
		changed = true
	}
	if req.MaxRequestBodyBytes != nil {
		v := *req.MaxRequestBodyBytes
		if v < 0 {
			v = 0
		}
		cfg.MaxRequestBodyBytes = v
		changed = true
	}
	if req.PassthroughHeaders != nil {
		cfg.PassthroughHeaders = *req.PassthroughHeaders
		changed = true
	}
	if req.NonstreamKeepalive != nil {
		v := *req.NonstreamKeepalive
		if v < 0 {
			v = 0
		}
		if v > 300 {
			v = 300
		}
		cfg.NonstreamKeepaliveSeconds = v
		changed = true
	}

	if req.DefaultModel != "" {
		found := false
		if h.Models != nil {
			for _, m := range h.Models() {
				if m.ID == req.DefaultModel {
					found = true
					break
				}
			}
		}
		if !found {
			httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: h.msg(r, "invalid_default_model")})
			return
		}
		if h.SetDefaultModel != nil {
			h.SetDefaultModel(req.DefaultModel)
		}
	}

	currentHost := ""
	if h.ListenHost != nil {
		currentHost = h.ListenHost()
	}
	var listenerOperation ListenerOperation
	if req.Host != "" {
		valid := req.Host == "127.0.0.1" || req.Host == "localhost" || req.Host == "::1"
		if !valid {
			httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: h.msg(r, "invalid_host")})
			return
		}
		if req.Host != currentHost {
			if h.ListenerOperations == nil || h.RestartListener == nil || h.SetListenHost == nil {
				httpx.WriteAPI(w, http.StatusServiceUnavailable, httpx.Response{Error: "listener reconfiguration unavailable"})
				return
			}
			var operationErr error
			listenerOperation, operationErr = h.ListenerOperations.Start(req.Host)
			if operationErr != nil {
				httpx.WriteAPI(w, http.StatusServiceUnavailable, httpx.Response{Error: operationErr.Error()})
				return
			}
			restarting = true
		}
	}

	if changed && h.SetProxy != nil {
		h.SetProxy(cfg)
	}

	// Restart is asynchronous. Persistence happens only after the new listener
	// binds and passes its local readiness probe; persistence failure rolls the
	// active listener back to the prior host.
	host := currentHost
	if restarting {
		port := 0
		if h.ListenPort != nil {
			port = h.ListenPort()
		}
		oldHost, newHost, operationID := currentHost, req.Host, listenerOperation.ID
		go func() {
			if err := h.RestartListener(newHost, port); err != nil {
				h.ListenerOperations.Fail(operationID, err)
				h.logf("Listener restart failed: %v", err)
				return
			}
			if err := h.SetListenHost(newHost); err != nil {
				rollbackErr := h.RestartListener(oldHost, port)
				if rollbackErr != nil {
					err = fmt.Errorf("persist listener host: %v; rollback failed: %w", err, rollbackErr)
				} else {
					err = fmt.Errorf("persist listener host: %w", err)
				}
				h.ListenerOperations.Fail(operationID, err)
				h.logf("Listener persistence failed: %v", err)
				if rollbackErr != nil && h.FatalListenerFailure != nil {
					h.FatalListenerFailure(err)
				}
				return
			}
			h.ListenerOperations.Succeed(operationID)
		}()
	}

	status := http.StatusOK
	if restarting {
		status = http.StatusAccepted
	}
	data := map[string]any{
		"strategy":                  cfg.Strategy,
		"dispatchMode":              cfg.DispatchMode,
		"headers":                   cfg.Headers,
		"onlyFree":                  cfg.OnlyFree,
		"defaultModel":              h.DefaultModel(),
		"host":                      host,
		"address":                   h.address(host),
		"restarting":                restarting,
		"chainUnverified":           chainUnverified,
		"retry":                     cfg.Retry,
		"stream":                    cfg.Stream,
		"sessionAffinity":           cfg.SessionAffinity,
		"runtimeState":              runtimeState,
		"modelAliases":              cfg.ModelAliases,
		"excludedModels":            cfg.ExcludedModels,
		"requestErrorRules":         cfg.RequestErrorRules,
		"maxRequestBodyBytes":       cfg.MaxRequestBodyBytes,
		"passthroughHeaders":        cfg.PassthroughHeaders,
		"nonstreamKeepaliveSeconds": cfg.NonstreamKeepaliveSeconds,
	}
	if restarting {
		data["listenerOperation"] = listenerOperation
	}
	httpx.WriteAPI(w, status, httpx.Response{Success: true, Data: data})
}

// valueOr picks the patch value when present, else the current config value.
func valueOr[T any](patch *T, current T) T {
	if patch != nil {
		return *patch
	}
	return current
}

// The copy helpers detach the scratch policy slices from the live config so
// in-place normalization cannot reorder shared backing arrays.
func copyAliases(in []configpkg.ModelAlias) []configpkg.ModelAlias {
	return append([]configpkg.ModelAlias(nil), in...)
}

func copyStrings(in []string) []string {
	return append([]string(nil), in...)
}

func copyRules(in []configpkg.RequestErrorRule) []configpkg.RequestErrorRule {
	out := make([]configpkg.RequestErrorRule, 0, len(in))
	for _, r := range in {
		r.StatusCodes = append([]int(nil), r.StatusCodes...)
		out = append(out, r)
	}
	return out
}
