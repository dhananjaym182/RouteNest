package providers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"routenest/internal/accounts"
	"routenest/internal/connections"
	"routenest/internal/domain"
	"routenest/internal/models"
)

// isolateProviderFiles points every store SaveCustomProvider touches at a
// temporary directory, so these tests can never write to a real data directory.
func isolateProviderFiles(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	oldProvPath, oldPoolPath, oldConnPath := FilePath, accounts.PoolPath, connections.FilePath
	FilePath = filepath.Join(tmp, ".routenest-providers.json")
	accounts.PoolPath = filepath.Join(tmp, ".routenest-accounts.json")
	connections.FilePath = filepath.Join(tmp, ".routenest-connections.json")
	oldProvs, oldLoaded, oldPool := CustomProviders, Loaded, accounts.Pool
	CustomProviders, Loaded = nil, true
	provRecheckMu.Lock()
	oldSyncPath, oldSyncState, oldOwner, oldSyncLoaded := providerSyncFilePath, providerRecheckStat, providerRuntimeStateOwner, provRecheckLoaded
	providerSyncFilePath = filepath.Join(tmp, ".routenest-providers-sync.json")
	providerRecheckStat = map[string]*providerRecheckState{}
	provRecheckLoaded = true
	providerRuntimeStateOwner = nil
	provRecheckMu.Unlock()
	t.Cleanup(func() {
		FilePath, accounts.PoolPath, connections.FilePath = oldProvPath, oldPoolPath, oldConnPath
		CustomProviders, Loaded, accounts.Pool = oldProvs, oldLoaded, oldPool
		provRecheckMu.Lock()
		providerSyncFilePath, providerRecheckStat, providerRuntimeStateOwner, provRecheckLoaded = oldSyncPath, oldSyncState, oldOwner, oldSyncLoaded
		provRecheckMu.Unlock()
	})
	return tmp
}

func TestSaveCustomProviderAccessPolicy(t *testing.T) {
	isolateProviderFiles(t)
	save := func(name string, policy domain.ProviderAccessPolicy) (CustomProvider, error) {
		return SaveCustomProvider(CustomProvider{Name: name, BaseURL: "https://" + strings.ToLower(name) + ".example.com", AccessPolicy: policy})
	}
	// The default is stored as empty, so providers nobody touched look exactly
	// like they did before the setting existed.
	for i, tc := range []struct {
		in   domain.ProviderAccessPolicy
		want domain.ProviderAccessPolicy
	}{
		{"", ""}, {"detect", ""}, {" DETECT ", ""},
		{"all_free", domain.AccessPolicyAllFree}, {" Allow_All ", domain.AccessPolicyAllowAll},
	} {
		got, err := save("P"+string(rune('a'+i)), tc.in)
		if err != nil || got.AccessPolicy != tc.want {
			t.Errorf("save(%q) = %q, %v; want %q", tc.in, got.AccessPolicy, err, tc.want)
		}
	}
	// A misspelling must be refused. Guessing could allow paid models.
	for _, bad := range []domain.ProviderAccessPolicy{"free", "both", "allow-all", "yes", "true"} {
		_, err := save("Bad"+string(bad), bad)
		if err == nil || !strings.Contains(err.Error(), "accessPolicy") {
			t.Errorf("save(%q): err = %v, want an accessPolicy error", bad, err)
		}
	}
}

