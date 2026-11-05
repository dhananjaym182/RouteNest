package compaction

import (
	"encoding/json"
	"log"

	"routenest/internal/config"
	"routenest/internal/protocol/anthropic"
	"routenest/internal/protocol/openaichat"
	"routenest/internal/routing"
)

// AnthropicOptions returns the converter options for Anthropic Messages requests:
// the override.md system prompt, the configured reasoning effort and the
// tool-use ID generator. The chat proxy and the compaction path both use it, so
// their conversions agree.
func AnthropicOptions() anthropic.Options {
	return anthropic.Options{
		SystemOverride:  config.OverrideContent(),
		ReasoningEffort: routing.ReasoningEffort(),
		NewToolUseID:    openaichat.GenToolUseID,
		Logf:            log.Printf,
	}
}

// AnthropicToOpenAI converts a raw Anthropic Messages request body to OpenAI
// chat params using AnthropicOptions.
func AnthropicToOpenAI(body []byte) (map[string]any, error) {
	var req anthropic.MessagesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	return anthropic.MessagesToOpenAI(req, AnthropicOptions()), nil
}
