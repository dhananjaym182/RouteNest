package main

import (
	"encoding/json"
	"net/http"

	"cline-go-proxy/internal/compaction"
)

// init wires the compaction package's root-owned seams: the Cline summary
// backend and the full Anthropic-to-OpenAI converter used by count_tokens.
func init() {
	compaction.CallClineAPI = func(params map[string]any, stream bool) (*http.Response, error) {
		resp, _, err := callClineAPI(params, stream)
		return resp, err
	}
	compaction.AnthropicToOpenAI = func(body []byte) (map[string]any, error) {
		var req anthropicReq
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return anthropicToOpenAI(req), nil
	}
}
