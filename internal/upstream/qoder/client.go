package qoder

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Creds is the per-connection identity needed to sign requests.
type Creds struct {
	UserID    string // Qoder user id (psd["userId"] on the connection)
	AuthToken string // device access token, "dt-..."
	Email     string
	Name      string
	MachineID string // stable per connection (psd["machineId"]); generated if empty
	BaseURL   string // e.g. "https://api3.qoder.sh" (no trailing slash)
}

// Chat sends an OpenAI-shaped request to Qoder and returns an OpenAI-compatible
// response. The returned *http.Response has an OpenAI SSE body when stream is
// true, and a single collapsed OpenAI JSON chat.completion body when stream is
// false. The caller owns closing the body.
//
// Upstream HTTP failures are passed through unchanged (original status and
// body) so callers can surface the real status; only 2xx envelope streams are
// rewritten.
func Chat(ctx context.Context, hc *http.Client, creds Creds, params map[string]any, stream bool) (*http.Response, error) {
	modelRaw, _ := params["model"].(string)
	modelKey := strings.TrimPrefix(modelRaw, "qoder/")
	if modelKey == "" {
		return nil, fmt.Errorf("qoder: request is missing a model")
	}
	if hc == nil {
		hc = http.DefaultClient
	}

	// Qoder silently downgrades the model when the wrong model_config block is
	// sent, so a catalog miss is fatal rather than a best-effort fallback.
	entry, err := modelConfigFor(ctx, hc, creds, modelKey)
	if err != nil {
		return nil, err
	}

	payload, err := buildChatPayload(creds, params, modelKey, entry)
	if err != nil {
		return nil, err
	}
	plain, err := marshalNoEscape(payload)
	if err != nil {
		return nil, fmt.Errorf("qoder: encode request body: %w", err)
	}
	encoded := encodeBody(plain)

	endpoint := ChatURL(creds.BaseURL)
	headers, err := buildCosyHeaders([]byte(encoded), endpoint, creds)
	if err != nil {
		return nil, err
	}

	// The derived context lets the returned body's Close() abort an upstream
	// read that is otherwise blocked on a quiet connection.
	reqCtx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, strings.NewReader(encoded))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("qoder: build request: %w", err)
	}
	for k, vs := range headers {
		req.Header[k] = vs
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("X-Model-Key", modelKey)
	req.Header.Set("X-Model-Source", modelSource(entry.raw))
	// gzip would break the body signature; force identity.
	req.Header.Set("Accept-Encoding", "identity")

	resp, err := hc.Do(req)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("qoder chat: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Pass the upstream error through untouched, but keep the request
		// context tied to the body lifetime.
		resp.Body = &cancelReadCloser{ReadCloser: resp.Body, cancel: cancel}
		return resp, nil
	}

	displayModel := "qoder/" + modelKey
	if modelRaw != "" {
		displayModel = modelRaw
	}

	if stream {
		return wrapStream(resp, displayModel, cancel), nil
	}
	return collapseStream(resp, displayModel, cancel)
}

// modelSource reads model_config.source, defaulting to "system".
func modelSource(raw json.RawMessage) string {
	var meta struct {
		Source string `json:"source"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil || meta.Source == "" {
		return "system"
	}
	return meta.Source
}
