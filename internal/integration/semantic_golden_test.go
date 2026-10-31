package integration

// Semantic golden fixtures (plan Phase 0, item 2).
//
// Seeds a fixed pool, one source instance, one connection, a free-only off
// config with one combo and one alias, then records the normalized responses of
// /v1/models, /admin/api/models, /admin/api/providers, and /admin/api/connections.
// Normalization replaces Unix timestamps and RFC 3339 strings with placeholders,
// and JSON object keys are sorted, so the golden changes only when the content
// does.
//
// Regenerate with -update-semantic-golden after an intended change.

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"routenest/internal/admin"
	"routenest/internal/config"
	"routenest/internal/connections"
)

var updateSemanticGolden = flag.Bool("update-semantic-golden", false, "rewrite testdata/semantic_responses.golden")

var rfc3339Re = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}`)

// unorderedKeys name JSON arrays whose order comes from Go map iteration in the
// handler, so the golden compares them as sets.
var unorderedKeys = map[string]bool{"flows": true}

// runtimeHistoryKeys name objects whose contents are recorded request history.
var runtimeHistoryKeys = map[string]bool{"accountHealth": true}

func isConnectionID(v any) bool {
	s, ok := v.(string)
	return ok && strings.HasPrefix(s, "conn_")
}

func sortedStrings(v any) any {
	list, ok := v.([]any)
	if !ok {
		return v
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	anyOut := make([]any, len(out))
	for i, s := range out {
		anyOut[i] = s
	}
	return anyOut
}

// normalizeSemantic replaces volatile values with stable placeholders.
func normalizeSemantic(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, child := range t {
			switch {
			case k == "id" && isConnectionID(child):
				out[k] = "<connection-id>"
			case unorderedKeys[k]:
				out[k] = sortedStrings(normalizeSemantic(child))
			case runtimeHistoryKeys[k]:
				// Request-outcome history is filled asynchronously by other requests and
				// is not configuration; the golden records only that the key exists.
				out[k] = "<runtime-history>"
			default:
				out[k] = normalizeSemantic(child)
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, child := range t {
			out[i] = normalizeSemantic(child)
		}
		return out
	case float64:
		if t > 1e9 {
			return "<timestamp>"
		}
		return t
	case string:
		if rfc3339Re.MatchString(t) {
			return "<time>"
		}
		return t
	}
	return v
}

func fetchNormalized(t *testing.T, baseURL, path string) string {
	t.Helper()
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(baseURL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("GET %s: not JSON (%d): %s", path, resp.StatusCode, raw)
	}
	pretty, err := json.MarshalIndent(normalizeSemantic(v), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("== GET " + path + " status=" + http.StatusText(resp.StatusCode) + "\n")
	b.Write(pretty)
	b.WriteString("\n")
	return b.String()
}

func seedSemanticState(t *testing.T) {
	t.Helper()
	cfg := admin.DefaultProxyConfig()
	cfg.Combos = []ModelCombo{
		{ID: "combo-fixture", Name: "Fixture", Enabled: true, Entries: []ComboEntry{{Model: "remote/alpha"}}},
	}
	cfg.ModelAliases = []config.ModelAlias{{Alias: "fixture-fast", Target: "custom/beta"}}
	installConfig(t, cfg)

	installPool(t, []Model{
		{ID: "remote/alpha", Provider: "cline", Cost: "pass", Status: "active", Source: "remote", Context: 200000, Output: 8192},
		{ID: "remote/gone", Provider: "cline", Cost: "free", Status: "active", Source: "remote", Delisted: true},
		{ID: "zen/free", Provider: "opencode", Cost: "free", Status: "active", Source: "zen"},
		{ID: "custom/beta", Provider: "custom", Cost: "pass", Status: "active", Custom: true, ThinkingLevels: []string{"low", "high"}},
		{ID: "custom/off", Provider: "custom", Cost: "pass", Status: "active", Custom: true, Disabled: true},
	})
	installProviders(t, &CustomProvider{
		ID: "pfix", Name: "Fixture Provider", BaseURL: "http://127.0.0.1:9", APIKey: "fixture-key",
		Enabled: true, ModelIDs: []string{"fixture/one"},
	})

	c := connections.UpsertConnection(&Connection{
		Provider: "qwen", Email: "fixture@example.invalid", AccessToken: "fixture-token",
		Status: "active", CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	})
	t.Cleanup(func() { connections.RemoveConnection(c.ID) })
}

func TestSemanticResponsesGolden(t *testing.T) {
	seedSemanticState(t)
	baseURL := protocolTestServer(t)

	var b bytes.Buffer
	for _, p := range []string{
		"/v1/models",
		"/admin/api/models",
		"/admin/api/providers",
		"/admin/api/connections",
	} {
		b.WriteString(fetchNormalized(t, baseURL, p))
	}
	compareGolden(t, filepath.Join("testdata", "semantic_responses.golden"), b.Bytes(), *updateSemanticGolden)
}
