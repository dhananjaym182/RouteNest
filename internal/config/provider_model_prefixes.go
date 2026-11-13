package config

import (
	"fmt"
	"sort"
	"strings"

	"routenest/internal/platform"
)

var saveProviderModelPrefixes = SaveProxy

// ProviderModelIDReserved reports whether id is a stable provider identity,
// including a deleted instance whose model namespace must never be reassigned.
func ProviderModelIDReserved(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	if id == "cline" || id == "opencode" {
		return true
	}
	for _, providerID := range Live().ProviderModelPrefixes {
		if providerID == id {
			return true
		}
	}
	return false
}

// ProviderModelPrefix returns the stable client namespace for a provider ID.
// Built-in namespaces also work before a legacy config has been registered.
func (c *Proxy) ProviderModelPrefix(providerID string) string {
	switch providerID = strings.TrimSpace(providerID); providerID {
	case "cline":
		return "cl"
	case "opencode":
		return "op"
	}
	if c != nil {
		for prefix, reservedID := range c.ProviderModelPrefixes {
			if reservedID == providerID {
				return prefix
			}
		}
	}
	return ""
}

// SplitProviderModel resolves a compact namespace without changing the upstream
// model ID, including its nested path or literal @ characters. A recognized
// prefix with an empty tail is qualified so callers can reject it explicitly.
func (c *Proxy) SplitProviderModel(raw string) (modelID, providerID string, qualified bool) {
	raw = strings.TrimSpace(raw)
	prefix, modelID, qualified := strings.Cut(raw, "/")
	if !qualified {
		return raw, "", false
	}
	switch prefix {
	case "cl":
		return modelID, "cline", true
	case "op":
		return modelID, "opencode", true
	}
	if c != nil {
		if providerID = c.ProviderModelPrefixes[prefix]; providerID != "" {
			return modelID, providerID, true
		}
	}
	return raw, "", false
}

// ValidateProviderModelPrefixes rejects ambiguous identities and any attempt to
// reuse a built-in namespace or overlap an administrator-configured alias.
func (c *Proxy) ValidateProviderModelPrefixes() error {
	prefixes := make([]string, 0, len(c.ProviderModelPrefixes))
	for prefix := range c.ProviderModelPrefixes {
		prefixes = append(prefixes, prefix)
	}
	sort.Strings(prefixes)
	seen := make(map[string]string, len(prefixes))
	for _, prefix := range prefixes {
		providerID := c.ProviderModelPrefixes[prefix]
		if !validProviderModelPrefix(prefix) {
			return fmt.Errorf("invalid provider model prefix %q", prefix)
		}
		if providerID == "" || strings.TrimSpace(providerID) != providerID {
			return fmt.Errorf("provider model prefix %q requires a stable provider ID", prefix)
		}
		if prefix == "cl" && providerID != "cline" || prefix == "op" && providerID != "opencode" ||
			providerID == "cline" && prefix != "cl" || providerID == "opencode" && prefix != "op" {
			return fmt.Errorf("provider model prefix %q conflicts with a built-in provider namespace", prefix)
		}
		if previous, ok := seen[providerID]; ok {
			return fmt.Errorf("provider ID %q has multiple model prefixes %q and %q", providerID, previous, prefix)
		}
		seen[providerID] = prefix
	}
	for _, alias := range c.ModelAliases {
		prefix, _, qualified := strings.Cut(strings.TrimSpace(alias.Alias), "/")
		_, reserved := c.ProviderModelPrefixes[prefix]
		if qualified && (reserved || prefix == "cl" || prefix == "op") {
			return fmt.Errorf("model alias %q conflicts with reserved provider namespace %q", alias.Alias, prefix)
		}
	}
	return nil
}

func validProviderModelPrefix(prefix string) bool {
	if prefix == "" || prefix[0] < 'a' || prefix[0] > 'z' {
		return false
	}
	for _, character := range prefix {
		if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' || character == '_') {
			return false
		}
	}
	return true
}

// EnsureProviderModelPrefixes reserves one namespace for each stable provider
// ID without removing old reservations. choosePrefix runs under Mu and must
// only read its private config argument; capture all external snapshots before
// calling this function. Nothing is published until persistence succeeds.
func EnsureProviderModelPrefixes(providerIDs []string, choosePrefix func(string, *Proxy) (string, error)) error {
	Mu.Lock()
	defer Mu.Unlock()
	before := heldLocked()
	if err := before.ValidateProviderModelPrefixes(); err != nil {
		return err
	}
	next := before.Clone()
	byProvider := make(map[string]string, len(next.ProviderModelPrefixes))
	for prefix, providerID := range next.ProviderModelPrefixes {
		byProvider[providerID] = prefix
	}
	changed := false
	for _, providerID := range providerIDs {
		if providerID == "" || strings.TrimSpace(providerID) != providerID {
			return fmt.Errorf("provider model namespace requires a stable provider ID")
		}
		if _, exists := byProvider[providerID]; exists {
			continue
		}
		if choosePrefix == nil {
			return fmt.Errorf("provider model namespace allocator is missing")
		}
		prefix, err := choosePrefix(providerID, next)
		if err != nil {
			return err
		}
		if previous, exists := next.ProviderModelPrefixes[prefix]; exists {
			return fmt.Errorf("provider model prefix %q is reserved for provider %q", prefix, previous)
		}
		if next.ProviderModelPrefixes == nil {
			next.ProviderModelPrefixes = make(map[string]string)
		}
		next.ProviderModelPrefixes[prefix] = providerID
		byProvider[providerID] = prefix
		changed = true
	}
	if err := next.ValidateProviderModelPrefixes(); err != nil {
		return err
	}
	if !changed {
		return nil
	}
	if err := saveProviderModelPrefixes(platform.ResolveDataPath(ProxyPath, nil), next); err != nil {
		return fmt.Errorf("save provider model namespaces: %w", err)
	}
	live.Store(next)
	return nil
}
