package config

import (
	"fmt"
	"time"
)

// RuntimeStateOutagePolicy controls admission when durable enforcement fails.
type RuntimeStateOutagePolicy string

const (
	RuntimeStateFailClosed RuntimeStateOutagePolicy = "fail_closed"
	RuntimeStateFailOpen   RuntimeStateOutagePolicy = "fail_open"
)

func (p RuntimeStateOutagePolicy) Valid() bool {
	return p == RuntimeStateFailClosed || p == RuntimeStateFailOpen
}

// RuntimeStateTopology describes the deployment's enforcement requirements.
// Selecting multi_replica does not make a local file backend replica-safe.
type RuntimeStateTopology string

const (
	RuntimeStateSingleNode                   RuntimeStateTopology = "single_node"
	RuntimeStateMultiReplica                 RuntimeStateTopology = "multi_replica"
	DefaultRuntimeStateSnapshotMaxAgeSeconds                      = 30
)

func (t RuntimeStateTopology) Valid() bool {
	return t == RuntimeStateSingleNode || t == RuntimeStateMultiReplica
}

// RuntimeStatePolicy is the persisted durability policy. Fail-open admission
// requires a separate acknowledgement of best-effort budget enforcement.
type RuntimeStatePolicy struct {
	OutagePolicy          RuntimeStateOutagePolicy `json:"outagePolicy"`
	AcknowledgeFailOpen   bool                     `json:"acknowledgeFailOpen"`
	DeploymentTopology    RuntimeStateTopology     `json:"deploymentTopology"`
	SnapshotMaxAgeSeconds int                      `json:"snapshotMaxAgeSeconds"`
}

func DefaultRuntimeStatePolicy() RuntimeStatePolicy {
	return RuntimeStatePolicy{
		OutagePolicy:          RuntimeStateFailClosed,
		DeploymentTopology:    RuntimeStateSingleNode,
		SnapshotMaxAgeSeconds: DefaultRuntimeStateSnapshotMaxAgeSeconds,
	}
}

// EffectiveOutagePolicy is safe even for an unnormalized or hand-built config.
func (p RuntimeStatePolicy) EffectiveOutagePolicy() RuntimeStateOutagePolicy {
	if p.OutagePolicy == RuntimeStateFailOpen && p.AcknowledgeFailOpen {
		return RuntimeStateFailOpen
	}
	return RuntimeStateFailClosed
}

// Normalize makes stale, absent, and unsafe persisted values secure by default.
func (p *RuntimeStatePolicy) Normalize() {
	p.OutagePolicy = p.EffectiveOutagePolicy()
	if !p.DeploymentTopology.Valid() {
		p.DeploymentTopology = RuntimeStateSingleNode
	}
	if !validSnapshotMaxAge(p.SnapshotMaxAgeSeconds) {
		p.SnapshotMaxAgeSeconds = DefaultRuntimeStateSnapshotMaxAgeSeconds
	}
}

// Validate rejects invalid administrative input before normalization can hide it.
func (p RuntimeStatePolicy) Validate() error {
	if !p.OutagePolicy.Valid() {
		return fmt.Errorf("invalid runtime state outage policy")
	}
	if p.OutagePolicy == RuntimeStateFailOpen && !p.AcknowledgeFailOpen {
		return fmt.Errorf("runtime state fail_open requires acknowledgeFailOpen")
	}
	if !p.DeploymentTopology.Valid() {
		return fmt.Errorf("invalid runtime state deployment topology")
	}
	if !validSnapshotMaxAge(p.SnapshotMaxAgeSeconds) {
		return fmt.Errorf("runtime state snapshotMaxAgeSeconds must be a positive duration")
	}
	return nil
}

// SnapshotMaxAge returns a bounded, positive duration even before normalization.
func (p RuntimeStatePolicy) SnapshotMaxAge() time.Duration {
	if !validSnapshotMaxAge(p.SnapshotMaxAgeSeconds) {
		return DefaultRuntimeStateSnapshotMaxAgeSeconds * time.Second
	}
	return time.Duration(p.SnapshotMaxAgeSeconds) * time.Second
}

func validSnapshotMaxAge(seconds int) bool {
	return seconds > 0 && int64(seconds) <= int64((1<<63-1)/time.Second)
}
