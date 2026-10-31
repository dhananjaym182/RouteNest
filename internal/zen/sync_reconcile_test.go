package zen

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"routenest/internal/accounts"
	"routenest/internal/domain"
)

// zenCatalogueTransport answers the zen /models request with a fixed body.
type zenCatalogueTransport struct{ body string }

func (z zenCatalogueTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(z.body)),
		Request:    r,
	}, nil
}

// installZenSync points the zen client at a fixed catalogue and the pool at a
// given starting state. Both are restored when the test ends.
func installZenSync(t *testing.T, catalogue string, start []domain.Model) {
	t.Helper()
	oldClient := HTTPClient
	HTTPClient = &http.Client{Transport: zenCatalogueTransport{body: catalogue}}
	ConfigMu.Lock()
	oldCfg := Config
	Config = &ConfigData{BaseURL: "https://zen.test/v1", Key: "public"}
	ConfigMu.Unlock()

	accounts.PoolMu.Lock()
	oldPool := accounts.Pool
	oldPath := accounts.PoolPath
	accounts.PoolPath = t.TempDir() + "/.routenest-accounts.json"
	accounts.Pool = &domain.AccountPool{Models: start}
	accounts.PoolMu.Unlock()

	t.Cleanup(func() {
		HTTPClient = oldClient
		ConfigMu.Lock()
		Config = oldCfg
		ConfigMu.Unlock()
		accounts.PoolMu.Lock()
		accounts.Pool = oldPool
		accounts.PoolPath = oldPath
		accounts.PoolMu.Unlock()
	})
}

func zenRecords(t *testing.T) map[string]domain.Model {
	t.Helper()
	accounts.PoolMu.Lock()
	defer accounts.PoolMu.Unlock()
	out := map[string]domain.Model{}
	for _, m := range accounts.Pool.Models {
		out[m.ID] = m
	}
	return out
}

// Table of OpenCode sync outcomes, expressed against the pool afterwards.
func TestZenSyncReconciliation(t *testing.T) {
	catalogue := `{"data":[
		{"id":"deepseek-v4-flash-free"},
		{"id":"fresh-model-free"},
		{"id":"paid-model"},
		{"id":"mimo-v2.6-flash-free"}
	]}`
	start := []domain.Model{
		// Locked limits and a manual disable must survive the rebuild.
		{ID: "paid-model", Source: "zen", Provider: "opencode", Cost: "pass", Context: 4096, Output: 8192, MetaLocked: true, Disabled: true},
		// Upstream no longer lists it: kept, marked delisted, not removed.
		{ID: "zen-gone", Source: "zen", Provider: "opencode", Cost: "pass"},
		// Other sources are never touched.
		{ID: "remote/x", Source: "remote", Provider: "cline", Cost: "pass"},
	}
	installZenSync(t, catalogue, start)

	res := SyncModels()
	if res.Error != "" {
		t.Fatalf("sync error: %s", res.Error)
	}
	if strings.Join(res.Added, ",") != "deepseek-v4-flash-free,fresh-model-free,mimo-v2.6-flash-free" {
		t.Errorf("added = %v", res.Added)
	}
	if strings.Join(res.Removed, ",") != "zen-gone" {
		t.Errorf("removed = %v", res.Removed)
	}
	byID := zenRecords(t)

	// Cost: "-free" suffix or the seed allowlist marks a model free.
	if byID["deepseek-v4-flash-free"].Cost != "free" || byID["fresh-model-free"].Cost != "free" {
		t.Errorf("free classification wrong: %+v", byID)
	}
	if byID["paid-model"].Cost != "pass" {
		t.Errorf("paid model cost = %q", byID["paid-model"].Cost)
	}

	// Limits: seed table for known ids, 200k/32k defaults for others.
	if m := byID["deepseek-v4-flash-free"]; m.Context != 200000 || m.Output != 128000 {
		t.Errorf("seed limits = %d/%d", m.Context, m.Output)
	}
	if m := byID["fresh-model-free"]; m.Context != 200000 || m.Output != 32768 {
		t.Errorf("default limits = %d/%d", m.Context, m.Output)
	}

	// Locked limits and manual disable survive.
	if m := byID["paid-model"]; m.Context != 4096 || m.Output != 8192 || !m.MetaLocked || !m.Disabled {
		t.Errorf("user state lost: %+v", m)
	}

	// Upstream removal marks, does not delete.
	if m, ok := byID["zen-gone"]; !ok || !m.Delisted {
		t.Errorf("zen-gone should be kept and delisted: %+v", m)
	}

	// Other sources untouched.
	if m := byID["remote/x"]; m.Source != "remote" || m.Delisted {
		t.Errorf("remote record changed: %+v", m)
	}
}

// An empty catalogue is an error and writes nothing.
func TestZenSyncEmptyListKeepsPool(t *testing.T) {
	start := []domain.Model{{ID: "zen-keep", Source: "zen", Provider: "opencode", Cost: "free"}}
	installZenSync(t, `{"data":[]}`, start)

	res := SyncModels()
	if res.Error == "" || !strings.Contains(res.Error, "empty list") {
		t.Fatalf("error = %q, want empty-list error", res.Error)
	}
	byID := zenRecords(t)
	if m, ok := byID["zen-keep"]; !ok || m.Delisted {
		t.Fatalf("pool changed on empty list: %+v", byID)
	}
}
