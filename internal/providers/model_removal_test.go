package providers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/config"
	"routenest/internal/domain"
	"routenest/internal/modelcatalog"
	"routenest/internal/models"
	"routenest/internal/store/syncstate"
)

func removalFixture(t *testing.T) (*CustomProvider, *CustomProvider) {
	t.Helper()
	t.Setenv("ROUTENEST_DATA_DIR", t.TempDir())
	alpha, beta := sharedOfferingStateProviders()
	installOfferingStateTest(t, []domain.Model{{ID: offeringStateSharedModel, Provider: alpha.Name, Source: models.SourceProvider, Status: "active", Cost: "pass"}}, alpha, beta)
	SyncModels()
	applyProviderOfferingDelisting(*alpha, []string{offeringStateSharedModel}, nil)
	syncProviderDelistedProjection(List())
	oldConfig := config.Snapshot()
	t.Cleanup(func() { config.Publish(oldConfig) })
	cfg := config.DefaultProxy()
	cfg.ModelChain = []string{offeringStateSharedModel, offeringStateSharedModel + "@alpha", offeringStateSharedModel + "@Alpha", offeringStateSharedModel + "@Beta"}
	cfg.ModelAliases = []config.ModelAlias{{Alias: "old", Target: offeringStateSharedModel, Provider: "alpha"}, {Alias: "live", Target: offeringStateSharedModel}}
	cfg.Combos = []domain.ModelCombo{{ID: "shared-combo", Enabled: true, Entries: []domain.ComboEntry{
		{Model: offeringStateSharedModel, Source: "provider", Provider: "alpha"},
		{Model: offeringStateSharedModel, Source: "provider", Provider: "Beta"},
		{Model: offeringStateSharedModel},
	}}, {ID: "old-combo", Enabled: true, Entries: []domain.ComboEntry{{Model: offeringStateSharedModel, Source: "provider", Provider: "Alpha"}}}}
	config.Publish(cfg)
	return alpha, beta
}

func TestRemoveDelistedModelIsProviderScopedAndCleansReferences(t *testing.T) {
	alpha, beta := removalFixture(t)
	modelID := offeringStateSharedModel
	importsMu.Lock()
	for _, id := range []string{"alpha-auto", "alpha-manual"} {
		items := []importItem{{ModelID: modelID, Tier: "free", Action: "tier_change"}, {ModelID: "new/keep", Tier: "pass", Action: "add"}}
		pendingImports[id] = &importBatch{ID: id, ProviderID: alpha.ID, Auto: id == "alpha-auto", Items: items, Stats: modelcatalog.ComputeBatchStats(items)}
	}
	pendingImports["beta-batch"] = &importBatch{ID: "beta-batch", ProviderID: beta.ID, Items: []importItem{{ModelID: modelID, Tier: "free", Action: "tier_change"}}}
	importsMu.Unlock()
	provRecheckMu.Lock()
	state := providerRecheckStat[alpha.ID]
	state.BatchID = "alpha-auto"
	state.NewQueued = 1
	state.Runtime = &syncstate.RuntimeState{SchemaVersion: 1, Budget: &syncstate.BudgetState{Tokens: map[string]int64{"history": 19}}}
	provRecheckMiss[alpha.ID] = map[string]int{modelID: 2}
	if err := saveProviderRecheckStateLocked(); err != nil {
		t.Fatal(err)
	}
	provRecheckMu.Unlock()
	Mu.Lock()
	Cooldowns[alpha.ID+":"+modelID] = time.Now().Add(time.Hour)
	Cooldowns[beta.ID+":"+modelID] = time.Now().Add(time.Hour)
	Mu.Unlock()
	ref := domain.ModelRemovalRef{ID: modelID, Source: models.SourceProvider, ProviderID: alpha.ID}
	removed, err := RemoveDelistedModel(ref)
	if err != nil || !removed {
		t.Fatalf("cleanup = %v, %v", removed, err)
	}
	if containsModel(Find(alpha.ID).ModelIDs, modelID) || !containsModel(Find(beta.ID).ModelIDs, modelID) {
		t.Fatal("cleanup did not isolate the provider declaration")
	}
	pool := models.Snapshot()
	if len(pool) != 1 || pool[0].Provider != beta.Name || pool[0].Delisted {
		t.Fatalf("live Beta row not preserved: %+v", pool)
	}
	accounts.PoolMu.Lock()
	defaultModel := accounts.Pool.DefaultModel
	accounts.PoolMu.Unlock()
	if defaultModel != modelID {
		t.Fatalf("live shared default cleared: %q", defaultModel)
	}
	cfg := config.Live()
	if !reflect.DeepEqual(cfg.ModelChain, []string{modelID, modelID + "@Beta"}) || len(cfg.ModelAliases) != 1 || len(cfg.Combos[0].Entries) != 2 || cfg.Combos[1].Enabled || len(cfg.Combos[1].Entries) != 0 {
		t.Fatalf("provider pins not cleaned while live references survive: %+v", cfg)
	}
	importsMu.Lock()
	for _, id := range []string{"alpha-auto", "alpha-manual"} {
		batch := pendingImports[id]
		if batch == nil || len(batch.Items) != 1 || batch.Items[0].ModelID != "new/keep" || batch.Stats.Total != 1 || batch.Stats.Add != 1 {
			t.Fatalf("pending batch not filtered: %+v", batch)
		}
	}
	if len(pendingImports["beta-batch"].Items) != 1 {
		t.Fatal("Beta batch changed")
	}
	importsMu.Unlock()
	states, err := syncstate.Load(providerSyncFilePath)
	if err != nil || states[alpha.ID].Offerings[modelID].Delisted != nil || len(states[alpha.ID].DelistedModels) != 0 || states[alpha.ID].Runtime.Budget.Tokens["history"] != 19 {
		t.Fatalf("offering/runtime state cleanup wrong: %+v err=%v", states[alpha.ID], err)
	}
	Mu.Lock()
	_, alphaCool := Cooldowns[alpha.ID+":"+modelID]
	_, betaCool := Cooldowns[beta.ID+":"+modelID]
	Mu.Unlock()
	if alphaCool || !betaCool {
		t.Fatal("cooldown cleanup was not scoped")
	}
	SyncModels()
	if containsModel(Find(alpha.ID).ModelIDs, modelID) {
		t.Fatal("projection sync resurrected removed declaration")
	}
	if again, err := RemoveDelistedModel(ref); err != nil || again {
		t.Fatalf("repeat cleanup = %v, %v", again, err)
	}
	restartOfferingStateTest(t)
	for _, offering := range mustRemovalOfferings(t) {
		if offering.ProviderID == domain.ProviderInstanceID(alpha.ID) && offering.ModelID == modelID {
			t.Fatalf("restart resurrected Alpha offering: %+v", offering)
		}
	}
}

