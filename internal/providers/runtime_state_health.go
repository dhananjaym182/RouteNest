package providers

import (
	"time"

	"routenest/internal/budget"
	"routenest/internal/config"
	"routenest/internal/domain"
	"routenest/internal/providerstate"
)

// RuntimeStateHealth returns the one redacted status used by public readiness
// and provider-state administration. It combines both semantic stores because
// either facet becoming unavailable makes durable routing state degraded.
func RuntimeStateHealth() domain.RuntimeStateHealth {
	health := domain.RuntimeStateHealth{
		Backend: "unconfigured", Degraded: true, Stale: true,
		Enforcement: budget.FailClosed, Reason: "backend_unavailable",
	}
	if budget.HasDefault() {
		health = budget.Default().Health()
	}
	if providerstate.HasDefault() {
		health = mergeRuntimeStateHealth(health, providerstate.Default().Health())
	} else {
		health.Ready, health.SingleNodeReady = false, false
		health.Degraded, health.Stale, health.Reason = true, true, "backend_unavailable"
	}

	policy := config.DefaultRuntimeStatePolicy()
	if ConfigProvider != nil {
		policy = liveConfig().RuntimeState
		policy.Normalize()
	}
	if health.Enforcement != "best_effort" {
		health.Enforcement = string(policy.EffectiveOutagePolicy())
	}
	if policy.DeploymentTopology == config.RuntimeStateMultiReplica && !health.MultiReplicaSupported {
		health.Ready = false
		health.Degraded = true
		health.Reason = "multi_replica_unsupported"
	}
	lastSuccess := health.LastSuccessfulRead
	if health.LastSuccessfulWrite.After(lastSuccess) {
		lastSuccess = health.LastSuccessfulWrite
	}
	if health.Degraded && !lastSuccess.IsZero() && time.Since(lastSuccess) > policy.SnapshotMaxAge() {
		health.Stale = true
	}
	return sanitizeRuntimeStateHealth(health)
}

func mergeRuntimeStateHealth(left, right domain.RuntimeStateHealth) domain.RuntimeStateHealth {
	if left.Backend == "" || left.Backend == "unconfigured" {
		left.Backend = right.Backend
	}
	left.Ready = left.Ready && right.Ready
	left.SingleNodeReady = left.SingleNodeReady && right.SingleNodeReady
	left.MultiReplicaSupported = left.MultiReplicaSupported && right.MultiReplicaSupported
	left.Degraded = left.Degraded || right.Degraded
	left.Stale = left.Stale || right.Stale
	if right.LastSuccessfulRead.After(left.LastSuccessfulRead) {
		left.LastSuccessfulRead = right.LastSuccessfulRead
	}
	if right.LastSuccessfulWrite.After(left.LastSuccessfulWrite) {
		left.LastSuccessfulWrite = right.LastSuccessfulWrite
	}
	if right.Degraded && right.Reason != "" {
		left.Reason = right.Reason
	}
	return left
}

func sanitizeRuntimeStateHealth(health domain.RuntimeStateHealth) domain.RuntimeStateHealth {
	switch health.Backend {
	case "local_file", "shared", "memory", "runtime_state", "unconfigured":
	default:
		health.Backend = "runtime_state"
	}
	switch health.Reason {
	case "", "backend_unavailable", "backend_not_ready", "multi_replica_unsupported", "initializing", "best_effort_accounting", "persistence_failed", "state_corrupt", "state_unreadable":
	default:
		health.Reason = "backend_unavailable"
	}
	return health
}
