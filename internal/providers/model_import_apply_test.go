package providers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"routenest/internal/accounts"
)

// Reproduces a lost update: applying an import batch appends a new model and
// then changes an existing one. Map iteration order decides which runs first.
// If the append reallocates the pool slice before the change, the change writes
// into the old array and disappears. The check repeats the apply to surface it.
func TestImportApplyKeepsTierChangeWhenAddReallocates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[
			{"id":"tier/exists","pricing":{"prompt":"0","completion":"0"}},
			{"id":"new/one","pricing":{"prompt":"1","completion":"3"}}]}`))
	}))
	defer srv.Close()

	oldPool := accounts.Pool
	t.Cleanup(func() { accounts.Pool = oldPool })
	provider := &CustomProvider{ID: "race1", Name: "RaceProv", BaseURL: srv.URL, Enabled: true, ModelIDs: []string{"tier/exists"}}
	installProviders(t, provider)
	for run := 0; run < 30; run++ {
		provider.ModelIDs = []string{"tier/exists"}
		provRecheckMu.Lock()
		providerRecheckStat = map[string]*providerRecheckState{}
		provRecheckMu.Unlock()
		accounts.Pool = &AccountPool{Models: []Model{
			{ID: "tier/exists", Provider: "RaceProv", Cost: "pass", Status: "active", Source: "provider"},
		}}

		prev, err := PreviewImport(ImportSource{ID: "race1"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ApplyImport(prev.ID, []string{"tier/exists", "new/one"}); err != nil {
			t.Fatalf("run %d: apply: %v", run, err)
		}

		accounts.PoolMu.Lock()
		got := map[string]Model{}
		for _, m := range accounts.Pool.Models {
			got[m.ID] = m
		}
		accounts.PoolMu.Unlock()
		if got["tier/exists"].Cost != "free" || got["new/one"].ID == "" {
			t.Fatalf("run %d: tier change lost or add missing: %+v", run, got)
		}

	}
}