func mustRemovalOfferings(t *testing.T) []domain.ModelOffering {
	t.Helper()
	offerings, err := SnapshotModelOfferings()
	if err != nil {
		t.Fatal(err)
	}
	return offerings
}

func TestRemoveDelistedModelSkipsRecoveredAndCustomOfferings(t *testing.T) {
	alpha, _ := removalFixture(t)
	applyProviderOfferingDelisting(*alpha, nil, map[string]bool{offeringStateSharedModel: true})
	ref := domain.ModelRemovalRef{ID: offeringStateSharedModel, Source: models.SourceProvider, ProviderID: alpha.ID}
	if removed, err := RemoveDelistedModel(ref); err != nil || removed {
		t.Fatalf("recovered cleanup = %v, %v", removed, err)
	}
	applyProviderOfferingDelisting(*alpha, []string{offeringStateSharedModel}, nil)
	provRecheckMu.Lock()
	custom := providerRecheckStat[alpha.ID].Offerings[offeringStateSharedModel]
	custom.Custom = boolValue(true)
	providerRecheckStat[alpha.ID].Offerings[offeringStateSharedModel] = custom
	provRecheckMu.Unlock()
	if removed, err := RemoveDelistedModel(ref); err != nil || removed {
		t.Fatalf("custom cleanup = %v, %v", removed, err)
	}
	if !containsModel(Find(alpha.ID).ModelIDs, offeringStateSharedModel) {
		t.Fatal("protected declaration removed")
	}
}

func TestRemoveDelistedModelPreservesPinsToAnotherProviderWithSameName(t *testing.T) {
	alpha, beta := removalFixture(t)
	beta.Name = alpha.Name
	Upsert(beta)
	cfg := config.DefaultProxy()
	cfg.ModelChain = []string{offeringStateSharedModel + "@alpha", offeringStateSharedModel + "@Alpha"}
	config.Publish(cfg)
	if removed, err := RemoveDelistedModel(domain.ModelRemovalRef{ID: offeringStateSharedModel, Source: models.SourceProvider, ProviderID: alpha.ID}); err != nil || !removed {
		t.Fatalf("cleanup = %v, %v", removed, err)
	}
	if !reflect.DeepEqual(config.Live().ModelChain, []string{offeringStateSharedModel + "@Alpha"}) {
		t.Fatalf("same-name live pin removed: %v", config.Live().ModelChain)
	}
}