func TestAccessPolicySurvivesRestartAndToggle(t *testing.T) {
	isolateProviderFiles(t)
	saved, err := SaveCustomProvider(CustomProvider{Name: "Keep", BaseURL: "https://keep.example.com", Enabled: true, AccessPolicy: domain.AccessPolicyAllowAll})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(FilePath)
	if err != nil || !strings.Contains(string(raw), `"accessPolicy": "allow_all"`) {
		t.Fatalf("the setting is not on disk: %v\n%s", err, raw)
	}
	// Restart: forget memory, read the file.
	Mu.Lock()
	CustomProviders, Loaded = nil, false
	Mu.Unlock()
	if got := Find(saved.ID); got == nil || got.AccessPolicy != domain.AccessPolicyAllowAll {
		t.Fatalf("after reload: %+v", got)
	}
	// The enable/disable toggle posts the whole provider back. Doing that with
	// the stored record must not reset the administrator's choice.
	again := *Find(saved.ID)
	again.Enabled = false
	if _, err := SaveCustomProvider(again); err != nil {
		t.Fatal(err)
	}
	if got := Find(saved.ID); got == nil || got.AccessPolicy != domain.AccessPolicyAllowAll || got.Enabled {
		t.Fatalf("after toggle: %+v", got)
	}
}

func TestProvidersSavedBeforeTheSettingExistedKeepDetecting(t *testing.T) {
	isolateProviderFiles(t)
	legacy := `[{"id":"prov_old","name":"Old","baseURL":"https://old.example.com/v1","enabled":true,"free":true,"modelIds":["m"]}]`
	if err := os.WriteFile(FilePath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	Mu.Lock()
	CustomProviders, Loaded = nil, false
	Mu.Unlock()
	got := Find("prov_old")
	if got == nil || got.AccessPolicy != "" || !got.Free {
		t.Fatalf("legacy provider = %+v, want default policy and its free flag intact", got)
	}
}

// The policy is read when a route is checked, not stored per model, so changing
// it takes effect at once. The detected classes in the sync state stay as they
// were: the administrator can still see which models are paid.
func TestChangingThePolicyNeedsNoResync(t *testing.T) {
	isolateProviderFiles(t)
	saved, err := SaveCustomProvider(CustomProvider{
		Name: "Mixed", BaseURL: "https://mixed.example.com", Enabled: true,
		ModelIDs: []string{"deepseek-free", "plain-model"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Give the offerings the classes a sync would have recorded.
	provRecheckMu.Lock()
	providerRecheckStat[saved.ID] = &providerRecheckState{Offerings: map[string]domain.ModelOfferingState{
		"deepseek-free": {UpstreamModelID: "deepseek-free", Cost: models.CostFree, Access: domain.AccessFree, TierSignal: "suffix-free"},
		"plain-model":   {UpstreamModelID: "plain-model", Cost: models.CostPass, Access: domain.AccessPaidAPI, TierSignal: "paid-default"},
	}}
	provRecheckMu.Unlock()

	eligible := func() (free, paid bool, paidClass domain.AccessClass) {
		offerings, err := SnapshotModelOfferings()
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range offerings {
			switch {
			case o.ProviderID == domain.ProviderInstanceID(saved.ID) && o.ModelID == "deepseek-free":
				free = o.FreeOnlyEligible()
			case o.ProviderID == domain.ProviderInstanceID(saved.ID) && o.ModelID == "plain-model":
				paid, paidClass = o.FreeOnlyEligible(), o.Access
			}
		}
		return
	}
	if free, paid, class := eligible(); !free || paid || class != domain.AccessPaidAPI {
		t.Fatalf("default: free=%v paid=%v class=%s, want true/false/paid_api", free, paid, class)
	}
	saved.AccessPolicy = domain.AccessPolicyAllowAll
	if _, err := SaveCustomProvider(saved); err != nil {
		t.Fatal(err)
	}
	if free, paid, class := eligible(); !free || !paid || class != domain.AccessPaidAPI {
		t.Fatalf("allow_all: free=%v paid=%v class=%s, want true/true/paid_api (allowed, still labelled paid)", free, paid, class)
	}
	saved.AccessPolicy = domain.AccessPolicyDetect
	if _, err := SaveCustomProvider(saved); err != nil {
		t.Fatal(err)
	}
	if _, paid, _ := eligible(); paid {
		t.Fatal("switching back to detect must block the paid model again")
	}
}
