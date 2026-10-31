package providers

import "routenest/internal/providerreg"

// Protocol returns the transport protocol for a provider, or "" for plain
// OpenAI-compatible chat. CustomProvider carries no protocol field, so the
// protocol comes from the built-in preset whose base URL matches. Source
// instances created from a preset therefore keep working without a stored
// preset ID.
func Protocol(p *CustomProvider) string {
	if p == nil {
		return ""
	}
	return providerreg.Builtin().ProtocolForBaseURL(p.BaseURL)
}
