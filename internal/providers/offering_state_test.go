package providers

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/domain"
	"routenest/internal/models"
	"routenest/internal/store/syncstate"
)

const offeringStateSharedModel = "shared/model"

func installOfferingStateTest(t *testing.T, records []domain.Model, provs ...*CustomProvider) {
	t.Helper()
	installProviders(t, provs...)
	accounts.PoolMu.Lock()
	oldPool, oldPath := accounts.Pool, accounts.PoolPath
	accounts.PoolPath = filepath.Join(t.TempDir(), ".routenest-accounts.json")
	accounts.Pool = &domain.AccountPool{Models: records, DefaultModel: offeringStateSharedModel}
	accounts.PoolMu.Unlock()
	importsMu.Lock()
	oldImports := pendingImports
	pendingImports = map[string]*importBatch{}
	importsMu.Unlock()
	t.Cleanup(func() {
		accounts.PoolMu.Lock()
		accounts.Pool, accounts.PoolPath = oldPool, oldPath
		accounts.PoolMu.Unlock()
		importsMu.Lock()
		pendingImports = oldImports
		importsMu.Unlock()
	})
}

func sharedOfferingStateProviders() (*CustomProvider, *CustomProvider) {
	return &CustomProvider{
			ID: "alpha", Name: "Alpha", BaseURL: "https://alpha.test/v1",
			Enabled: true, Priority: 1, ModelIDs: []string{offeringStateSharedModel},
		}, &CustomProvider{
			ID: "beta", Name: "Beta", BaseURL: "https://beta.test/v1",
			Enabled: true, Priority: 2, ModelIDs: []string{offeringStateSharedModel},
		}
}

func restartOfferingStateTest(t *testing.T) {
	t.Helper()
	Mu.Lock()
	CustomProviders, Loaded = nil, false
	Cooldowns = map[string]time.Time{}
	Mu.Unlock()
	provRecheckMu.Lock()
	providerRecheckStat = map[string]*providerRecheckState{}
	provRecheckMiss = map[string]map[string]int{}
	provRecheckLoaded = false
	provRecheckMu.Unlock()
	accounts.PoolMu.Lock()
	accounts.Pool = nil
	accounts.PoolMu.Unlock()
	Load()
	LoadRecheckState()
	accounts.LoadPool()
}

func offeringForStateTest(t *testing.T, providerID string) domain.ModelOffering {
	t.Helper()
	offerings, err := SnapshotModelOfferings()
	if err != nil {
		t.Fatal(err)
	}
	for _, offering := range offerings {
		if offering.ProviderID == domain.ProviderInstanceID(providerID) && offering.ModelID == offeringStateSharedModel {
			return offering
		}
	}
	t.Fatalf("shared model offering missing for provider %q: %+v", providerID, offerings)
	return domain.ModelOffering{}
}

func TestProviderOfferingStateCostIsolation(t *testing.T) {
	for _, betaFirst := range []bool{false, true} {
		name := "alpha_then_beta"
		if betaFirst {
			name = "beta_then_alpha"
		}
		t.Run(name, func(t *testing.T) {
			alpha, beta := sharedOfferingStateProviders()
			installOfferingStateTest(t, []domain.Model{{
				ID: offeringStateSharedModel, Provider: alpha.Name, Source: models.SourceProvider,
				Cost: models.CostPass, TierSignal: tierSignalPricingPaid, TierDecidedAt: 100, Status: "active",
			}}, alpha, beta)
			SyncModels()
			order := []*CustomProvider{alpha, beta}
			if betaFirst {
				order = []*CustomProvider{beta, alpha}
			}
			for _, provider := range order {
				decision := offeringDecision{ID: offeringStateSharedModel, Cost: models.CostPass, Signal: tierSignalPricingPaid}
				if provider.ID == beta.ID {
					decision.Cost, decision.Signal = models.CostFree, tierSignalPricingZero
				}
				added, _ := applyProviderOfferingDecisions(*provider, []offeringDecision{decision}, 200)
				if len(added) != 0 {
					t.Fatalf("declared shared model was added again: %v", added)
				}
				mirrorOfferingDecisions(*provider, []offeringDecision{decision}, 200)
			}
			SyncModels()
			alphaOffering := offeringForStateTest(t, alpha.ID)
			betaOffering := offeringForStateTest(t, beta.ID)
			if alphaOffering.Access != domain.AccessPaidAPI || alphaOffering.FreeOnlyEligible() || alphaOffering.TierSignal != tierSignalPricingPaid {
				t.Fatalf("Alpha must retain its paid offering: %+v", alphaOffering)
			}
			if betaOffering.Access != domain.AccessFree || !betaOffering.FreeOnlyEligible() || betaOffering.TierSignal != tierSignalPricingZero {
				t.Fatalf("Beta must retain its verified free offering: %+v", betaOffering)
			}
			legacy, ok := poolModelByID(offeringStateSharedModel)
			if !ok || legacy.Cost != models.CostPass || legacy.TierSignal != tierSignalPricingPaid {
				t.Fatalf("Beta's decision changed Alpha's compatibility record: %+v", legacy)
			}

			betaBefore := currentProviderOffering(*beta, offeringStateSharedModel)
			for _, cost := range []string{models.CostFree, models.CostPass} {
				if err := SetModelCost(alpha.ID, offeringStateSharedModel, cost); err != nil {
					t.Fatal(err)
				}
				if got := currentProviderOffering(*alpha, offeringStateSharedModel); got.Cost != cost || got.TierSignal != "manual" || got.Manual == nil || !*got.Manual {
					t.Fatalf("Alpha manual cost was not applied: %+v", got)
				}
				if got := currentProviderOffering(*beta, offeringStateSharedModel); !reflect.DeepEqual(got, betaBefore) {
					t.Fatalf("Alpha manual cost changed Beta: got %+v, want %+v", got, betaBefore)
				}
			}
		})
	}
}

