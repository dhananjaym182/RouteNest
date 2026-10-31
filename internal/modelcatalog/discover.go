package modelcatalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"routenest/internal/platform"
	"routenest/internal/providerreg"
)

// MaxCatalogBody bounds one catalogue response. The admin probe previously read
// 64 KiB; the unified limit is larger so long catalogues are not truncated.
const MaxCatalogBody = 2 << 20

// errorSnippetLimit bounds upstream body text placed in error messages.
const errorSnippetLimit = 300

// ParseModels decodes an OpenAI- or OpenRouter-shaped model list. It accepts
// {"data":[...]}, {"models":[...]}, or both, de-duplicates IDs, and rejects an
// empty result.
func ParseModels(body []byte) ([]Entry, error) {
	var payload struct {
		Data   []map[string]any `json:"data"`
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, ErrUnexpectedFormat
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
		return nil, ErrNoModels
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

// Request describes one catalogue read. The unified path serves every caller:
// admin probes, import previews and periodic rechecks.
type Request struct {
	// Client performs the HTTP calls; the caller owns its transport and timeout.
	Client *http.Client
	// BaseURL is the provider base URL. Its /models list is tried first.
	BaseURL string
	// APIKey is sent as a Bearer token when non-empty.
	APIKey string
	// Headers are sent on every request. An empty value deletes the header,
	// including a default such as Authorization.
	Headers map[string]string
	// FetcherBase is an optional preset catalogue base, tried only when the
	// primary read fails.
	FetcherBase string
}

// ErrNoModels is returned when a catalogue read succeeds but lists no model.
var ErrNoModels = errors.New("upstream returned no models")

// ErrUnexpectedFormat is returned when a catalogue body has no model list.
var ErrUnexpectedFormat = errors.New("unexpected response format")

// Discover reads the model catalogue. It tries BaseURL/models and retries
// /v1/models on 404. If that read fails for any reason, it tries FetcherBase
// the same way. When both fail, the primary error is returned, because it
// describes the endpoint the operator configured. Error text never contains the
// API key or long header values.
func Discover(req Request) ([]Entry, error) {
	base := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("baseURL is empty")
	}
	entries, primaryErr := listFrom(req, base)
	if primaryErr == nil {
		return entries, nil
	}
	if fb := strings.TrimRight(strings.TrimSpace(req.FetcherBase), "/"); fb != "" && fb != base {
		if fallback, err := listFrom(req, fb); err == nil {
			return fallback, nil
		}
	}
	return nil, errors.New(redact(primaryErr.Error(), secretsOf(req)...))
}

// listFrom reads one base and parses its model list.
func listFrom(req Request, base string) ([]Entry, error) {
	status, body, err := fetchCatalog(req, base+"/models")
	if err != nil {
		return nil, fmt.Errorf("request failed: %v", err)
	}
	if status == http.StatusNotFound {
		if s2, b2, e2 := fetchCatalog(req, base+"/v1/models"); e2 == nil && s2 == http.StatusOK {
			status, body = s2, b2
		}
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", status, snippet(body))
	}
	entries, err := ParseModels(body)
	if err != nil {
		if errors.Is(err, ErrUnexpectedFormat) {
			return nil, fmt.Errorf("%w: %s", ErrUnexpectedFormat, snippet(body))
		}
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	return entries, nil
}

// fetchCatalog performs one GET with the request's auth and headers.
func fetchCatalog(req Request, rawURL string) (int, []byte, error) {
	httpReq, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, nil, err
	}
	httpReq.Header.Set("Accept", "application/json")
	if req.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)
	}
	for k, v := range req.Headers {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if strings.TrimSpace(v) == "" {
			httpReq.Header.Del(k)
			continue
		}
		httpReq.Header.Set(k, v)
	}
	resp, err := req.Client.Do(httpReq)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, MaxCatalogBody))
	return resp.StatusCode, body, nil
}

// snippet returns a trimmed prefix of an upstream body for error text.
func snippet(body []byte) string {
	return platform.Truncate(strings.TrimSpace(string(body)), errorSnippetLimit)
}

// secretsOf lists values that must not appear in error text: the API key and
// long header values (short values would match ordinary words and digits).
func secretsOf(req Request) []string {
	var out []string
	if len(req.APIKey) >= 4 {
		out = append(out, req.APIKey)
	}
	for _, v := range req.Headers {
		if len(v) >= 8 {
			out = append(out, v)
		}
	}
	return out
}

// Redact replaces each secret in s with a fixed mask. Empty secrets are ignored.
func Redact(s string, secrets ...string) string {
	return redact(s, secrets...)
}

func redact(s string, secrets ...string) string {
	for _, sec := range secrets {
		if sec != "" {
			s = strings.ReplaceAll(s, sec, redactedMask)
		}
	}
	return s
}

const redactedMask = "***"

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
