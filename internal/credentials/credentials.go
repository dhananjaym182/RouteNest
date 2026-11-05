// Package credentials provides a provider-neutral, redacted view over legacy
// credential stores. Source adapters remain responsible for persistence and
// refresh/login protocol behavior during migration.
package credentials

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"routenest/internal/domain"
	"routenest/internal/providerstate"
)

type Source interface {
	Name() string
	Snapshot() []domain.ProviderCredential
}

type Registry struct {
	store   *providerstate.Store
	sources []Source
}

func NewRegistry(store *providerstate.Store, sources ...Source) (*Registry, error) {
	if store == nil {
		return nil, fmt.Errorf("credentials: provider state store is required")
	}
	seen := map[string]bool{}
	for _, source := range sources {
		if source == nil || strings.TrimSpace(source.Name()) == "" {
			return nil, fmt.Errorf("credentials: source name is required")
		}
		if seen[source.Name()] {
			return nil, fmt.Errorf("credentials: duplicate source %q", source.Name())
		}
		seen[source.Name()] = true
	}
	return &Registry{store: store, sources: append([]Source(nil), sources...)}, nil
}

func cloneCredential(credential domain.ProviderCredential) domain.ProviderCredential {
	credential.ProviderIDs = append([]domain.ProviderInstanceID(nil), credential.ProviderIDs...)
	if credential.ModelCooldowns != nil {
		copyMap := make(map[string]time.Time, len(credential.ModelCooldowns))
		for model, until := range credential.ModelCooldowns {
			copyMap[model] = until
		}
		credential.ModelCooldowns = copyMap
	}
	return credential
}

func (r *Registry) Snapshot() ([]domain.ProviderCredential, error) {
	if r == nil {
		return nil, fmt.Errorf("credentials: registry is nil")
	}
	seen := map[domain.CredentialID]string{}
	var out []domain.ProviderCredential
	for _, source := range r.sources {
		for _, credential := range source.Snapshot() {
			credential = cloneCredential(credential)
			if !credential.Valid() {
				return nil, fmt.Errorf("credentials: source %q returned invalid credential %q", source.Name(), credential.ID)
			}
			if previous, duplicate := seen[credential.ID]; duplicate {
				return nil, fmt.Errorf("credentials: duplicate credential %q from %s and %s", credential.ID, previous, source.Name())
			}
			seen[credential.ID] = source.Name()
			sort.Slice(credential.ProviderIDs, func(i, j int) bool { return credential.ProviderIDs[i] < credential.ProviderIDs[j] })
			out = append(out, credential)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func hasProvider(credential domain.ProviderCredential, providerID domain.ProviderInstanceID) bool {
	for _, id := range credential.ProviderIDs {
		if id == providerID {
			return true
		}
	}
	return false
}

func (r *Registry) Eligible(providerID domain.ProviderInstanceID, modelID string, now time.Time) ([]domain.ProviderCredential, error) {
	credentials, err := r.Snapshot()
	if err != nil {
		return nil, err
	}
	out := make([]domain.ProviderCredential, 0, len(credentials))
	for _, credential := range credentials {
		if !hasProvider(credential, providerID) || credential.Status != "active" {
			continue
		}
		if !credential.ExpiresAt.IsZero() && !now.Before(credential.ExpiresAt) && !credential.Refreshable {
			continue
		}
		if until := credential.ModelCooldowns[modelID]; !until.IsZero() && now.Before(until) {
			continue
		}
		if !credential.CooldownUntil.IsZero() && now.Before(credential.CooldownUntil) {
			continue
		}
		scope := domain.AvailabilityScope{ProviderID: providerID, CredentialID: credential.ID, ModelID: modelID}
		if len(r.store.Blocking(scope)) != 0 {
			continue
		}
		out = append(out, cloneCredential(credential))
	}
	return out, nil
}

func (r *Registry) Observe(observation domain.AvailabilityObservation) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("credentials: registry is not configured")
	}
	return r.store.Apply(observation)
}

var defaultRegistry atomic.Pointer[Registry]

func Publish(registry *Registry) error {
	if registry == nil {
		return fmt.Errorf("credentials: default registry is nil")
	}
	defaultRegistry.Store(registry)
	return nil
}

func Default() *Registry {
	registry := defaultRegistry.Load()
	if registry == nil {
		panic("credentials: app.Configure() was not called (registry unset)")
	}
	return registry
}

func HasDefault() bool { return defaultRegistry.Load() != nil }
