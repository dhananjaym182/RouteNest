package providers

import (
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/config"
	"routenest/internal/domain"
	"routenest/internal/modelcatalog"
	"routenest/internal/models"
	"routenest/internal/platform"
	poolstore "routenest/internal/store/accounts"
	"routenest/internal/store/syncstate"
)

const manualRemovalLiteralID = "org/shared-model@revision"

func manualRemovalServiceFixture(t *testing.T) (*CustomProvider, *CustomProvider) {
	t.Helper()
	t.Setenv(platform.DataDirEnv, t.TempDir())
	beforeConfig := config.Snapshot()
	config.Mu.Lock()
	*config.Held() = *config.DefaultProxy()
	config.Mu.Unlock()
	t.Cleanup(func() {
		config.Mu.Lock()
		*config.Held() = *beforeConfig.Clone()
		config.Mu.Unlock()
	})
	beforeCatalog := ModelRemovalOfferingsProvider
	ModelRemovalOfferingsProvider = SnapshotModelOfferings
	t.Cleanup(func() { ModelRemovalOfferingsProvider = beforeCatalog })
	alpha := &CustomProvider{ID: "manual-alpha", Name: "AntSeed", BaseURL: "https://antseed.test/v1", APIKey: "fixture-alpha-key", Enabled: true,
		ModelIDs: []string{manualRemovalLiteralID, "alpha-keep"}, Priority: 7, TimeoutSec: 19, ProxyURL: "direct", ConnectionID: "alpha-connection", Headers: map[string]string{"X-Fixture": "alpha"}}
	beta := &CustomProvider{ID: "manual-beta", Name: "Sibling", BaseURL: "https://sibling.test/v1", APIKey: "fixture-beta-key", Enabled: true,
		ModelIDs: []string{manualRemovalLiteralID, "beta-keep"}, Priority: 9, ConnectionIDs: []string{"beta-connection"}}
	installOfferingStateTest(t, []domain.Model{
		{ID: manualRemovalLiteralID, Source: models.SourceProvider, Provider: alpha.Name, Cost: "free", TierSignal: "manual", Custom: true, Status: "active"},
		{ID: manualRemovalLiteralID, Source: models.SourceRemote, Cost: "free", Status: "active"},
		{ID: manualRemovalLiteralID, Source: models.SourceZen, Cost: "free", Status: "active"},
		{ID: "org/shared-model", Source: models.SourceRemote, Status: "active"},
	}, alpha, beta)
	accounts.PoolMu.Lock()
	accounts.Pool.DefaultModel = manualRemovalLiteralID
	accounts.Pool.Accounts = []*domain.Account{{AccountID: "manual-fixture-account", AccessToken: "fixture-access", RefreshToken: "fixture-refresh", Status: "active"}}
	accounts.PoolMu.Unlock()
	SyncModels()
	cfg := config.DefaultProxy()
	cfg.ProviderModelPrefixes = map[string]string{"as": alpha.ID, "bs": beta.ID, "retired": "manual-retired"}
	cfg.ModelChain = []string{manualRemovalLiteralID, "as/" + manualRemovalLiteralID, "bs/" + manualRemovalLiteralID, "cl/" + manualRemovalLiteralID, "op/" + manualRemovalLiteralID, manualRemovalLiteralID + "@" + alpha.ID}
	cfg.ModelAliases = []config.ModelAlias{{Alias: "alpha-alias", Target: "as/" + manualRemovalLiteralID}, {Alias: "sibling-alias", Target: "bs/" + manualRemovalLiteralID}}
	cfg.Combos = []domain.ModelCombo{{ID: "manual-combo", Enabled: true, Entries: []domain.ComboEntry{
		{Model: manualRemovalLiteralID, Source: "provider", Provider: alpha.ID},
		{Model: manualRemovalLiteralID, Source: "provider", Provider: beta.ID},
		{Model: manualRemovalLiteralID, Source: "cline"}, {Model: manualRemovalLiteralID, Source: "zen"}, {Model: manualRemovalLiteralID},
	}}}
	config.Publish(cfg)
	return Find(alpha.ID), Find(beta.ID)
}

func manualRemovalOffering(t *testing.T, providerID string) domain.ModelOffering {
	t.Helper()
	for _, offering := range mustRemovalOfferings(t) {
		if string(offering.ProviderID) == providerID && offering.ModelID == manualRemovalLiteralID {
			return offering
		}
	}
	t.Fatalf("fixture offering missing for provider %q", providerID)
	return domain.ModelOffering{}
}