func TestProviderOfferingStateDelistingIsolation(t *testing.T) {
	alpha, beta := sharedOfferingStateProviders()
	installOfferingStateTest(t, nil, alpha, beta)
	SyncModels()
	betaBefore := currentProviderOffering(*beta, offeringStateSharedModel)
	marked, total := applyProviderOfferingDelisting(*alpha, []string{offeringStateSharedModel}, nil)
	if marked != 1 || total != 1 {
		t.Fatalf("Alpha delisting counts = %d/%d, want 1/1", marked, total)
	}
	syncProviderDelistedProjection(List())
	if providerOfferingEligible(*alpha, offeringStateSharedModel) || !offeringForStateTest(t, alpha.ID).Delisted {
		t.Fatal("Alpha's delisted route remained active")
	}
	if !providerOfferingEligible(*beta, offeringStateSharedModel) || offeringForStateTest(t, beta.ID).Delisted {
		t.Fatal("Alpha's delisting disabled Beta's active route")
	}
	if got := currentProviderOffering(*beta, offeringStateSharedModel); !reflect.DeepEqual(got, betaBefore) {
		t.Fatalf("Alpha delisting changed Beta's overlay: %+v", got)
	}
	if legacy, ok := poolModelByID(offeringStateSharedModel); !ok || legacy.Delisted {
		t.Fatalf("shared model must remain active while Beta offers it: %+v", legacy)
	}
	if count, ids := providerDelistedSnapshot(alpha.ID, alpha.ModelIDs); count != 1 || !reflect.DeepEqual(ids, []string{offeringStateSharedModel}) {
		t.Fatalf("Alpha delisted snapshot = %d/%v", count, ids)
	}
	if count, ids := providerDelistedSnapshot(beta.ID, beta.ModelIDs); count != 0 || len(ids) != 0 {
		t.Fatalf("Beta inherited Alpha's delisted status: %d/%v", count, ids)
	}
	applyProviderOfferingDelisting(*beta, nil, map[string]bool{offeringStateSharedModel: true})
	if providerOfferingEligible(*alpha, offeringStateSharedModel) {
		t.Fatal("Beta's upstream presence cleared Alpha's delisting")
	}
	applyProviderOfferingDelisting(*alpha, nil, map[string]bool{offeringStateSharedModel: true})
	if !providerOfferingEligible(*alpha, offeringStateSharedModel) || !providerOfferingEligible(*beta, offeringStateSharedModel) {
		t.Fatal("Alpha's restored upstream presence did not restore both active routes")
	}
}

