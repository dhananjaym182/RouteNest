package qoder

import "testing"

// TestURLBuildersTolerateChatSuffix pins the mirror of
// normalizePresetBaseURL / providerProtocol: a provider created with the full
// chat endpoint must still produce correctly signed bare-host URLs.
func TestURLBuildersTolerateChatSuffix(t *testing.T) {
	for _, base := range []string{
		"https://api3.qoder.sh",
		"https://api3.qoder.sh/",
		"https://api3.qoder.sh/chat/completions",
	} {
		if got, want := ChatURL(base), "https://api3.qoder.sh"+chatPath; got != want {
			t.Errorf("ChatURL(%q) = %q, want %q", base, got, want)
		}
		if got, want := ModelListURL(base), "https://api3.qoder.sh"+modelListPath; got != want {
			t.Errorf("ModelListURL(%q) = %q, want %q", base, got, want)
		}
	}
}