func TestRemoveModelExplicitlyRemovesActiveCustomAndDelistedOfferings(t *testing.T) {
	for _, kind := range []string{"active", "custom", "delisted"} {
		t.Run(kind, func(t *testing.T) {
			alpha, beta := manualRemovalServiceFixture(t)
			provRecheckMu.Lock()
			state := providerRecheckStat[alpha.ID].Offerings[manualRemovalLiteralID]
			state.Custom, state.Delisted = boolValue(kind == "custom"), boolValue(kind == "delisted")
			providerRecheckStat[alpha.ID].Offerings[manualRemovalLiteralID] = state
			if err := saveProviderRecheckStateLocked(); err != nil {
				t.Fatal(err)
			}
			provRecheckMu.Unlock()
			beforeSibling, beforeCline, beforeZen := manualRemovalOffering(t, beta.ID), manualRemovalOffering(t, "cline"), manualRemovalOffering(t, "opencode")
			reservations := config.Snapshot().ProviderModelPrefixes
			ref := domain.ModelRemovalRef{ID: manualRemovalLiteralID, Source: models.SourceProvider, ProviderID: alpha.ID, Provider: "wrong display name"}
			if removed, err := RemoveModel(ref); err != nil || !removed {
				t.Fatalf("explicit %s removal = %v, %v", kind, removed, err)
			}
			alpha.ModelIDs = []string{"alpha-keep"}
			if !reflect.DeepEqual(Find(alpha.ID), alpha) || !reflect.DeepEqual(Find(beta.ID), beta) {
				t.Fatal("model removal changed provider settings, credentials, or a sibling declaration")
			}
			if !reflect.DeepEqual(manualRemovalOffering(t, beta.ID), beforeSibling) || !reflect.DeepEqual(manualRemovalOffering(t, "cline"), beforeCline) || !reflect.DeepEqual(manualRemovalOffering(t, "opencode"), beforeZen) {
				t.Fatal("same-ID sibling offering state changed")
			}
			cfg := config.Live()
			wantChain := []string{manualRemovalLiteralID, "bs/" + manualRemovalLiteralID, "cl/" + manualRemovalLiteralID, "op/" + manualRemovalLiteralID}
			if !reflect.DeepEqual(cfg.ModelChain, wantChain) || !reflect.DeepEqual(cfg.ProviderModelPrefixes, reservations) || len(cfg.Combos[0].Entries) != 4 || len(cfg.ModelAliases) != 1 {
				t.Fatalf("routing references were not provider-scoped: chain=%v combos=%+v aliases=%+v", cfg.ModelChain, cfg.Combos, cfg.ModelAliases)
			}
			accounts.PoolMu.Lock()
			account, defaultID := accounts.Pool.Accounts[0], accounts.Pool.DefaultModel
			accounts.PoolMu.Unlock()
			if defaultID != manualRemovalLiteralID || account.AccessToken != "fixture-access" || account.RefreshToken != "fixture-refresh" {
				t.Fatal("removal changed a sibling-backed default or account credentials")
			}
			if again, err := RemoveModel(ref); err != nil || again {
				t.Fatalf("repeat explicit removal = %v, %v", again, err)
			}
			SyncModels()
			for _, offering := range mustRemovalOfferings(t) {
				if string(offering.ProviderID) == alpha.ID && offering.ModelID == ref.ID {
					t.Fatal("refresh resurrected removed provider offering")
				}
			}
		})
	}
}