func TestProviderOfferingStateSparseOverlayInheritsLegacyRoutingState(t *testing.T) {
	alpha := &CustomProvider{
		ID: "alpha", Name: "Alpha", Enabled: true,
		ModelIDs: []string{offeringStateSharedModel},
	}
	installOfferingStateTest(t, []domain.Model{{
		ID: offeringStateSharedModel, Provider: alpha.Name, Source: models.SourceProvider,
		Cost: models.CostPass, TierSignal: tierSignalPricingPaid, Status: "inactive",
		Disabled: true, Delisted: true, Context: 4096, Output: 1024,
		ThinkingLevels: []string{"high"},
	}}, alpha)
	provRecheckMu.Lock()
	providerRecheckStat[alpha.ID] = &providerRecheckState{Offerings: map[string]domain.ModelOfferingState{
		offeringStateSharedModel: {UpstreamModelID: "vendor/shared"},
	}}
	provRecheckMu.Unlock()

	inherited := currentProviderOffering(*alpha, offeringStateSharedModel)
	if inherited.UpstreamModelID != "vendor/shared" || inherited.Status == nil || *inherited.Status != "inactive" || inherited.Disabled == nil || !*inherited.Disabled || inherited.Delisted == nil || !*inherited.Delisted {
		t.Fatalf("sparse overlay did not inherit legacy routing state: %+v", inherited)
	}
	if inherited.Context == nil || *inherited.Context != 4096 || inherited.Output == nil || *inherited.Output != 1024 || inherited.ThinkingLevels == nil || !reflect.DeepEqual(*inherited.ThinkingLevels, []string{"high"}) {
		t.Fatalf("sparse overlay did not inherit legacy capabilities: %+v", inherited)
	}
	if providerOfferingEligible(*alpha, offeringStateSharedModel) {
		t.Fatal("sparse overlay bypassed inherited disabled/delisted state")
	}
	if got := offeringForStateTest(t, alpha.ID); !got.Disabled || !got.Delisted || got.Status != "inactive" || got.UpstreamModelID != "vendor/shared" {
		t.Fatalf("projected sparse overlay disagrees with routing state: %+v", got)
	}

	active, cleared := "active", false
	provRecheckMu.Lock()
	providerRecheckStat[alpha.ID].Offerings[offeringStateSharedModel] = domain.ModelOfferingState{
		UpstreamModelID: "vendor/shared", Status: &active, Disabled: &cleared, Delisted: &cleared,
	}
	provRecheckMu.Unlock()
	overridden := currentProviderOffering(*alpha, offeringStateSharedModel)
	if overridden.Status == nil || *overridden.Status != "active" || overridden.Disabled == nil || *overridden.Disabled || overridden.Delisted == nil || *overridden.Delisted || !providerOfferingEligible(*alpha, offeringStateSharedModel) {
		t.Fatalf("explicit sparse overrides did not win over legacy flags: %+v", overridden)
	}
}

func TestProviderOfferingStateDuplicateNamesDoNotShareLegacySeed(t *testing.T) {
	alpha := &CustomProvider{
		ID: "alpha", Name: "Same Name", Enabled: true,
		ModelIDs: []string{offeringStateSharedModel},
	}
	beta := &CustomProvider{
		ID: "beta", Name: "Same Name", Enabled: true, Free: true,
		ModelIDs: []string{offeringStateSharedModel},
	}
	installOfferingStateTest(t, []domain.Model{{
		ID: offeringStateSharedModel, Provider: "Same Name", Source: models.SourceProvider,
		Cost: models.CostFree, TierSignal: "manual", Status: "inactive",
		Disabled: true, Delisted: true, Context: 8192,
	}}, alpha, beta)
	SyncModels()

	alphaState := currentProviderOffering(*alpha, offeringStateSharedModel)
	if alphaState.Cost != models.CostPass || alphaState.Access != domain.AccessPaidAPI || alphaState.Delisted == nil || *alphaState.Delisted || alphaState.Disabled != nil || alphaState.Status != nil || alphaState.Context != nil {
		t.Fatalf("Alpha inherited an ambiguous same-name legacy row: %+v", alphaState)
	}
	betaState := currentProviderOffering(*beta, offeringStateSharedModel)
	if betaState.Cost != models.CostFree || betaState.Access != domain.AccessUnknown || betaState.TierSignal != tierSignalProviderFree || betaState.Delisted == nil || *betaState.Delisted || betaState.Disabled != nil || betaState.Status != nil || betaState.Context != nil {
		t.Fatalf("Beta inherited an ambiguous same-name legacy row: %+v", betaState)
	}
	if !providerOfferingEligible(*alpha, offeringStateSharedModel) || !providerOfferingEligible(*beta, offeringStateSharedModel) {
		t.Fatal("ambiguous legacy ownership disabled independently seeded routes")
	}
}

