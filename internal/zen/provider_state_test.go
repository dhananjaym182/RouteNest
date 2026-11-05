package zen

import (
	"testing"
	"time"

	"routenest/internal/domain"
	"routenest/internal/providerstate"
)

func TestZenProxyCooldownMirrorsEgressState(t *testing.T) {
	store := providerstate.NewStore(time.Now)
	if err := providerstate.Publish(store); err != nil {
		t.Fatal(err)
	}
	cooldownZenProxy(2, time.Minute)
	reasons := store.Blocking(domain.AvailabilityScope{ProviderID: "opencode", EgressID: "proxy-2"})
	if len(reasons) != 1 || reasons[0].Kind != domain.ObservationRateLimited {
		t.Fatalf("shadow state = %+v", reasons)
	}
}
