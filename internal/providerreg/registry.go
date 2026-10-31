// Package providerreg is the single authority for known provider source
// definitions (presets). The catalogue is built once from two provenance sets:
//
//   - manual/*.json: hand-maintained RouteNest definitions (provenance "manual");
//   - imported.json: definitions generated from the ExtremeRouter registry by
//     tools/gen-provider-presets.mjs (provenance "imported").
//
// Presets are compile-time data: editing a JSON file requires rebuilding the
// binary. Runtime custom providers are stored separately in
// .routenest-providers.json and apply without a rebuild.
package providerreg

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"sort"
	"strings"
	"sync"

	"routenest/internal/deviceflow"
)

// Provenance values record which source a definition came from.
const (
	ProvenanceManual   = "manual"
	ProvenanceImported = "imported"
)

// DefaultProtocol is the protocol ID of plain OpenAI-compatible chat, which
// presets express by leaving Protocol empty.
const DefaultProtocol = "openai-chat"

//go:embed manual/*.json
var manualFS embed.FS

//go:embed imported.json
var importedFS embed.FS

// ModelsFetcher describes an upstream model-catalog endpoint.
type ModelsFetcher struct {
	URL  string `json:"url"`
	Type string `json:"type"` // "openai" (the only supported catalog format)
}

// Preset is the data-only definition of a known provider. Provider-level hints
// such as FreeTier and Kind describe the provider offering; they are not
// per-model access entitlements and must not be used as such.
type Preset struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"baseURL"`
	// Protocol is the transport protocol. Empty means OpenAI-compatible chat;
	// "qoder" selects the signed, non-OpenAI Qoder protocol.
	Protocol string            `json:"protocol,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Notes    string            `json:"notes,omitempty"`
	// FreeTier is a provider-level UI hint, not a per-model entitlement.
	FreeTier bool `json:"freeTier"`
	// ModelIDs seeds the provider's catalog; models are still classified by
	// the import path before they become routable.
	ModelIDs      []string       `json:"modelIds,omitempty"`
	ModelsFetcher *ModelsFetcher `json:"modelsFetcher,omitempty"`
	// Kind is the UI grouping hint: free, freeTier, hasFree, local, or other.
	Kind string `json:"kind,omitempty"`
	// AuthFlow is the ID of the device/browser login flow (internal/deviceflow)
	// that authenticates this provider. Empty means the provider uses an API key
	// or no authentication.
	AuthFlow string `json:"authFlow,omitempty"`
	// LoginFlow is derived from AuthFlow (AuthFlow != "") and is not stored in
	// preset files. It is kept for the admin API response.
	LoginFlow bool `json:"loginFlow,omitempty"`
	// Provenance is derived from the source file set and is not stored in
	// preset files.
	Provenance string `json:"provenance,omitempty"`
}

// Registry is an immutable preset catalogue with lookup indexes. Indexes are
// built once in New; every accessor returns copies.
type Registry struct {
	byID        map[string]Preset
	byBaseURL   map[string][]string
	byAuthFlow  map[string][]string
	byProtocol  map[string][]string
	protocolFor map[string]string // normalized base URL -> preset protocol
}

func clonePreset(p Preset) Preset {
	cp := p
	cp.ModelIDs = append([]string(nil), p.ModelIDs...)
	if p.Headers != nil {
		cp.Headers = make(map[string]string, len(p.Headers))
		for key, value := range p.Headers {
			cp.Headers[key] = value
		}
	}
	if p.ModelsFetcher != nil {
		fetcher := *p.ModelsFetcher
		cp.ModelsFetcher = &fetcher
	}
	return cp
}

// NormalizeBaseURL canonicalizes a base URL for matching: lower case, no
// trailing slash, and no trailing /chat/completions. Connection and provider
// records match presets through this form.
func NormalizeBaseURL(u string) string {
	u = strings.ToLower(strings.TrimSpace(u))
	u = strings.TrimRight(u, "/")
	return strings.TrimSuffix(u, "/chat/completions")
}

// effectiveProtocol returns the protocol ID used for indexing. Empty means
// DefaultProtocol.
func effectiveProtocol(p Preset) string {
	if p.Protocol == "" {
		return DefaultProtocol
	}
	return p.Protocol
}