func TestProviderOfferingStatePersistsAcrossRestart(t *testing.T) {
	alpha, beta := sharedOfferingStateProviders()
	installOfferingStateTest(t, nil, alpha, beta)
	SyncModels()
	applyProviderOfferingDecisions(*alpha, []offeringDecision{{ID: offeringStateSharedModel, Cost: models.CostPass, Signal: tierSignalPricingPaid}}, 100)
	applyProviderOfferingDecisions(*beta, []offeringDecision{{ID: offeringStateSharedModel, Cost: models.CostFree, Signal: tierSignalPricingZero}}, 200)
	applyProviderOfferingDelisting(*alpha, []string{offeringStateSharedModel}, nil)
	setProviderRecheckState(alpha.ID, providerRecheckState{LastCheckedMs: 1000, DelistedMarked: 1, DelistedModels: []string{offeringStateSharedModel}})
	setProviderRecheckState(beta.ID, providerRecheckState{LastCheckedMs: 2000, LastError: "timeout", NewQueued: 2, BatchID: "beta-batch"})
	context, output := 32768, 0
	thinking, inputs := []string{"low", "high"}, []string{"text", "image"}
	provRecheckMu.Lock()
	betaState := providerRecheckStat[beta.ID].Offerings[offeringStateSharedModel]
	betaState.Context, betaState.Output = &context, &output
	betaState.Disabled, betaState.MetaLocked = boolValue(false), boolValue(true)
	betaState.ThinkingLevels, betaState.InputModalities = &thinking, &inputs
	providerRecheckStat[beta.ID].Offerings[offeringStateSharedModel] = betaState
	saveProviderRecheckStateLocked()
	provRecheckMu.Unlock()
	wantAlpha := cloneProviderRecheckState(getProviderRecheckStateForTest(alpha.ID))
	wantBeta := cloneProviderRecheckState(getProviderRecheckStateForTest(beta.ID))
	Mu.Lock()
	SaveLocked()
	Mu.Unlock()
	accounts.SavePool()

	restartOfferingStateTest(t)
	for providerID, want := range map[string]providerRecheckState{alpha.ID: wantAlpha, beta.ID: wantBeta} {
		if got := getProviderRecheckStateForTest(providerID); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s state changed after restart: got %+v, want %+v", providerID, got, want)
		}
		if provider := Find(providerID); provider == nil || !reflect.DeepEqual(provider.ModelIDs, []string{offeringStateSharedModel}) {
			t.Fatalf("%s declaration was lost after restart: %+v", providerID, provider)
		}
	}
	if got := offeringForStateTest(t, alpha.ID); got.Access != domain.AccessPaidAPI || !got.Delisted {
		t.Fatalf("Alpha's paid/delisted state was lost after restart: %+v", got)
	}
	if got := offeringForStateTest(t, beta.ID); got.Access != domain.AccessFree || got.Delisted || got.Disabled || got.Context != context || got.Output != output || !reflect.DeepEqual(got.InputModalities, inputs) {
		t.Fatalf("Beta's free/active capabilities were lost after restart: %+v", got)
	}
	SyncModels()
	if got := getProviderRecheckStateForTest(beta.ID); !reflect.DeepEqual(got, wantBeta) {
		t.Fatalf("post-restart declaration sync changed Beta's persisted overlay: %+v", got)
	}
}

func TestProviderOfferingStateDeletionIsScoped(t *testing.T) {
	alpha, beta := sharedOfferingStateProviders()
	alpha.ModelIDs = append(alpha.ModelIDs, "alpha/only")
	beta.ModelIDs = append(beta.ModelIDs, "beta/only")
	installOfferingStateTest(t, nil, alpha, beta)
	SyncModels()
	applyProviderOfferingDecisions(*beta, []offeringDecision{{ID: offeringStateSharedModel, Cost: models.CostFree, Signal: tierSignalPricingZero}}, 200)
	applyProviderOfferingDelisting(*alpha, []string{offeringStateSharedModel}, nil)
	wantBeta := cloneProviderRecheckState(getProviderRecheckStateForTest(beta.ID))
	if err := DeleteCustomProvider(alpha.ID); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"after_delete", "after_restart"} {
		if stage == "after_restart" {
			restartOfferingStateTest(t)
		}
		if Find(alpha.ID) != nil {
			t.Fatalf("%s: Alpha's provider declaration remains", stage)
		}
		if provider := Find(beta.ID); provider == nil || !reflect.DeepEqual(provider.ModelIDs, beta.ModelIDs) {
			t.Fatalf("%s: Beta's declaration changed: %+v", stage, provider)
		}
		if got := getProviderRecheckStateForTest(beta.ID); !reflect.DeepEqual(got, wantBeta) {
			t.Fatalf("%s: deleting Alpha changed Beta's overlay: %+v", stage, got)
		}
		provRecheckMu.Lock()
		_, alphaRemains := providerRecheckStat[alpha.ID]
		provRecheckMu.Unlock()
		if alphaRemains {
			t.Fatalf("%s: Alpha's offering overlay remains", stage)
		}
		if _, ok := poolModelByID("alpha/only"); ok {
			t.Fatalf("%s: Alpha's exclusive model remains", stage)
		}
		if _, ok := poolModelByID("beta/only"); !ok {
			t.Fatalf("%s: Beta's exclusive model was removed", stage)
		}
		if got := offeringForStateTest(t, beta.ID); got.Access != domain.AccessFree || got.Delisted {
			t.Fatalf("%s: Beta's shared offering changed: %+v", stage, got)
		}
		if got := accounts.LoadPool().DefaultModel; got != offeringStateSharedModel {
			t.Fatalf("%s: deleting Alpha cleared the still-available default model: %q", stage, got)
		}
	}
	persisted, err := syncstate.Load(providerSyncFilePath)
	if err != nil || persisted[alpha.ID] != nil || persisted[beta.ID] == nil {
		t.Fatalf("persisted deletion was not scoped: states=%+v err=%v", persisted, err)
	}
}

