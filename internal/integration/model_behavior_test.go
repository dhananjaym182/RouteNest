package integration

// Model behavior characterization (plan Phase 0, item 4).
//
// These tests pin the current behavior of the live model catalogue: duplicate
// IDs, source precedence, enable/disable, metadata edits, delisted records,
// default-model cleanup, aliases, combos, free-only listing, and provider
// projection. Several assertions document quirks on purpose (for example,
// metadata edits affect only the first record with a duplicate ID). A later
// phase may change them, but only as a reviewed, separate fix.
//
// All state lives in the in-memory pool installed by installPool and in the
// per-test temp directories used by installConfig and installProviders.

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/admin"
	"routenest/internal/config"
	"routenest/internal/providers"
	"routenest/internal/routing"
)

// poolModels returns a copy of the current pool model list.
func poolModels(t *testing.T) []Model {
	t.Helper()
	accounts.PoolMu.Lock()
	defer accounts.PoolMu.Unlock()
	out := make([]Model, len(accounts.Pool.Models))
	copy(out, accounts.Pool.Models)
	return out
}

func poolDefaultModel() string {
	accounts.PoolMu.Lock()
	defer accounts.PoolMu.Unlock()
	return accounts.Pool.DefaultModel
}

func setPoolDefaultModel(v string) {
	accounts.PoolMu.Lock()
	defer accounts.PoolMu.Unlock()
	accounts.Pool.DefaultModel = v
}

// postAdmin sends a JSON POST to the live test server and returns the status and
// decoded envelope.
func postAdmin(t *testing.T, baseURL, path, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var env map[string]any
	_ = json.Unmarshal(raw, &env)
	return resp.StatusCode, env
}

// v1ModelIDs returns the sorted IDs exposed by /v1/models.
func v1ModelIDs(t *testing.T, baseURL string) []string {
	t.Helper()
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(baseURL + "/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode /v1/models: %v", err)
	}
	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	return ids
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// findModels returns every pool record with the given ID, in pool order.
func findModels(models []Model, id string) []Model {
	var out []Model
	for _, m := range models {
		if m.ID == id {
			out = append(out, m)
		}
	}
	return out
}

func TestModelDuplicateIDsMetadataEditsFirstMatchOnly(t *testing.T) {
	installPool(t, []Model{
		{ID: "dup/x", Provider: "dup", Cost: "pass", Status: "active", Custom: true},
		{ID: "dup/x", Provider: "dup", Cost: "free", Status: "active", Source: "remote"},
	})
	baseURL := protocolTestServer(t)

	status, _ := postAdmin(t, baseURL, "/admin/api/models/context", `{"id":"dup/x","context":100,"output":10}`)
	if status != http.StatusOK {
		t.Fatalf("context status = %d", status)
	}
	got := findModels(poolModels(t), "dup/x")
	if len(got) != 2 {
		t.Fatalf("duplicate records = %d, want 2", len(got))
	}
	if got[0].Context != 100 || !got[0].MetaLocked {
		t.Errorf("first record not updated: %+v", got[0])
	}
	if got[1].Context != 0 || got[1].MetaLocked {
		t.Errorf("second record must stay untouched by first-match edit: %+v", got[1])
	}
}

func TestModelDuplicateIDsToggleAppliesToEveryRecord(t *testing.T) {
	installPool(t, []Model{
		{ID: "dup/y", Provider: "dup", Cost: "pass", Status: "active", Custom: true},
		{ID: "dup/y", Provider: "dup", Cost: "free", Status: "active", Source: "remote"},
		{ID: "other/z", Provider: "other", Cost: "pass", Status: "active", Custom: true},
	})
	baseURL := protocolTestServer(t)

	if status, _ := postAdmin(t, baseURL, "/admin/api/models/toggle", `{"ids":["dup/y"],"disabled":true}`); status != http.StatusOK {
		t.Fatalf("disable status = %d", status)
	}
	for _, m := range findModels(poolModels(t), "dup/y") {
		if !m.Disabled {
			t.Errorf("record source=%q not disabled; enable/disable is ID-wide", m.Source)
		}
	}
	ids := v1ModelIDs(t, baseURL)
	if contains(ids, "dup/y") {
		t.Errorf("disabled duplicate still listed in /v1/models: %v", ids)
	}
	if !contains(ids, "other/z") {
		t.Errorf("unrelated model dropped from /v1/models: %v", ids)
	}

	if status, _ := postAdmin(t, baseURL, "/admin/api/models/toggle", `{"ids":["dup/y"],"disabled":false}`); status != http.StatusOK {
		t.Fatalf("enable status = %d", status)
	}
	if !contains(v1ModelIDs(t, baseURL), "dup/y") {
		t.Error("re-enabled model missing from /v1/models")
	}
}

