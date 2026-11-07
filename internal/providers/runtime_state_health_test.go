package providers

import (
	"testing"
	"time"

	"routenest/internal/config"
	"routenest/internal/providerstate"
)

func TestRuntimeStateHealthRejectsLocalMultiReplicaReadiness(t *testing.T) {
	installTestBudget(t, time.Now)
	if err := providerstate.Publish(providerstate.NewStore(time.Now)); err != nil {
		t.Fatal(err)
	}
	previous := ConfigProvider
	cfg := config.DefaultProxy()
	cfg.RuntimeState.DeploymentTopology = config.RuntimeStateMultiReplica
	ConfigProvider = func() *config.Proxy { return cfg }
	t.Cleanup(func() { ConfigProvider = previous })

	health := RuntimeStateHealth()
	if health.Ready || !health.SingleNodeReady || health.MultiReplicaSupported || !health.Degraded ||
		health.Reason != "multi_replica_unsupported" {
		t.Fatalf("multi-replica local health = %+v", health)
	}

	cfg.RuntimeState.DeploymentTopology = config.RuntimeStateSingleNode
	health = RuntimeStateHealth()
	if !health.Ready || !health.SingleNodeReady || health.MultiReplicaSupported || health.Degraded || health.Reason != "" {
		t.Fatalf("single-node health = %+v", health)
	}
}