func TestRemoveModelBuiltinSourcesPersistReservationsAndStayRemovedOnRefresh(t *testing.T) {
	for _, target := range []struct{ source, providerID, prefix string }{{models.SourceRemote, "cline", "cl"}, {models.SourceZen, "opencode", "op"}} {
		t.Run(target.providerID, func(t *testing.T) {
			manualRemovalServiceFixture(t)
			before := mustRemovalOfferings(t)
			ref := domain.ModelRemovalRef{ID: manualRemovalLiteralID, Source: target.source, ProviderID: target.providerID}
			if removed, err := RemoveModel(ref); err != nil || !removed {
				t.Fatalf("builtin explicit removal = %v, %v", removed, err)
			}
			models.View(func(pool *domain.AccountPool) {
				if !models.OfferingRemovalReserved(pool, target.providerID, ref.ID) || pool.DefaultModel != ref.ID {
					t.Fatalf("builtin service did not reserve removal or preserve shared default: reservations=%+v default=%q", pool.RemovedModelOfferings, pool.DefaultModel)
				}
			})
			stored, err := poolstore.Load(accounts.PoolPath)
			if err != nil || !models.OfferingRemovalReserved(stored, target.providerID, ref.ID) {
				t.Fatalf("builtin reservation was not saved: %v", err)
			}
			for _, expected := range before {
				if expected.ModelID != ref.ID || string(expected.ProviderID) == target.providerID {
					continue
				}
				if !reflect.DeepEqual(manualRemovalOffering(t, string(expected.ProviderID)), expected) {
					t.Fatalf("builtin removal changed sibling %s", expected.ProviderID)
				}
			}
			if _, err := models.ReplaceSource(target.source, func([]domain.Model) []domain.Model {
				return []domain.Model{{ID: ref.ID, Source: target.source, Status: "active"}, {ID: "refresh-keep", Source: target.source, Status: "active"}}
			}); err != nil {
				t.Fatalf("source refresh failed: %v", err)
			}
			for _, offering := range mustRemovalOfferings(t) {
				if string(offering.ProviderID) == target.providerID && offering.ModelID == ref.ID {
					t.Fatalf("refresh restored manually removed builtin offering: %+v", offering)
				}
			}
			for _, model := range config.Live().ModelChain {
				if model == target.prefix+"/"+ref.ID {
					t.Fatal("builtin namespace route reference survived removal")
				}
			}
			if again, err := RemoveModel(ref); err != nil || again {
				t.Fatalf("repeat builtin removal = %v, %v", again, err)
			}
		})
	}
}

func TestRemoveModelClearsOnlyItsProviderQualifiedDefault(t *testing.T) {
	alpha, beta := manualRemovalServiceFixture(t)
	accounts.PoolMu.Lock()
	accounts.Pool.DefaultModel = "as/" + manualRemovalLiteralID
	accounts.PoolMu.Unlock()
	if removed, err := RemoveModel(domain.ModelRemovalRef{ID: manualRemovalLiteralID, Source: models.SourceProvider, ProviderID: alpha.ID}); err != nil || !removed {
		t.Fatalf("explicit removal = %v, %v", removed, err)
	}
	models.View(func(pool *domain.AccountPool) {
		if pool.DefaultModel != "" {
			t.Fatalf("removed provider-qualified default survived: %q", pool.DefaultModel)
		}
	})
	if !containsModel(Find(beta.ID).ModelIDs, manualRemovalLiteralID) {
		t.Fatal("clearing a provider-qualified default removed a sibling")
	}
}

func TestRemoveModelPrunesCanonicalBuiltinComboSourceWithCustomProviderSibling(t *testing.T) {
	for _, target := range []struct{ source, providerID, prefix, otherSource string }{
		{models.SourceRemote, "cline", "cl", models.SourceZen},
		{models.SourceZen, "opencode", "op", models.SourceRemote},
	} {
		t.Run(target.providerID, func(t *testing.T) {
			_, beta := manualRemovalServiceFixture(t)
			models.Edit(func(pool *domain.AccountPool) bool {
				for i := range pool.Models {
					if pool.Models[i].ID == manualRemovalLiteralID && pool.Models[i].Source == models.SourceProvider {
						pool.Models[i].Provider, pool.Models[i].Custom = beta.Name, true
					}
				}
				return true
			})
			cfg := config.Snapshot()
			entries := []domain.ComboEntry{
				{Model: manualRemovalLiteralID, Source: target.source},
				{Model: manualRemovalLiteralID, Source: "provider", Provider: beta.ID},
				{Model: manualRemovalLiteralID},
				{Model: "org/shared-model", Source: target.source},
				{Model: manualRemovalLiteralID, Source: target.otherSource},
			}
			cfg.Combos = []domain.ModelCombo{{ID: "canonical-source", Enabled: true, Entries: entries}}
			config.Publish(cfg)
			ref := domain.ModelRemovalRef{ID: manualRemovalLiteralID, Source: target.source, ProviderID: target.providerID}
			if removed, err := RemoveModel(ref); err != nil || !removed {
				t.Fatalf("builtin canonical-source removal = %v, %v", removed, err)
			}
			if got := config.Live().Combos[0].Entries; !reflect.DeepEqual(got, entries[1:]) {
				t.Fatalf("canonical builtin combo pin survived removal or a sibling changed: got=%+v want=%+v", got, entries[1:])
			}
			if !reflect.DeepEqual(Find(beta.ID), beta) {
				t.Fatal("builtin combo cleanup changed the custom sibling's credentials or provider settings")
			}
			models.View(func(pool *domain.AccountPool) {
				if pool.DefaultModel != manualRemovalLiteralID {
					t.Fatalf("same-ID sibling-backed default was cleared: %q", pool.DefaultModel)
				}
				found := false
				for _, row := range pool.Models {
					found = found || row.ID == manualRemovalLiteralID && row.Source == models.SourceProvider && row.Provider == beta.Name && row.Custom
				}
				if !found {
					t.Fatal("canonical source cleanup changed the retained custom provider row")
				}
			})
			stored, err := poolstore.Load(accounts.PoolPath)
			if err != nil || stored.DefaultModel != manualRemovalLiteralID || !models.OfferingRemovalReserved(stored, target.providerID, ref.ID) {
				t.Fatalf("canonical source removal did not preserve/persist pool state: err=%v", err)
			}
		})
	}
}