func TestModelDeleteRemovesFirstMatchAndRejectsBuiltin(t *testing.T) {
	installPool(t, []Model{
		{ID: "dup/d", Provider: "dup", Cost: "pass", Status: "active", Custom: true},
		{ID: "dup/d", Provider: "dup", Cost: "free", Status: "active", Source: "remote"},
	})
	baseURL := protocolTestServer(t)

	if status, _ := postAdmin(t, baseURL, "/admin/api/models/delete", `{"id":"dup/d"}`); status != http.StatusOK {
		t.Fatalf("delete custom duplicate status = %d", status)
	}
	left := findModels(poolModels(t), "dup/d")
	if len(left) != 1 || left[0].Source != "remote" {
		t.Fatalf("after first delete = %+v, want the remote record only", left)
	}
	// The remaining record is a non-delisted synchronized model: built-in, not deletable.
	if status, _ := postAdmin(t, baseURL, "/admin/api/models/delete", `{"id":"dup/d"}`); status != http.StatusBadRequest {
		t.Errorf("delete non-delisted remote status = %d, want 400 (cannot_delete_builtin)", status)
	}
}

func TestModelDeleteDelistedRemoteIsAllowed(t *testing.T) {
	installPool(t, []Model{
		{ID: "remote/gone", Provider: "remote", Cost: "free", Status: "active", Source: "remote", Delisted: true},
	})
	baseURL := protocolTestServer(t)
	if status, _ := postAdmin(t, baseURL, "/admin/api/models/delete", `{"id":"remote/gone"}`); status != http.StatusOK {
		t.Fatalf("delete delisted status = %d, want 200", status)
	}
	if len(findModels(poolModels(t), "remote/gone")) != 0 {
		t.Error("delisted record still present after delete")
	}
}

// Current behavior: BuildModelsList filters only Disabled. A delisted record is
// still exposed through /v1/models until it is deleted.
func TestDelistedModelStillListedUntilDeleted(t *testing.T) {
	installPool(t, []Model{
		{ID: "remote/old", Provider: "remote", Cost: "free", Status: "active", Source: "remote", Delisted: true},
	})
	baseURL := protocolTestServer(t)
	if !contains(v1ModelIDs(t, baseURL), "remote/old") {
		t.Error("delisted model no longer listed; behavior changed")
	}
}

func TestDefaultModelClearedByDisableAndDelete(t *testing.T) {
	installPool(t, []Model{
		{ID: "c/one", Provider: "c", Cost: "pass", Status: "active", Custom: true},
		{ID: "c/two", Provider: "c", Cost: "pass", Status: "active", Custom: true},
	})
	baseURL := protocolTestServer(t)

	setPoolDefaultModel("c/one")
	if status, _ := postAdmin(t, baseURL, "/admin/api/models/toggle", `{"ids":["c/one"],"disabled":true}`); status != http.StatusOK {
		t.Fatalf("disable status = %d", status)
	}
	if got := poolDefaultModel(); got != "" {
		t.Errorf("default after disabling it = %q, want cleared", got)
	}

	setPoolDefaultModel("c/two")
	if status, _ := postAdmin(t, baseURL, "/admin/api/models/delete", `{"id":"c/two"}`); status != http.StatusOK {
		t.Fatalf("delete status = %d", status)
	}
	if got := poolDefaultModel(); got != "" {
		t.Errorf("default after deleting it = %q, want cleared", got)
	}
}

func TestDefaultModelUntouchedByOtherToggle(t *testing.T) {
	installPool(t, []Model{
		{ID: "k/one", Provider: "k", Cost: "pass", Status: "active", Custom: true},
		{ID: "k/two", Provider: "k", Cost: "pass", Status: "active", Custom: true},
	})
	baseURL := protocolTestServer(t)
	setPoolDefaultModel("k/one")
	if status, _ := postAdmin(t, baseURL, "/admin/api/models/toggle", `{"ids":["k/two"],"disabled":true}`); status != http.StatusOK {
		t.Fatalf("toggle status = %d", status)
	}
	if got := poolDefaultModel(); got != "k/one" {
		t.Errorf("default = %q, want k/one", got)
	}
}

