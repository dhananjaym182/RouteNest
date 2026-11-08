package providers

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"routenest/internal/config"
	"routenest/internal/domain"
	"routenest/internal/models"
	"routenest/internal/providerreg"
)

// EnsureModelPrefixes durably reserves provider-scoped client model namespaces.
// Startup and provider saves call this explicitly; read-only projections never
// allocate prefixes. Disabled and deleted provider identities stay reserved.
func EnsureModelPrefixes() error {
	return ensureModelPrefixesFor(List(), models.Snapshot())
}

// ensureModelPrefixForProvider stages the saved provider's identity before it
// becomes visible in the registry. The caller holds modelLifecycleMu.
func ensureModelPrefixForProvider(provider *CustomProvider) error {
	if provider.ID == "" {
		provider.ID = GenID()
	}
	staged := *cloneProvider(provider)
	snapshot := List()
	found := false
	for index, existing := range snapshot {
		if existing.ID == staged.ID {
			staged.CreatedAt = existing.CreatedAt
			snapshot[index] = staged
			found = true
			break
		}
	}
	if !found {
		staged.CreatedAt = time.Now()
		snapshot = append(snapshot, staged)
	}
	return ensureModelPrefixesFor(snapshot, models.Snapshot())
}

func ensureModelPrefixesFor(providers []CustomProvider, records []domain.Model) error {
	ordered := append([]CustomProvider(nil), providers...)
	sort.Slice(ordered, func(i, j int) bool {
		if !ordered[i].CreatedAt.Equal(ordered[j].CreatedAt) {
			return ordered[i].CreatedAt.Before(ordered[j].CreatedAt)
		}
		return ordered[i].ID < ordered[j].ID
	})
	ids := []string{"cline", "opencode"}
	stems := map[string]string{"cline": "cl", "opencode": "op"}
	rawNamespaces := map[string]bool{}
	addRaw := func(id string) {
		if prefix, _, qualified := strings.Cut(strings.TrimSpace(id), "/"); qualified {
			rawNamespaces[prefix] = true
		}
	}
	for _, record := range records {
		addRaw(record.ID)
	}
	for _, provider := range ordered {
		if _, duplicate := stems[provider.ID]; duplicate {
			return fmt.Errorf("provider ID %q conflicts with an existing provider identity", provider.ID)
		}
		ids = append(ids, provider.ID)
		stems[provider.ID] = providerModelPrefixStem(provider)
		for _, modelID := range provider.ModelIDs {
			addRaw(modelID)
		}
	}
	for prefix := range config.Live().ProviderModelPrefixes {
		if rawNamespaces[prefix] {
			return fmt.Errorf("raw model ID conflicts with reserved provider namespace %q", prefix)
		}
	}
	return config.EnsureProviderModelPrefixes(ids, func(providerID string, current *config.Proxy) (string, error) {
		available := func(prefix string) bool {
			if _, reserved := current.ProviderModelPrefixes[prefix]; reserved || rawNamespaces[prefix] {
				return false
			}
			for _, alias := range current.ModelAliases {
				aliasPrefix, _, qualified := strings.Cut(strings.TrimSpace(alias.Alias), "/")
				if qualified && aliasPrefix == prefix {
					return false
				}
			}
			return true
		}
		stem := stems[providerID]
		if available(stem) {
			return stem, nil
		}
		if providerID == "cline" || providerID == "opencode" {
			return "", fmt.Errorf("built-in provider namespace %q conflicts with an existing model or alias", stem)
		}
		digest := sha256.Sum256([]byte(providerID))
		suffix := hex.EncodeToString(digest[:])
		for length := 8; length <= len(suffix); length += 2 {
			candidate := stem + "_" + suffix[:length]
			if available(candidate) {
				return candidate, nil
			}
		}
		return "", fmt.Errorf("cannot reserve a unique model namespace for provider %q", providerID)
	})
}

func providerModelPrefixStem(provider CustomProvider) string {
	label := provider.Name
	if definition, known := definitionForRuntime(providerreg.Builtin(), provider.BaseURL); known {
		label = definition.ID
	}
	compact := strings.Map(func(character rune) rune {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			return character
		}
		return -1
	}, strings.ToLower(label))
	if strings.HasPrefix(compact, "antseed") {
		return "as"
	}
	switch compact {
	case "opencode":
		return "op"
	case "cline":
		return "cl"
	case "openrouter":
		return "or"
	}
	compact = strings.TrimLeft(compact, "0123456789")
	if compact == "" {
		return "pr"
	}
	if len(compact) == 1 {
		return "p" + compact
	}
	return compact[:2]
}