func TestRemoveModelRetainsSameNameSiblingPinsAndRejectsMissingStableIdentity(t *testing.T) {
	alpha, beta := manualRemovalServiceFixture(t)
	beta.Name = alpha.Name
	Upsert(beta)
	cfg := config.Snapshot()
	cfg.ModelChain = []string{manualRemovalLiteralID + "@" + alpha.ID, manualRemovalLiteralID + "@" + alpha.Name}
	config.Publish(cfg)
	for _, ref := range []domain.ModelRemovalRef{{ID: manualRemovalLiteralID, Source: models.SourceProvider, Provider: alpha.Name}, {ID: manualRemovalLiteralID, Source: models.SourceRemote}} {
		if removed, err := RemoveModel(ref); err == nil || removed {
			t.Fatalf("manual removal accepted missing stable provider ID: %v %v", removed, err)
		}
	}
	if removed, err := RemoveModel(domain.ModelRemovalRef{ID: manualRemovalLiteralID, Source: models.SourceProvider, ProviderID: alpha.ID}); err != nil || !removed {
		t.Fatalf("stable provider removal = %v, %v", removed, err)
	}
	if !reflect.DeepEqual(config.Live().ModelChain, []string{manualRemovalLiteralID + "@" + alpha.Name}) {
		t.Fatalf("same-name sibling pin was removed: %v", config.Live().ModelChain)
	}
}

func TestRemoveModelDoesNotTreatUnknownStableIDAsSiblingName(t *testing.T) {
	_, beta := manualRemovalServiceFixture(t)
	beta.Name = "unknown-instance"
	Upsert(beta)
	beforePool, beforeConfig := models.Snapshot(), config.Snapshot()
	if removed, err := RemoveModel(domain.ModelRemovalRef{ID: manualRemovalLiteralID, Source: models.SourceProvider, ProviderID: beta.Name}); err != nil || removed {
		t.Fatalf("unknown instance removal = %v, %v", removed, err)
	}
	if !reflect.DeepEqual(beforePool, models.Snapshot()) || !reflect.DeepEqual(beforeConfig, config.Snapshot()) || !containsModel(Find(beta.ID).ModelIDs, manualRemovalLiteralID) {
		t.Fatal("unknown stable provider ID changed a same-name sibling")
	}
}