func TestMetaLockFollowsContextEdits(t *testing.T) {
	installPool(t, []Model{
		{ID: "meta/m", Provider: "meta", Cost: "pass", Status: "active", Custom: true},
	})
	baseURL := protocolTestServer(t)

	if status, _ := postAdmin(t, baseURL, "/admin/api/models/context", `{"id":"meta/m","context":4096,"output":8192}`); status != http.StatusOK {
		t.Fatalf("set status = %d", status)
	}
	m := findModels(poolModels(t), "meta/m")[0]
	if !m.MetaLocked || m.Context != 4096 || m.Output != 8192 {
		t.Errorf("after set: %+v, want locked 4096/8192", m)
	}

	if status, _ := postAdmin(t, baseURL, "/admin/api/models/context", `{"id":"meta/m","context":0,"output":0}`); status != http.StatusOK {
		t.Fatalf("reset status = %d", status)
	}
	m = findModels(poolModels(t), "meta/m")[0]
	if m.MetaLocked || m.Context != 0 || m.Output != 0 {
		t.Errorf("after reset: %+v, want unlocked and unknown", m)
	}
}

func TestModelAddConflictAndProviderInference(t *testing.T) {
	installPool(t, nil)
	baseURL := protocolTestServer(t)

	if status, _ := postAdmin(t, baseURL, "/admin/api/models/add", `{"id":"acme/m1"}`); status != http.StatusOK {
		t.Fatalf("add status = %d", status)
	}
	added := findModels(poolModels(t), "acme/m1")
	if len(added) != 1 || added[0].Provider != "acme" || added[0].Cost != "pass" || !added[0].Custom || added[0].Status != "active" {
		t.Errorf("added record = %+v, want provider=acme cost=pass custom=true", added)
	}

	if status, _ := postAdmin(t, baseURL, "/admin/api/models/add", `{"id":"acme/m1"}`); status != http.StatusConflict {
		t.Errorf("duplicate add status = %d, want 409", status)
	}

	if status, _ := postAdmin(t, baseURL, "/admin/api/models/add", `{"id":"plain"}`); status != http.StatusOK {
		t.Fatalf("add slashless status = %d", status)
	}
	plain := findModels(poolModels(t), "plain")
	if len(plain) != 1 || plain[0].Provider != "custom" {
		t.Errorf("slashless provider = %+v, want custom", plain)
	}
}

// Source precedence: when a synchronized catalogue exists, AllModels returns
// remote, then zen, then custom records, and never the built-in fallback.
func TestAllModelsSourcePrecedence(t *testing.T) {
	installPool(t, []Model{
		{ID: "custom/c", Provider: "custom", Cost: "pass", Status: "active", Custom: true},
		{ID: "zen/z", Provider: "opencode", Cost: "free", Status: "active", Source: "zen"},
		{ID: "remote/r", Provider: "cline", Cost: "pass", Status: "active", Source: "remote"},
	})
	got := routing.AllModels()
	var order []string
	for _, m := range got {
		order = append(order, m.ID)
	}
	if len(order) < 3 {
		t.Fatalf("AllModels = %v", order)
	}
	pos := map[string]int{}
	for i, id := range order {
		pos[id] = i
	}
	if !(pos["remote/r"] < pos["zen/z"] && pos["zen/z"] < pos["custom/c"]) {
		t.Errorf("precedence order = %v, want remote < zen < custom", order)
	}
}

func TestFreeOnlyListingKeepsFreeModelsAndEnabledCombos(t *testing.T) {
	cfg := admin.DefaultProxyConfig()
	cfg.OnlyFree = true
	cfg.Combos = []ModelCombo{
		{ID: "combo-mixed", Name: "Mixed", Enabled: true, Entries: []ComboEntry{{Model: "paid/p"}}},
	}
	installPool(t, []Model{
		{ID: "free/f:free", Provider: "free", Cost: "free", Status: "active", Custom: true},
		{ID: "paid/p", Provider: "paid", Cost: "pass", Status: "active", Custom: true},
	})
	installConfig(t, cfg)
	baseURL := protocolTestServer(t)

	ids := v1ModelIDs(t, baseURL)
	if !contains(ids, "free/f:free") {
		t.Errorf("free model missing under free-only: %v", ids)
	}
	if contains(ids, "paid/p") {
		t.Errorf("paid model listed under free-only: %v", ids)
	}
	if !contains(ids, "combo-mixed") {
		t.Errorf("enabled combo must stay listed under free-only: %v", ids)
	}
}