func TestRemoveDelistedModelUnknownProviderIsIdempotentWithoutChangingLiveReferences(t *testing.T) {
	removalFixture(t)
	cfg := config.DefaultProxy()
	cfg.ModelChain = []string{offeringStateSharedModel + "@unknown", offeringStateSharedModel}
	config.Publish(cfg)
	before := models.Snapshot()
	if removed, err := RemoveDelistedModel(domain.ModelRemovalRef{ID: offeringStateSharedModel, Source: models.SourceProvider, ProviderID: "unknown"}); err != nil || removed || !reflect.DeepEqual(before, models.Snapshot()) || !reflect.DeepEqual(cfg.ModelChain, config.Live().ModelChain) {
		t.Fatalf("unknown-provider cleanup changed live state: %v %v", removed, err)
	}
}

func TestRemoveDelistedModelSaveFailuresAreTruthfulAndRetryable(t *testing.T) {
	for _, failure := range []string{"declaration", "pool", "config"} {
		t.Run(failure, func(t *testing.T) {
			alpha, _ := removalFixture(t)
			ref := domain.ModelRemovalRef{ID: offeringStateSharedModel, Source: models.SourceProvider, ProviderID: alpha.ID}
			var restore func()
			switch failure {
			case "declaration":
				before := FilePath
				FilePath = t.TempDir()
				restore = func() { FilePath = before }
			case "pool":
				before := accounts.PoolPath
				accounts.PoolPath = t.TempDir()
				restore = func() { accounts.PoolPath = before }
			case "config":
				before := pruneModelReferences
				pruneModelReferences = func(domain.ModelRemovalRef, bool, bool, bool) error {
					return errors.New("injected config save failure")
				}
				restore = func() { pruneModelReferences = before }
			}
			removed, err := RemoveDelistedModel(ref)
			if err == nil || removed {
				t.Fatalf("failed %s reported success: %v, %v", failure, removed, err)
			}
			if failure == "declaration" && !containsModel(Find(alpha.ID).ModelIDs, ref.ID) {
				t.Fatal("failed declaration save published in memory")
			}
			restore()
			if _, err := RemoveDelistedModel(ref); err != nil {
				t.Fatalf("retry could not finish cleanup: %v", err)
			}
			if strings.Contains(strings.Join(config.Live().ModelChain, ","), "@alpha") {
				t.Fatal("retry left stale route references")
			}
		})
	}
}

func TestProviderRecheckCannotResurrectOfferingRemovedDuringDiscovery(t *testing.T) {
	alpha, _ := removalFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": offeringStateSharedModel}, {"id": "still-present"}}})
	}))
	defer server.Close()
	alpha.BaseURL = server.URL
	Upsert(alpha)
	result := make(chan providerRecheckState, 1)
	go func() { result <- recheckProviderModels(*alpha) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("discovery did not start")
	}
	removed, err := RemoveDelistedModel(domain.ModelRemovalRef{ID: offeringStateSharedModel, Source: models.SourceProvider, ProviderID: alpha.ID})
	close(release)
	if err != nil || !removed {
		t.Fatalf("concurrent cleanup = %v, %v", removed, err)
	}
	select {
	case state := <-result:
		if state.LastError != "" {
			t.Fatalf("recheck error: %+v", state)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("recheck did not finish")
	}
	provRecheckMu.Lock()
	_, exists := providerRecheckStat[alpha.ID].Offerings[offeringStateSharedModel]
	provRecheckMu.Unlock()
	if exists || containsModel(Find(alpha.ID).ModelIDs, offeringStateSharedModel) {
		t.Fatal("stale discovery resurrected removed offering")
	}
	for _, batch := range PendingImports() {
		if batch.ProviderID == alpha.ID {
			full, err := PendingImport(batch.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range full.Items {
				if item.ModelID == offeringStateSharedModel {
					t.Fatal("stale discovery recreated removed preview item")
				}
			}
		}
	}
}

func TestProviderPreviewCannotRequeueOfferingRemovedDuringDiscovery(t *testing.T) {
	alpha, _ := removalFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": offeringStateSharedModel}, {"id": "new-model"}}})
	}))
	defer server.Close()
	alpha.BaseURL = server.URL
	Upsert(alpha)
	type previewResult struct {
		batch *importBatch
		err   error
	}
	result := make(chan previewResult, 1)
	go func() { batch, err := PreviewImport(ImportSource{ID: alpha.ID}); result <- previewResult{batch, err} }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("discovery did not start")
	}
	removed, err := RemoveDelistedModel(domain.ModelRemovalRef{ID: offeringStateSharedModel, Source: models.SourceProvider, ProviderID: alpha.ID})
	close(release)
	if err != nil || !removed {
		t.Fatalf("concurrent cleanup = %v, %v", removed, err)
	}
	select {
	case response := <-result:
		if response.err != nil {
			t.Fatal(response.err)
		}
		if len(response.batch.Items) != 1 || response.batch.Items[0].ModelID != "new-model" {
			t.Fatalf("stale preview requeued removed model: %+v", response.batch)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("preview did not finish")
	}
}