func TestRemoveModelPrunesOnlyTargetPreviewAndPreservesRuntimeAccounting(t *testing.T) {
	alpha, beta := manualRemovalServiceFixture(t)
	importsMu.Lock()
	for _, provider := range []*CustomProvider{alpha, beta} {
		items := []importItem{{ModelID: manualRemovalLiteralID, Action: "tier_change", Tier: "free"}, {ModelID: "preview-keep", Action: "add", Tier: "free"}}
		pendingImports[provider.ID] = &importBatch{ID: provider.ID, ProviderID: provider.ID, Items: items, Stats: modelcatalog.ComputeBatchStats(items)}
	}
	importsMu.Unlock()
	provRecheckMu.Lock()
	providerRecheckStat[alpha.ID].Runtime = &syncstate.RuntimeState{SchemaVersion: 1, Budget: &syncstate.BudgetState{Tokens: map[string]int64{"history": 31}}}
	if err := saveProviderRecheckStateLocked(); err != nil {
		t.Fatal(err)
	}
	provRecheckMu.Unlock()
	if removed, err := RemoveModel(domain.ModelRemovalRef{ID: manualRemovalLiteralID, Source: models.SourceProvider, ProviderID: alpha.ID}); err != nil || !removed {
		t.Fatalf("explicit removal = %v, %v", removed, err)
	}
	importsMu.Lock()
	alphaBatch, betaBatch := pendingImports[alpha.ID], pendingImports[beta.ID]
	importsMu.Unlock()
	if alphaBatch == nil || len(alphaBatch.Items) != 1 || alphaBatch.Items[0].ModelID != "preview-keep" || betaBatch == nil || len(betaBatch.Items) != 2 {
		t.Fatal("pending import cleanup was not provider/model scoped")
	}
	states, err := syncstate.Load(providerSyncFilePath)
	if err != nil || states[alpha.ID] == nil || states[alpha.ID].Runtime.Budget.Tokens["history"] != 31 {
		t.Fatalf("runtime accounting was deleted during model removal: %v", err)
	}
}

func TestRemoveModelFailedWritesAreTruthfulAndRetryable(t *testing.T) {
	for _, failure := range []string{"declaration", "pool", "state", "cooldown", "config"} {
		t.Run(failure, func(t *testing.T) {
			alpha, beta := manualRemovalServiceFixture(t)
			Mu.Lock()
			Cooldowns[alpha.ID+":"+manualRemovalLiteralID], Cooldowns[beta.ID+":"+manualRemovalLiteralID] = time.Now().Add(time.Hour), time.Now().Add(time.Hour)
			Mu.Unlock()
			if _, _, _, err := NewLocalRuntimeStateBackend(time.Now); err != nil {
				t.Fatal(err)
			}
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
			case "cooldown":
				before := CooldownsPath
				CooldownsPath = t.TempDir()
				restore = func() { CooldownsPath = before }
			case "config":
				before := pruneModelReferences
				pruneModelReferences = func(domain.ModelRemovalRef, bool, bool, bool) error {
					return errors.New("injected config save failure")
				}
				restore = func() { pruneModelReferences = before }
			default:
				path := providerSyncFilePath
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
				restore = func() {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
			}
			ref := domain.ModelRemovalRef{ID: manualRemovalLiteralID, Source: models.SourceProvider, ProviderID: alpha.ID}
			removed, err := RemoveModel(ref)
			restore()
			if err == nil || removed {
				t.Fatalf("failed %s write was reported as removal: %v, %v", failure, removed, err)
			}
			if _, err := RemoveModel(ref); err != nil {
				t.Fatalf("retry after %s failure failed: %v", failure, err)
			}
			if containsModel(Find(alpha.ID).ModelIDs, ref.ID) || !containsModel(Find(beta.ID).ModelIDs, ref.ID) {
				t.Fatal("retry did not finish isolated declaration cleanup")
			}
			for _, model := range config.Live().ModelChain {
				if model == "as/"+ref.ID || model == ref.ID+"@"+alpha.ID {
					t.Fatal("retry left target routing references")
				}
			}
			if again, err := RemoveModel(ref); err != nil || again {
				t.Fatalf("repeat after retry = %v, %v", again, err)
			}
		})
	}
}

func TestExplicitRemovalDoesNotWeakenDelistedRemovalProtections(t *testing.T) {
	alpha, _ := manualRemovalServiceFixture(t)
	ref := domain.ModelRemovalRef{ID: manualRemovalLiteralID, Source: models.SourceProvider, ProviderID: alpha.ID}
	if removed, err := RemoveDelistedModel(ref); err != nil || removed || !containsModel(Find(alpha.ID).ModelIDs, ref.ID) {
		t.Fatalf("delisted endpoint removed an active custom offering: %v, %v", removed, err)
	}
	if removed, err := RemoveModel(ref); err != nil || !removed {
		t.Fatalf("explicit endpoint did not remove selected custom offering: %v, %v", removed, err)
	}
}