func TestAliasAndExclusionListing(t *testing.T) {
	cfg := admin.DefaultProxyConfig()
	cfg.ModelAliases = []config.ModelAlias{
		{Alias: "fast", Target: "live/ok"},
		{Alias: "via-excluded", Target: "excluded/x"},
		{Alias: "via-disabled", Target: "off/y"},
	}
	cfg.ExcludedModels = []string{"excluded/x"}
	installPool(t, []Model{
		{ID: "live/ok", Provider: "live", Cost: "pass", Status: "active", Custom: true},
		{ID: "excluded/x", Provider: "excluded", Cost: "pass", Status: "active", Custom: true},
		{ID: "off/y", Provider: "off", Cost: "pass", Status: "active", Custom: true, Disabled: true},
	})
	installConfig(t, cfg)
	baseURL := protocolTestServer(t)

	ids := v1ModelIDs(t, baseURL)
	if !contains(ids, "fast") {
		t.Errorf("alias to a live model missing: %v", ids)
	}
	if contains(ids, "via-excluded") {
		t.Errorf("alias to an excluded target listed: %v", ids)
	}
	if contains(ids, "via-disabled") {
		t.Errorf("alias to a disabled target listed: %v", ids)
	}
	if contains(ids, "excluded/x") {
		t.Errorf("excluded model listed: %v", ids)
	}
}

// Provider projection: enabled source instances claim model IDs into the pool
// as Source=provider, keep user edits across re-sync, remove stale records, and
// clear a default model that the provider no longer claims.
func TestProviderProjectionLifecycle(t *testing.T) {
	installPool(t, nil)
	installProviders(t, &CustomProvider{
		ID: "pA", Name: "Alpha", BaseURL: "http://127.0.0.1:9", APIKey: "k", Enabled: true,
		ModelIDs: []string{"alpha/one", "alpha/free:free"},
	})
	providers.SyncModels()

	models := poolModels(t)
	one := findModels(models, "alpha/one")
	free := findModels(models, "alpha/free:free")
	if len(one) != 1 || one[0].Source != "provider" || one[0].Provider != "Alpha" || one[0].Cost != "pass" {
		t.Fatalf("alpha/one projection = %+v", one)
	}
	if len(free) != 1 || free[0].Cost != "free" {
		t.Fatalf("alpha/free:free projection = %+v, want cost=free from the :free suffix", free)
	}

	// User edits survive re-projection.
	setPoolDefaultModel("alpha/one")
	accounts.PoolMu.Lock()
	for i := range accounts.Pool.Models {
		if accounts.Pool.Models[i].ID == "alpha/one" {
			accounts.Pool.Models[i].Disabled = true
			accounts.Pool.Models[i].Context = 2048
			accounts.Pool.Models[i].MetaLocked = true
		}
	}
	accounts.PoolMu.Unlock()
	providers.SyncModels()
	one = findModels(poolModels(t), "alpha/one")
	if len(one) != 1 || !one[0].Disabled || one[0].Context != 2048 || !one[0].MetaLocked {
		t.Errorf("re-projection overwrote user state: %+v", one)
	}
	if poolDefaultModel() != "alpha/one" {
		t.Errorf("default cleared while still claimed: %q", poolDefaultModel())
	}

	// Removing the claim drops the record and clears the default.
	installProviders(t, &CustomProvider{
		ID: "pA", Name: "Alpha", BaseURL: "http://127.0.0.1:9", APIKey: "k", Enabled: true,
		ModelIDs: []string{"alpha/free:free"},
	})
	providers.SyncModels()
	if len(findModels(poolModels(t), "alpha/one")) != 0 {
		t.Error("stale provider record kept after claim removal")
	}
	if poolDefaultModel() != "" {
		t.Errorf("default %q not cleared after its provider claim was removed", poolDefaultModel())
	}
}

// An ID already claimed by a synchronized source is not duplicated by provider
// projection; routing still resolves the ID through the provider.
func TestProviderProjectionSkipsIDsFromOtherSources(t *testing.T) {
	installPool(t, []Model{
		{ID: "shared/s", Provider: "cline", Cost: "pass", Status: "active", Source: "remote"},
	})
	installProviders(t, &CustomProvider{
		ID: "pS", Name: "Shared", BaseURL: "http://127.0.0.1:9", APIKey: "k", Enabled: true,
		ModelIDs: []string{"shared/s"},
	})
	providers.SyncModels()

	recs := findModels(poolModels(t), "shared/s")
	if len(recs) != 1 || recs[0].Source != "remote" {
		t.Errorf("provider projection altered an ID owned by remote: %+v", recs)
	}
}
