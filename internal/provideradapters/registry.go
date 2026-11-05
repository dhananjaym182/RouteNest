package provideradapters

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"

	"routenest/internal/providerreg"
)

// Registry is immutable after construction.
type Registry struct {
	byID map[string]Bundle
}

func NewRegistry(bundles ...Bundle) (*Registry, error) {
	r := &Registry{byID: make(map[string]Bundle, len(bundles))}
	for _, bundle := range bundles {
		bundle.ID = strings.TrimSpace(bundle.ID)
		if bundle.ID == "" {
			return nil, fmt.Errorf("provider adapter id is required")
		}
		if bundle.Invoker == nil {
			return nil, fmt.Errorf("provider adapter %q: invoker is required", bundle.ID)
		}
		if _, exists := r.byID[bundle.ID]; exists {
			return nil, fmt.Errorf("provider adapter %q: duplicate registration", bundle.ID)
		}
		r.byID[bundle.ID] = bundle
	}
	return r, nil
}

func (r *Registry) Get(id string) (Bundle, bool) {
	if r == nil {
		return Bundle{}, false
	}
	bundle, ok := r.byID[id]
	return bundle, ok
}

func (r *Registry) IDs() []string {
	if r == nil {
		return nil
	}
	ids := make([]string, 0, len(r.byID))
	for id := range r.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// ValidateDefinitions ensures every immutable definition resolves to an
// executable adapter before the registry is published.
func (r *Registry) ValidateDefinitions(definitions *providerreg.Registry) error {
	if r == nil || definitions == nil {
		return fmt.Errorf("provider adapter registry and definitions are required")
	}
	for _, id := range definitions.IDs() {
		adapter := definitions.AdapterFor(id)
		if _, ok := r.Get(adapter); !ok {
			return fmt.Errorf("provider definition %q references unavailable adapter %q", id, adapter)
		}
	}
	return nil
}

var published atomic.Pointer[Registry]

func Publish(registry *Registry) error {
	if registry == nil {
		return fmt.Errorf("provider adapter registry is nil")
	}
	published.Store(registry)
	return nil
}

func Default() *Registry {
	registry := published.Load()
	if registry == nil {
		panic("provideradapters: app.Configure() was not called (registry unset)")
	}
	return registry
}

func HasDefault() bool { return published.Load() != nil }
