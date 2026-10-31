package main

import (
	"cline-go-proxy/internal/routing"
	"encoding/json"
	"net/http"

	"cline-go-proxy/internal/compaction"
	"cline-go-proxy/internal/proxycore"
)

// init wires the compaction package's root-owned seams: the Cline summary
// backend and the full Anthropic-to-OpenAI converter used by count_tokens.
func init() {
	routing.IsContextLengthError = compaction.IsContextLengthError
	compaction.CallClineAPI = func(params map[string]any, stream bool) (*http.Response, error) {
		resp, _, err := routing.CallClineAPI(params, stream)
		return resp, err
	}
	compaction.AnthropicToOpenAI = func(body []byte) (map[string]any, error) {
		var req proxycore.AnthropicReq
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return proxycore.AnthropicToOpenAI(req), nil
	}
}
