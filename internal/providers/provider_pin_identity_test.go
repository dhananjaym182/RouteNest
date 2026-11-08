package providers

import (
	"reflect"
	"testing"
	"time"

	"routenest/internal/config"
)

func TestResolveNamedProviderIdentityPin(t *testing.T) {
	const modelID = "deepseek-v4-flash"
	const antseedID = "prov_18e5f4f23187e173"
	for _, strategy := range []string{"round_robin", "fill", "random", "quality", "weighted_round_robin"} {
		t.Run(strategy, func(t *testing.T) {
			previousProvider := ConfigProvider
			cfg := config.DefaultProxy()
			cfg.Strategy = strategy
			ConfigProvider = func() *config.Proxy { return cfg }
			t.Cleanup(func() { ConfigProvider = previousProvider })
			installProviders(t,
				&CustomProvider{ID: antseedID, Name: "AntSeed", ModelIDs: []string{modelID}, Enabled: true, Priority: 10},
				&CustomProvider{ID: "prov_name_collision", Name: antseedID, ModelIDs: []string{modelID}, Enabled: true, Priority: 1},
			)
			for _, pin := range []string{antseedID, "  " + antseedID + "  "} {
				got := ResolveNamedForModel(modelID, pin)
				if len(got) != 1 || got[0].ID != antseedID {
					t.Fatalf("pin %q selected %+v, want exactly AntSeed instance %s", pin, got, antseedID)
				}
				if !NameServesModel(pin, modelID) {
					t.Fatalf("validator rejected AntSeed instance ID %q", pin)
				}
			}
		})
	}
}

func TestProviderIdentityPinSurvivesRename(t *testing.T) {
	const antseedID = "prov_18e5f4f23187e173"
	installProviders(t,
		&CustomProvider{ID: antseedID, Name: "AntSeed", ModelIDs: []string{"m"}, Enabled: true},
		&CustomProvider{ID: "replacement", Name: "AntSeed", ModelIDs: []string{"m"}, Enabled: true},
	)
	Mu.Lock()
	CustomProviders[0].Name = "AntSeed renamed"
	Mu.Unlock()
	got := ResolveNamedForModel("m", antseedID)
	if len(got) != 1 || got[0].ID != antseedID || got[0].Name != "AntSeed renamed" {
		t.Fatalf("renamed instance pin selected %+v", got)
	}
	if !NameServesModel(antseedID, "m") {
		t.Fatal("renamed instance pin became invalid")
	}
}

func TestProviderIdentityPinCannotBecomeNameFallback(t *testing.T) {
	const pinID = "prov_18e5f4f23187e173"
	const modelID = "deepseek-v4-flash"
	for _, state := range []string{"disabled", "model-absent", "cooling", "removed"} {
		t.Run(state, func(t *testing.T) {
			providers := []*CustomProvider{{ID: "prov_name_collision", Name: pinID, ModelIDs: []string{modelID}, Enabled: true}}
			if state != "removed" {
				target := &CustomProvider{ID: pinID, Name: "AntSeed", ModelIDs: []string{modelID}, Enabled: state != "disabled"}
				if state == "model-absent" {
					target.ModelIDs = []string{"other-model"}
				}
				providers = append(providers, target)
			}
			installProviders(t, providers...)
			if state == "cooling" {
				Mu.Lock()
				Cooldowns[pinID+":"+modelID] = time.Now().Add(time.Hour)
				Mu.Unlock()
			}
			if got := ResolveNamedForModel(modelID, pinID); len(got) != 0 {
				t.Fatalf("%s ID pin fell through to %+v", state, got)
			}
			if state != "cooling" && NameServesModel(pinID, modelID) {
				t.Fatalf("%s ID pin was validated through a display-name collision", state)
			}
		})
	}
}

func TestNamedProviderGroupPinsRemainCompatible(t *testing.T) {
	installProviders(t,
		&CustomProvider{ID: "prov_antseed_1", Name: "AntSeed", ModelIDs: []string{"m"}, Enabled: true, Priority: 1},
		&CustomProvider{ID: "prov_antseed_2", Name: "antseed", ModelIDs: []string{"m"}, Enabled: true, Priority: 2},
	)
	got := ResolveNamedForModel("m", " ANTSEED ")
	ids := make([]string, 0, len(got))
	for _, provider := range got {
		ids = append(ids, provider.ID)
	}
	if !reflect.DeepEqual(ids, []string{"prov_antseed_1", "prov_antseed_2"}) {
		t.Fatalf("legacy name-group pin changed: %v", ids)
	}
	if !NameServesModel(" ANTSEED ", "m") {
		t.Fatal("legacy name-group validation changed")
	}
}

func TestArbitraryKnownProviderIDWinsOverName(t *testing.T) {
	installProviders(t,
		&CustomProvider{ID: "antseed_instance", Name: "AntSeed", ModelIDs: []string{"m"}, Enabled: true},
		&CustomProvider{ID: "other", Name: "antseed_instance", ModelIDs: []string{"m"}, Enabled: true},
	)
	got := ResolveNamedForModel("m", "antseed_instance")
	if len(got) != 1 || got[0].ID != "antseed_instance" {
		t.Fatalf("known ID selected display-name collision: %+v", got)
	}
}

func TestReservedProviderIdentityPinCannotBecomeNameFallback(t *testing.T) {
	previous := config.Live()
	policy := previous.Clone()
	policy.ProviderModelPrefixes = map[string]string{"retired_pin": "retired-antseed-instance"}
	config.Publish(policy)
	t.Cleanup(func() { config.Publish(previous) })
	installProviders(t, &CustomProvider{
		ID: "replacement-provider", Name: "retired-antseed-instance", ModelIDs: []string{"m"}, Enabled: true,
	})
	if got := ResolveNamedForModel("m", "retired-antseed-instance"); len(got) != 0 {
		t.Fatalf("reserved deleted provider ID became a display-name pin: %+v", got)
	}
	if NameServesModel("retired-antseed-instance", "m") {
		t.Fatal("reserved deleted provider ID validated through another provider's name")
	}
}