// New validates presets and returns an immutable registry with its indexes.
func New(presets map[string]Preset) (*Registry, error) {
	if err := Validate(presets); err != nil {
		return nil, err
	}
	reg := &Registry{
		byID:        make(map[string]Preset, len(presets)),
		byBaseURL:   map[string][]string{},
		byAuthFlow:  map[string][]string{},
		byProtocol:  map[string][]string{},
		protocolFor: map[string]string{},
	}
	for id, p := range presets {
		cp := clonePreset(p)
		reg.byID[id] = cp
		base := NormalizeBaseURL(cp.BaseURL)
		reg.byBaseURL[base] = append(reg.byBaseURL[base], id)
		if cp.AuthFlow != "" {
			reg.byAuthFlow[cp.AuthFlow] = append(reg.byAuthFlow[cp.AuthFlow], id)
		}
		proto := effectiveProtocol(cp)
		reg.byProtocol[proto] = append(reg.byProtocol[proto], id)
	}
	for base, ids := range reg.byBaseURL {
		sort.Strings(ids)
		// Presets that share a base URL must agree on the protocol; otherwise
		// the base URL cannot identify the protocol.
		want := effectiveProtocol(reg.byID[ids[0]])
		for _, id := range ids[1:] {
			if got := effectiveProtocol(reg.byID[id]); got != want {
				return nil, fmt.Errorf("base URL %q shared by presets %q (%s) and %q (%s) with different protocols", base, ids[0], want, id, got)
			}
		}
		// Only non-default protocols are reported by ProtocolForBaseURL, matching
		// the historical lookup where plain OpenAI chat has no protocol.
		if reg.byID[ids[0]].Protocol != "" {
			reg.protocolFor[base] = reg.byID[ids[0]].Protocol
		}
	}
	for k := range reg.byAuthFlow {
		sort.Strings(reg.byAuthFlow[k])
	}
	for k := range reg.byProtocol {
		sort.Strings(reg.byProtocol[k])
	}
	return reg, nil
}

// Get returns a preset by ID.
func (r *Registry) Get(id string) (Preset, bool) {
	p, ok := r.byID[id]
	return clonePreset(p), ok
}

// All returns a copy of every preset.
func (r *Registry) All() map[string]Preset {
	out := make(map[string]Preset, len(r.byID))
	for id, p := range r.byID {
		out[id] = clonePreset(p)
	}
	return out
}

// IDs returns the preset IDs in sorted order.
func (r *Registry) IDs() []string {
	ids := make([]string, 0, len(r.byID))
	for id := range r.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Len returns the number of presets.
func (r *Registry) Len() int { return len(r.byID) }

// ByBaseURL returns the sorted IDs of presets whose normalized base URL equals
// the normalized form of base. The result is a copy.
func (r *Registry) ByBaseURL(base string) []string {
	return append([]string(nil), r.byBaseURL[NormalizeBaseURL(base)]...)
}

// ProtocolForBaseURL returns the non-default protocol of the preset whose base
// URL matches, or "" for plain OpenAI chat and unknown URLs. Connection and
// provider records use this fallback because they carry no preset ID.
func (r *Registry) ProtocolForBaseURL(base string) string {
	return r.protocolFor[NormalizeBaseURL(base)]
}

// ByAuthFlow returns the sorted IDs of presets that use the given auth flow.
func (r *Registry) ByAuthFlow(flow string) []string {
	return append([]string(nil), r.byAuthFlow[flow]...)
}

// ByProtocol returns the sorted IDs of presets that use the given protocol ID
// (DefaultProtocol for plain OpenAI chat).
func (r *Registry) ByProtocol(protocol string) []string {
	if protocol == "" {
		protocol = DefaultProtocol
	}
	return append([]string(nil), r.byProtocol[protocol]...)
}

var builtinKinds = map[string]bool{"": true, "free": true, "freeTier": true, "hasFree": true, "local": true, "other": true}

// knownFetcherTypes lists catalog fetcher implementations that exist in the
// codebase. Providers with a specialized fetcher still speak OpenAI-compatible
// chat; only their catalog format differs. The exception is "qoder", whose
// fetcher reads /algo/api/v2/model/list for the signed Qoder protocol.
var knownFetcherTypes = map[string]bool{
	"":              true,
	"openai":        true,
	"agentrouter":   true,
	"bynara":        true,
	"hcnsec":        true,
	"inxora":        true,
	"mimo-free":     true,
	"opencode-free": true,
	// qoder reads its catalog from /algo/api/v2/model/list.
	"qoder": true,
}

// Validate checks every preset and its cross-references. The map key must equal
// the preset ID. Auth flow IDs must name a built-in device-flow spec.
func Validate(presets map[string]Preset) error {
	flows := deviceflow.Specs()
	seenIDs := make(map[string]string, len(presets))
	for key, p := range presets {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("preset with empty key")
		}
		if p.ID != "" && p.ID != key {
			return fmt.Errorf("preset %q: id %q does not match key", key, p.ID)
		}
		if prev, dup := seenIDs[p.ID]; dup {
			return fmt.Errorf("preset id %q duplicated (%s and %s)", p.ID, prev, key)
		}
		seenIDs[p.ID] = key
		if strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("preset %q: name is required", key)
		}
		u, err := url.Parse(p.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("preset %q: baseURL %q must be an absolute http(s) URL", key, p.BaseURL)
		}
		if !builtinKinds[p.Kind] {
			return fmt.Errorf("preset %q: unknown kind %q", key, p.Kind)
		}
		if p.Kind == "free" && !p.FreeTier {
			return fmt.Errorf("preset %q: kind free requires freeTier", key)
		}
		// "qoder" is a signed, non-OpenAI protocol implemented in
		// internal/upstream/qoder.
		if p.Protocol != "" && p.Protocol != DefaultProtocol && p.Protocol != "qoder" {
			return fmt.Errorf("preset %q: unsupported protocol %q", key, p.Protocol)
		}
		if p.AuthFlow != "" {
			if _, ok := flows[p.AuthFlow]; !ok {
				return fmt.Errorf("preset %q: authFlow %q has no device-flow spec", key, p.AuthFlow)
			}
		}
		if p.LoginFlow != (p.AuthFlow != "") {
			return fmt.Errorf("preset %q: loginFlow must be true exactly when authFlow is set", key)
		}
		if p.Provenance != "" && p.Provenance != ProvenanceManual && p.Provenance != ProvenanceImported {
			return fmt.Errorf("preset %q: unknown provenance %q", key, p.Provenance)
		}
		if p.ModelsFetcher != nil {
			fu, err := url.Parse(p.ModelsFetcher.URL)
			if err != nil || (fu.Scheme != "http" && fu.Scheme != "https") || fu.Host == "" {
				return fmt.Errorf("preset %q: modelsFetcher URL %q must be absolute", key, p.ModelsFetcher.URL)
			}
			if !knownFetcherTypes[p.ModelsFetcher.Type] {
				return fmt.Errorf("preset %q: unsupported modelsFetcher type %q", key, p.ModelsFetcher.Type)
			}
		}
		seenModels := make(map[string]bool, len(p.ModelIDs))
		for _, id := range p.ModelIDs {
			if strings.TrimSpace(id) == "" {
				return fmt.Errorf("preset %q: empty model id", key)
			}
			if seenModels[id] {
				return fmt.Errorf("preset %q: duplicate model id %q", key, id)
			}
			seenModels[id] = true
		}
	}
	return nil
}