func TestProviderOfferingStateLoadsLegacySyncJSON(t *testing.T) {
	alpha, beta := sharedOfferingStateProviders()
	beta.Free = true
	installOfferingStateTest(t, []domain.Model{{
		ID: offeringStateSharedModel, Provider: alpha.Name, Source: models.SourceProvider,
		Cost: models.CostPass, TierSignal: "manual", TierDecidedAt: 100, Status: "active", Delisted: true,
	}}, alpha, beta)
	legacyJSON := `{
		"alpha":{"lastCheckedMs":1000,"lastError":"timeout","delistedMarked":1,"delistedModels":["shared/model"],"missCounting":2,"newQueued":3,"batchId":"legacy-batch"},
		"beta":{"lastCheckedMs":2000,"delistedModels":[]}
	}`
	if err := os.WriteFile(providerSyncFilePath, []byte(legacyJSON), 0600); err != nil {
		t.Fatal(err)
	}
	provRecheckMu.Lock()
	provRecheckLoaded = false
	provRecheckMu.Unlock()
	LoadRecheckState()
	wantAlpha := providerRecheckState{
		LastCheckedMs: 1000, LastError: "timeout", DelistedMarked: 1,
		DelistedModels: []string{offeringStateSharedModel}, MissCounting: 2, NewQueued: 3, BatchID: "legacy-batch",
	}
	if got := getProviderRecheckStateForTest(alpha.ID); !reflect.DeepEqual(got, wantAlpha) {
		t.Fatalf("legacy display fields were not loaded: got %+v, want %+v", got, wantAlpha)
	}
	if got := getProviderRecheckStateForTest(beta.ID); got.LastCheckedMs != 2000 || got.Offerings != nil {
		t.Fatalf("legacy Beta state did not load without offerings: %+v", got)
	}
	SyncModels()
	alphaState := currentProviderOffering(*alpha, offeringStateSharedModel)
	if alphaState.Cost != models.CostPass || alphaState.Access != domain.AccessPaidAPI || alphaState.TierSignal != "manual" || alphaState.TierDecidedAt != 100 || alphaState.Delisted == nil || !*alphaState.Delisted || alphaState.Manual == nil || !*alphaState.Manual {
		t.Fatalf("legacy owner decisions were not migrated into Alpha: %+v", alphaState)
	}
	betaState := currentProviderOffering(*beta, offeringStateSharedModel)
	if betaState.Cost != models.CostFree || betaState.Access != domain.AccessUnknown || betaState.TierSignal != tierSignalProviderFree || betaState.Delisted == nil || *betaState.Delisted || (betaState.Manual != nil && *betaState.Manual) {
		t.Fatalf("Beta inherited Alpha's legacy decisions instead of its own classification: %+v", betaState)
	}
	gotAlpha := cloneProviderRecheckState(getProviderRecheckStateForTest(alpha.ID))
	gotAlpha.Offerings = nil
	if !reflect.DeepEqual(gotAlpha, wantAlpha) {
		t.Fatalf("adding offerings changed legacy display fields: got %+v, want %+v", gotAlpha, wantAlpha)
	}
	persisted, err := syncstate.Load(providerSyncFilePath)
	if err != nil || persisted[alpha.ID] == nil || persisted[beta.ID] == nil || len(persisted[alpha.ID].Offerings) != 1 || len(persisted[beta.ID].Offerings) != 1 {
		t.Fatalf("legacy migration did not persist both scoped overlays: states=%+v err=%v", persisted, err)
	}
	if syncProviderOfferingDeclarations(List(), models.Snapshot()) {
		t.Fatal("legacy offering migration is not idempotent")
	}
}
