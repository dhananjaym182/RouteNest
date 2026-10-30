package qoder

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// catalogTTL matches the Kiro catalog cache: one hour per credential.
const catalogTTL = time.Hour

// catalogEntry is one raw entry from /algo/api/v2/model/list. The raw JSON is
// preserved verbatim because it is sent back as `model_config` on every chat
// request — reconstructing it from parsed fields risks dropping keys the
// upstream uses to pick the model.
type catalogEntry struct {
	key             string
	raw             json.RawMessage
	enable          bool
	isReasoning     bool
	maxOutputTokens int
}

// catalog is the cached result of one model-list fetch.
type catalog struct {
	expiresAt time.Time
	entries   []catalogEntry
	byKey     map[string]int
}

func (c *catalog) lookup(key string) (catalogEntry, bool) {
	if c == nil {
		return catalogEntry{}, false
	}
	idx, ok := c.byKey[key]
	if !ok {
		return catalogEntry{}, false
	}
	return c.entries[idx], true
}

// catalogCall coalesces concurrent misses for the same credential into one
// upstream request.
type catalogCall struct {
	done chan struct{}
	cat  *catalog
	err  error
}

var (
	catalogMu       sync.Mutex
	catalogCache    = map[string]*catalog{}
	catalogInflight = map[string]*catalogCall{}
)

// InvalidateCatalog drops the cached catalog for a (baseURL, userID) pair, so
// the next Models/Chat call re-fetches. Callers use it after re-authenticating
// or when a model_config is missing.
func InvalidateCatalog(baseURL, userID string) {
	catalogMu.Lock()
	delete(catalogCache, catalogCacheKey(baseURL, userID))
	catalogMu.Unlock()
}

func catalogCacheKey(baseURL, userID string) string {
	return strings.TrimRight(baseURL, "/") + "\x00" + userID
}

// Models returns the live model catalog (Qoder model keys) for a credential.
// Entries with enable:false are included: the upstream still accepts chat for
// them, they are only hidden in the Qoder UI.
func Models(ctx context.Context, hc *http.Client, creds Creds) ([]string, error) {
	cat, err := loadCatalog(ctx, hc, creds, false)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(cat.entries))
	for _, e := range cat.entries {
		keys = append(keys, e.key)
	}
	return keys, nil
}

// loadCatalog returns the cached catalog, fetching it when missing/expired.
// Concurrent misses for the same credential share one upstream call. A
// force=true call bypasses the cache and performs its own fetch (used when a
// requested model was not present in the cached catalog).
func loadCatalog(ctx context.Context, hc *http.Client, creds Creds, force bool) (*catalog, error) {
	if creds.UserID == "" || creds.AuthToken == "" {
		return nil, fmt.Errorf("qoder: credential is missing userId or accessToken")
	}
	key := catalogCacheKey(creds.BaseURL, creds.UserID)

	catalogMu.Lock()
	if !force {
		if cat := catalogCache[key]; cat != nil && time.Now().Before(cat.expiresAt) {
			catalogMu.Unlock()
			return cat, nil
		}
		if call := catalogInflight[key]; call != nil {
			catalogMu.Unlock()
			select {
			case <-call.done:
				return call.cat, call.err
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	call := &catalogCall{done: make(chan struct{})}
	catalogInflight[key] = call
	catalogMu.Unlock()

	cat, err := fetchCatalog(ctx, hc, creds)

	catalogMu.Lock()
	call.cat, call.err = cat, err
	if err == nil {
		catalogCache[key] = cat
	}
	if catalogInflight[key] == call {
		delete(catalogInflight, key)
	}
	catalogMu.Unlock()
	close(call.done)
	return cat, err
}

// fetchCatalog performs the COSY-signed GET. The body is empty, so the COSY
// signature covers an empty payload.
func fetchCatalog(ctx context.Context, hc *http.Client, creds Creds) (*catalog, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	endpoint := ModelListURL(creds.BaseURL)
	headers, err := buildCosyHeaders(nil, endpoint, creds)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("qoder model list: build request: %w", err)
	}
	for k, vs := range headers {
		req.Header[k] = vs
	}
	req.Header.Set("Accept", "application/json")
	// gzip would invalidate the body signature; force identity.
	req.Header.Set("Accept-Encoding", "identity")

	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qoder model list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("qoder model list: HTTP %d %s", resp.StatusCode, truncate(strings.TrimSpace(string(snippet)), 200))
	}

	var payload struct {
		Chat []json.RawMessage `json:"chat"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("qoder model list: decode: %w", err)
	}
	if payload.Chat == nil {
		// A 200 whose body has no "chat" array is a shape mismatch (WAF page,
		// changed API). Do NOT cache it: an empty catalog would hard-error
		// every chat for a full TTL hour. The JS reference behaves the same.
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return nil, fmt.Errorf("qoder model list: unexpected response shape %q", truncate(string(b), 120))
	}

	cat := &catalog{
		expiresAt: time.Now().Add(catalogTTL),
		byKey:     make(map[string]int, len(payload.Chat)),
	}
	for _, raw := range payload.Chat {
		var meta struct {
			Key             string `json:"key"`
			Enable          *bool  `json:"enable"`
			IsReasoning     bool   `json:"is_reasoning"`
			MaxOutputTokens any    `json:"max_output_tokens"`
		}
		if err := json.Unmarshal(raw, &meta); err != nil {
			continue
		}
		if meta.Key == "" {
			continue
		}
		// enable is absent on some entries; only an explicit false disables.
		enable := meta.Enable == nil || *meta.Enable
		maxOut := 0
		if n, ok := numberToInt(meta.MaxOutputTokens); ok && n > 0 {
			maxOut = n
		}
		if _, dup := cat.byKey[meta.Key]; dup {
			continue
		}
		cat.byKey[meta.Key] = len(cat.entries)
		cat.entries = append(cat.entries, catalogEntry{
			key:             meta.Key,
			raw:             append(json.RawMessage(nil), raw...),
			enable:          enable,
			isReasoning:     meta.IsReasoning,
			maxOutputTokens: maxOut,
		})
	}
	return cat, nil
}

// modelConfigFor resolves the raw model_config block for modelKey. A missing
// entry triggers exactly one forced refresh: Qoder silently downgrades the
// model when the wrong block is sent, so a final miss is a hard error.
func modelConfigFor(ctx context.Context, hc *http.Client, creds Creds, modelKey string) (catalogEntry, error) {
	firstErr := error(nil)
	if cat, err := loadCatalog(ctx, hc, creds, false); err != nil {
		firstErr = err
	} else if entry, ok := cat.lookup(modelKey); ok {
		return entry, nil
	}

	refreshed, refreshErr := loadCatalog(ctx, hc, creds, true)
	if refreshErr == nil {
		if entry, ok := refreshed.lookup(modelKey); ok {
			return entry, nil
		}
	}
	if refreshErr != nil {
		return catalogEntry{}, fmt.Errorf("qoder: model_config for %q is unavailable: the model catalog could not supply a model_config (refresh failed: %v)", modelKey, refreshErr)
	}
	if firstErr != nil {
		return catalogEntry{}, fmt.Errorf("qoder: model_config for %q is unavailable: the model catalog could not supply a model_config (%v)", modelKey, firstErr)
	}
	return catalogEntry{}, fmt.Errorf("qoder: model_config for %q is unavailable: the model catalog could not supply a model_config (the model may not be offered to this account)", modelKey)
}
