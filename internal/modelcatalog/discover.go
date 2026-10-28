package modelcatalog

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"cline-go-proxy/internal/platform"
	"cline-go-proxy/internal/providerreg"
)

const maxCatalogBody = 2 << 20

// ParseModels decodes an OpenAI- or OpenRouter-shaped model list. It accepts
// {"data":[...]}, {"models":[...]}, or both, de-duplicates IDs, and rejects an
// empty result.
func ParseModels(body []byte) ([]Entry, error) {
	var payload struct {
		Data   []map[string]any `json:"data"`
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("unexpected response format")
	}
	var out []Entry
	seen := map[string]bool{}
	for _, list := range [][]map[string]any{payload.Data, payload.Models} {
		for _, it := range list {
			id, _ := it["id"].(string)
			id = strings.TrimSpace(id)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			e := Entry{ID: id}
			if n, ok := it["name"].(string); ok {
				e.Name = strings.TrimSpace(n)
			} else if n, ok := it["display_name"].(string); ok {
				e.Name = strings.TrimSpace(n)
			}
			if pr, ok := it["pricing"].(map[string]any); ok {
				e.PricingPrompt = rawNum(pr["prompt"])
				e.PricingComplet = rawNum(pr["completion"])
				e.PricingRequest = rawNum(pr["request"])
				// Additive alternate notations: *_text variants and
				// LiteLLM-style per-token keys (values may be strings).
				if e.PricingPrompt == nil {
					e.PricingPrompt = firstNum(pr["prompt_text"], pr["input_cost_per_token"])
				}
				if e.PricingComplet == nil {
					e.PricingComplet = firstNum(pr["completion_text"], pr["output_cost_per_token"])
				}
				if e.PricingRequest == nil {
					e.PricingRequest = firstNum(pr["request_cost"])
				}
			}
			// Top-level per-token fallbacks (LiteLLM layout) when the pricing
			// object is absent or left the component unresolved.
			if e.PricingPrompt == nil {
				e.PricingPrompt = firstNum(it["input_cost_per_token"])
			}
			if e.PricingComplet == nil {
				e.PricingComplet = firstNum(it["output_cost_per_token"])
			}
			if e.PricingRequest == nil {
				e.PricingRequest = firstNum(it["request_cost"])
			}
			if tags, ok := it["tags"].([]any); ok {
				for _, t := range tags {
					if s, ok := t.(string); ok {
						e.Tags = append(e.Tags, s)
					}
				}
			}
			switch fv := it["free"].(type) {
			case bool:
				e.FreeFlag = fv
			case string:
				e.FreeFlag = strings.EqualFold(strings.TrimSpace(fv), "true")
			}
			if mv, ok := it["mode"].(string); ok {
				e.Mode = strings.TrimSpace(mv)
			}
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("upstream returned no models")
	}
	return out, nil
}

func rawNum(v any) *float64 {
	switch n := v.(type) {
	case float64:
		return &n
	case int:
		f := float64(n)
		return &f
	case int64:
		f := float64(n)
		return &f
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return nil
		}
		return &f
	case string:
		s := strings.TrimSpace(n)
		s = strings.TrimSpace(strings.TrimPrefix(s, "$"))
		lower := strings.ToLower(s)
		lower = strings.TrimSpace(strings.TrimSuffix(lower, "usd"))
		lower = strings.TrimSpace(strings.TrimSuffix(lower, "/1m"))
		if lower == "" {
			return nil
		}
		f, err := strconv.ParseFloat(lower, 64)
		if err != nil {
			// Unparseable component stays unknown; callers must not treat
			// unknown pricing as free.
			return nil
		}
		return &f
	}
	return nil
}

// firstNum returns the first successfully parsed number among vals.
func firstNum(vals ...any) *float64 {
	for _, v := range vals {
		if n := rawNum(v); n != nil {
			return n
		}
	}
	return nil
}

// Discover fetches {baseURL}/models, retrying /v1/models on 404 and then the
// preset catalog endpoint when fetcherBase is non-empty. The caller owns the
// HTTP client and its transport policy.
func Discover(client *http.Client, baseURL, apiKey string, headers map[string]string, fetcherBase string) ([]Entry, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		return nil, fmt.Errorf("baseURL is empty")
	}
	fetch := func(u string) (int, []byte, error) {
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Accept", "application/json")
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
		for k, v := range headers {
			if strings.TrimSpace(v) != "" {
				req.Header.Set(strings.TrimSpace(k), v)
			}
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBody))
		return resp.StatusCode, body, nil
	}
	tryBase := func(b string) ([]Entry, error) {
		status, body, err := fetch(b + "/models")
		if err != nil {
			return nil, fmt.Errorf("request failed: %v", err)
		}
		if status == http.StatusNotFound {
			s2, b2, e2 := fetch(b + "/v1/models")
			if e2 == nil && s2 == http.StatusOK {
				status, body = s2, b2
			}
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("HTTP %d: %s", status, platform.Truncate(strings.TrimSpace(string(body)), 200))
		}
		return ParseModels(body)
	}
	bases := []string{baseURL}
	if fetcherBase != "" && fetcherBase != baseURL {
		bases = append(bases, fetcherBase)
	}
	var entries []Entry
	var lastErr error
	for _, b := range bases {
		entries, lastErr = tryBase(b)
		if lastErr == nil {
			break
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	return entries, nil
}

// FetcherBaseFor returns a preset's catalog base URL for this provider baseURL.
// Only fetcher URLs ending in /models are trusted; other formats are ignored
// rather than guessed. The zen source is hardcoded: its catalog path is owned
// by the dedicated zen transport, so no preset row is required.
func FetcherBaseFor(baseURL string) string {
	norm := func(u string) string {
		u = strings.ToLower(strings.TrimRight(strings.TrimSpace(u), "/"))
		u = strings.TrimSuffix(u, "/chat/completions")
		return strings.Replace(u, "//localhost", "//127.0.0.1", 1)
	}
	target := norm(baseURL)
	if target == "" {
		return ""
	}
	if target == "https://opencode.ai" {
		return "https://opencode.ai/zen/v1"
	}
	for _, p := range providerreg.Builtin().All() {
		if p.ModelsFetcher == nil || !strings.HasSuffix(strings.ToLower(p.ModelsFetcher.URL), "/models") {
			continue
		}
		if norm(p.BaseURL) == target {
			fb := strings.TrimRight(strings.TrimSuffix(strings.TrimRight(p.ModelsFetcher.URL, "/"), "/models"), "/")
			if fb != "" && fb != target {
				return fb
			}
		}
	}
	return ""
}
