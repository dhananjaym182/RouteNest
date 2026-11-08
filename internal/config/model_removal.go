package config

import (
	"reflect"
	"strings"
	"time"

	"routenest/internal/domain"
	"routenest/internal/platform"
)

// PruneModelReferences removes stale route configuration using a private copy.
// Global references survive when another offering still serves this logical ID.
// A same-name provider pin survives when another live provider has that name.
func PruneModelReferences(ref domain.ModelRemovalRef, removeGlobal, removeSource, removeName bool) error {
	Mu.Lock()
	defer Mu.Unlock()
	before := heldLocked()
	next := before.Clone()
	providerID := ref.ProviderID
	if ref.Source == "remote" {
		providerID = "cline"
	}
	if ref.Source == "zen" {
		providerID = "opencode"
	}
	pinMatches := func(pin string) bool {
		pin = strings.TrimSpace(pin)
		return removeSource && (pin == providerID || removeName && strings.EqualFold(pin, ref.Provider))
	}
	modelMatches := func(model, pin string) bool {
		bare := strings.TrimSpace(model)
		if index := strings.LastIndex(bare, "@"); index > 0 {
			if pin == "" {
				pin = bare[index+1:]
			}
			bare = bare[:index]
		}
		return bare == ref.ID && (removeGlobal || pinMatches(pin))
	}
	chain := make([]string, 0, len(next.ModelChain))
	for _, model := range next.ModelChain {
		if !modelMatches(model, "") {
			chain = append(chain, model)
		}
	}
	if len(chain) != len(next.ModelChain) {
		next.ModelChain = chain
	}
	aliases := make([]ModelAlias, 0, len(next.ModelAliases))
	for _, alias := range next.ModelAliases {
		if !modelMatches(alias.Target, alias.Provider) {
			aliases = append(aliases, alias)
		}
	}
	if len(aliases) != len(next.ModelAliases) {
		next.ModelAliases = aliases
	}
	for index := range next.Combos {
		combo := &next.Combos[index]
		entries := make([]domain.ComboEntry, 0, len(combo.Entries))
		for _, entry := range combo.Entries {
			pin := entry.Provider
			if entry.Source == "cline" {
				pin = "cline"
			}
			if entry.Source == "zen" {
				pin = "opencode"
			}
			if !modelMatches(entry.Model, pin) {
				entries = append(entries, entry)
			}
		}
		if len(entries) != len(combo.Entries) {
			combo.Entries = entries
			combo.UpdatedAt = time.Now()
			enabled := false
			for _, entry := range entries {
				enabled = enabled || !entry.Disabled
			}
			if !enabled {
				combo.Enabled = false
			}
		}
	}
	if reflect.DeepEqual(before, next) {
		return nil
	}
	if err := SaveProxy(platform.ResolveDataPath(ProxyPath, nil), next); err != nil {
		return err
	}
	live.Store(next)
	return nil
}
