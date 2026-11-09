package onboarding

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"

	"routenest/internal/platform"
)

const FileName = ".routenest-onboarding.json"
const stateVersion = 1

type State struct {
	Version                         int                  `json:"version"`
	VerifiedProviders               map[string]time.Time `json:"verifiedProviders,omitempty"`
	FirstSuccessfulGatewayRequestAt *time.Time           `json:"firstSuccessfulGatewayRequestAt,omitempty"`
}

// stateMu serializes read-modify-write operations across the admin and request
// completion owners so a verification cannot overwrite a completed milestone.
var stateMu sync.Mutex

func Empty() State {
	return State{Version: stateVersion, VerifiedProviders: map[string]time.Time{}}
}

func Load(path string) (State, error) {
	stateMu.Lock()
	defer stateMu.Unlock()
	return load(path)
}

func load(path string) (State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Empty(), nil
	}
	if err != nil {
		return State{}, err
	}
	state := Empty()
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, err
	}
	if state.Version != stateVersion || state.VerifiedProviders == nil || len(state.VerifiedProviders) > 1024 {
		return State{}, errors.New("invalid onboarding state")
	}
	return state, nil
}

func Save(path string, state State) error {
	stateMu.Lock()
	defer stateMu.Unlock()
	return save(path, state)
}

func save(path string, state State) error {
	state.Version = stateVersion
	if state.VerifiedProviders == nil {
		state.VerifiedProviders = map[string]time.Time{}
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return platform.WriteFileAtomicDurable(path, data, 0600)
}

// Update applies a mutation to the latest persisted state. The callback returns
// whether anything changed; unchanged state is not rewritten.
func Update(path string, mutate func(*State) bool) (State, error) {
	stateMu.Lock()
	defer stateMu.Unlock()
	state, err := load(path)
	if err != nil {
		return State{}, err
	}
	if mutate(&state) {
		if err := save(path, state); err != nil {
			return State{}, err
		}
	}
	return state, nil
}

// RecordSuccessfulGatewayRequest persists the first actual successful gateway
// completion independently of the request log's retention and clear operations.
func RecordSuccessfulGatewayRequest(path string, at time.Time) error {
	if at.IsZero() {
		return errors.New("successful gateway request completion time is required")
	}
	_, err := Update(path, func(state *State) bool {
		if state.FirstSuccessfulGatewayRequestAt != nil && !state.FirstSuccessfulGatewayRequestAt.IsZero() {
			return false
		}
		at = at.UTC()
		state.FirstSuccessfulGatewayRequestAt = &at
		return true
	})
	return err
}
