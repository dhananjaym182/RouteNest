package domain

import "time"

// RuntimeStateHealth is the redacted durability/enforcement status exposed to
// health and administration surfaces. It deliberately carries no backend
// address, file path, credential, proxy, or raw error text.
type RuntimeStateHealth struct {
	Backend               string    `json:"backend"`
	Ready                 bool      `json:"ready"`
	SingleNodeReady       bool      `json:"singleNodeReady"`
	MultiReplicaSupported bool      `json:"multiReplicaSupported"`
	Degraded              bool      `json:"degraded"`
	Stale                 bool      `json:"stale"`
	Enforcement           string    `json:"enforcement"`
	LastSuccessfulRead    time.Time `json:"lastSuccessfulRead,omitzero"`
	LastSuccessfulWrite   time.Time `json:"lastSuccessfulWrite,omitzero"`
	Reason                string    `json:"reason,omitempty"`
}
