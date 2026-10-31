package providers

import (
	"strings"
	"sync"
)

// Call 调用自定义 provider 的 /chat/completions。
// providerProtocolForBaseURL maps a provider baseURL to its preset protocol.
// CustomProvider carries no protocol field: the preset it was created from
// decides how the upstream is spoken to, and the preset maps are compile-time
// data, so the lookup table is built once.
var providerProtocolForBaseURL = sync.OnceValue(func() map[string]string {
	out := map[string]string{}
	for _, presets := range []map[string]providerPreset{providerPresets, providerPresetsImported} {
		for _, p := range presets {
			if p.Protocol == "" {
				continue
			}
			if k := normalizePresetBaseURL(p.BaseURL); k != "" {
				out[k] = p.Protocol
			}
		}
	}
	return out
})

// normalizePresetBaseURL canonicalizes a baseURL for preset matching.
func normalizePresetBaseURL(u string) string {
	u = strings.ToLower(strings.TrimSpace(u))
	u = strings.TrimRight(u, "/")
	return strings.TrimSuffix(u, "/chat/completions")
}

// Protocol returns the transport protocol for a provider, or "" for
// plain OpenAI-compatible chat.
func Protocol(p *CustomProvider) string {
	if p == nil {
		return ""
	}
	return providerProtocolForBaseURL()[normalizePresetBaseURL(p.BaseURL)]
}