// readSource decodes every JSON preset in one embedded directory or file set.
// Each file in dir must be named "<id>.json" with a matching id.
func readSource(fsys fs.FS, dir string, provenance string) (map[string]Preset, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read %s presets: %w", provenance, err)
	}
	presets := make(map[string]Preset, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		data, err := fs.ReadFile(fsys, dir+"/"+entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read preset %s: %w", entry.Name(), err)
		}
		var p Preset
		if err := json.Unmarshal(data, &p); err != nil {
			return nil, fmt.Errorf("decode preset %s: %w", entry.Name(), err)
		}
		if p.ID != name {
			return nil, fmt.Errorf("preset %s: id %q must match filename", entry.Name(), p.ID)
		}
		if _, dup := presets[p.ID]; dup {
			return nil, fmt.Errorf("preset %s: duplicate id", entry.Name())
		}
		p.LoginFlow = p.AuthFlow != ""
		p.Provenance = provenance
		presets[p.ID] = p
	}
	return presets, nil
}

// readImported decodes the generated catalogue, a JSON object keyed by preset ID.
func readImported() (map[string]Preset, error) {
	data, err := importedFS.ReadFile("imported.json")
	if err != nil {
		return nil, fmt.Errorf("read imported presets: %w", err)
	}
	var presets map[string]Preset
	if err := json.Unmarshal(data, &presets); err != nil {
		return nil, fmt.Errorf("decode imported presets: %w", err)
	}
	for key, p := range presets {
		if p.ID != key {
			return nil, fmt.Errorf("imported preset %q: id %q does not match key", key, p.ID)
		}
		p.LoginFlow = p.AuthFlow != ""
		p.Provenance = ProvenanceImported
		presets[key] = p
	}
	return presets, nil
}

// LoadImported returns the registry of imported (generated) presets only. It
// is for structural validation; runtime code uses Builtin.
func LoadImported() (*Registry, error) {
	presets, err := readImported()
	if err != nil {
		return nil, err
	}
	if len(presets) == 0 {
		return nil, fmt.Errorf("no imported presets found")
	}
	return New(presets)
}

// LoadEmbedded builds the single built-in catalogue: manual presets and imported
// presets merged once. An ID defined in both sets is an error.
func LoadEmbedded() (*Registry, error) {
	manual, err := readSource(manualFS, "manual", ProvenanceManual)
	if err != nil {
		return nil, err
	}
	if len(manual) == 0 {
		return nil, fmt.Errorf("no manual presets found")
	}
	imported, err := readImported()
	if err != nil {
		return nil, err
	}
	merged, err := mergeCatalogues(manual, imported)
	if err != nil {
		return nil, err
	}
	return New(merged)
}

// mergeCatalogues combines provenance sets into one map. An ID defined by two
// sets is an error, so each preset has exactly one source.
func mergeCatalogues(sets ...map[string]Preset) (map[string]Preset, error) {
	merged := map[string]Preset{}
	for _, set := range sets {
		for id, p := range set {
			if prev, dup := merged[id]; dup {
				return nil, fmt.Errorf("preset %q defined by both %s and %s sources", id, prev.Provenance, p.Provenance)
			}
			merged[id] = p
		}
	}
	return merged, nil
}

var (
	builtinOnce sync.Once
	builtin     *Registry
	builtinErr  error
)

// Builtin returns the immutable catalogue compiled into the binary. Invalid
// embedded data is a build defect and panics at first use.
func Builtin() *Registry {
	builtinOnce.Do(func() {
		builtin, builtinErr = LoadEmbedded()
	})
	if builtinErr != nil {
		panic("providerreg: invalid embedded presets: " + builtinErr.Error())
	}
	return builtin
}
