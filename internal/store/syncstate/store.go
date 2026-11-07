// Package syncstate persists provider catalog recheck status, offering overlays,
// and additive provider runtime state. It performs no upstream calls.
package syncstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"routenest/internal/domain"
	"routenest/internal/platform"
)

// FileName is the data file holding provider recheck status.
const FileName = ".routenest-providers-sync.json"

// ErrInvalidDocument identifies structurally decoded input that is not the
// required top-level provider object (for example JSON null).
var ErrInvalidDocument = errors.New("syncstate: invalid provider state document")

// State is the persisted sync, offering, and runtime state for one provider.
type State struct {
	LastCheckedMs  int64                                `json:"lastCheckedMs"`
	LastError      string                               `json:"lastError,omitempty"`
	DelistedMarked int                                  `json:"delistedMarked,omitempty"`
	DelistedModels []string                             `json:"delistedModels"`
	MissCounting   int                                  `json:"missCounting,omitempty"`
	NewQueued      int                                  `json:"newQueued,omitempty"`
	BatchID        string                               `json:"batchId,omitempty"`
	Offerings      map[string]domain.ModelOfferingState `json:"offerings,omitempty"`
	Runtime        *RuntimeState                        `json:"runtime,omitempty"`
}

// CloneState protects the live provider state from aliasing through snapshots
// and JSON persistence. Optional runtime fields and offering capability slices
// require a deep copy.
func CloneState(state State) State {
	if state.DelistedModels != nil {
		// [] is an authoritative successful sync with no delistings; null means
		// an older/unknown state. Keep that distinction across clone/save/load.
		state.DelistedModels = append([]string{}, state.DelistedModels...)
	}
	if state.Offerings != nil {
		state.Offerings = cloneOfferings(state.Offerings)
	}
	if state.Runtime != nil {
		runtime := state.Runtime.Clone()
		state.Runtime = &runtime
	}
	return state
}

func cloneOfferings(in map[string]domain.ModelOfferingState) map[string]domain.ModelOfferingState {
	out := make(map[string]domain.ModelOfferingState, len(in))
	for modelID, offering := range in {
		out[modelID] = offering.Clone()
	}
	return out
}

// Load reads path. Only a missing file yields nil and no error. Other read
// errors and decode errors are returned so callers cannot mistake unavailable
// durable enforcement state for a fresh installation.
func Load(path string) (map[string]*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var states map[string]*State
	if err := json.Unmarshal(data, &states); err != nil {
		return nil, err
	}
	if states == nil {
		return nil, fmt.Errorf("%w: top-level value must be an object", ErrInvalidDocument)
	}
	for providerID, state := range states {
		if state == nil {
			continue
		}
		clone := CloneState(*state)
		states[providerID] = &clone
	}
	return states, nil
}

// Save writes the state table atomically.
func Save(path string, states map[string]State) error {
	cloned := make(map[string]State, len(states))
	for providerID, state := range states {
		cloned[providerID] = CloneState(state)
	}
	data, err := json.MarshalIndent(cloned, "", "  ")
	if err != nil {
		return err
	}
	return platform.WriteFileAtomic(path, data, 0600)
}
