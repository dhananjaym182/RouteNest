package models

import (
	"testing"

	"routenest/internal/domain"
)

func TestProviderNameDoesNotCreateUndeclaredOfferings(t *testing.T) {
	record := domain.Model{ID: "shared", Source: SourceProvider, Provider: "AntSeed", Cost: CostFree, Status: "active"}
	for _, undeclared := range [][]string{nil, {"different"}} {
		offerings := ProjectOfferings([]domain.Model{record}, []OfferingSource{
			{Instance: domain.ProviderInstance{ID: "declared", Name: "AntSeed", Enabled: true}, ModelIDs: []string{"shared"}},
			{Instance: domain.ProviderInstance{ID: "sibling", Name: "AntSeed", Enabled: true}, ModelIDs: undeclared},
		})
		declared := false
		for _, offering := range offerings {
			if offering.ModelID == record.ID {
				if offering.ProviderID != "declared" {
					t.Fatalf("display name created an undeclared offering: %+v", offering)
				}
				declared = true
			}
		}
		if !declared {
			t.Fatal("declared provider lost its offering")
		}
	}
}

func TestSparseProviderOverlayDoesNotBorrowForeignCapabilities(t *testing.T) {
	legacy := domain.Model{ID: "shared@revision", Source: SourceZen, Provider: "opencode", Cost: CostFree, Context: 32768, Disabled: true, ThinkingLevels: []string{"low"}}
	got := ProjectOfferings([]domain.Model{legacy}, []OfferingSource{{
		Instance: domain.ProviderInstance{ID: "antseed", Name: "AntSeed", Enabled: true},
		ModelIDs: []string{legacy.ID},
		States:   map[string]domain.ModelOfferingState{legacy.ID: {UpstreamModelID: legacy.ID, Access: domain.AccessPaidAPI, Cost: CostPass}},
	}})
	if len(got) != 1 || got[0].Disabled || got[0].Context != 0 || len(got[0].ThinkingLevels) != 0 || got[0].Access != domain.AccessPaidAPI {
		t.Fatalf("AntSeed borrowed OpenCode's offering evidence: %+v", got)
	}
}
