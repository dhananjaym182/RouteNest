package models

import (
	"errors"
	"sort"

	"routenest/internal/domain"
)

var ErrNonAuthoritative = errors.New("catalogue snapshot is not authoritative")

type PresenceState struct {
	Delisted  bool
	Protected bool
}

type ReconcilePolicy struct {
	ProviderID       domain.ProviderInstanceID
	MissingThreshold int
	Authoritative    bool
}

type PresenceResult struct {
	ToDelist        []string
	ToRestore       []string
	New             []string
	MissingCounting int
}

// ReconcilePresence compares one successful authoritative catalogue snapshot
// with declared/current offerings. It updates consecutive-miss counters only
// after validating the snapshot, so errors and empty lists are non-destructive.
func ReconcilePresence(
	declared []string,
	discovered map[string]bool,
	current map[string]PresenceState,
	misses map[string]int,
	policy ReconcilePolicy,
) (PresenceResult, error) {
	if !policy.Authoritative {
		return PresenceResult{}, ErrNonAuthoritative
	}
	if len(discovered) == 0 {
		return PresenceResult{}, ErrEmptySource
	}
	threshold := policy.MissingThreshold
	if threshold <= 0 {
		threshold = 2
	}

	nextMisses := make(map[string]int, len(misses))
	for id, count := range misses {
		nextMisses[id] = count
	}
	declaredSet := make(map[string]bool, len(declared))
	result := PresenceResult{}
	for _, id := range declared {
		if id == "" || declaredSet[id] {
			continue
		}
		declaredSet[id] = true
		state := current[id]
		if discovered[id] {
			delete(nextMisses, id)
			if state.Delisted {
				result.ToRestore = append(result.ToRestore, id)
			}
			continue
		}
		if state.Protected || state.Delisted {
			delete(nextMisses, id)
			continue
		}
		nextMisses[id]++
		if nextMisses[id] >= threshold {
			result.ToDelist = append(result.ToDelist, id)
		} else {
			result.MissingCounting++
		}
	}
	for id := range nextMisses {
		if !declaredSet[id] {
			delete(nextMisses, id)
		}
	}
	for id := range discovered {
		if !declaredSet[id] {
			if _, exists := current[id]; !exists {
				result.New = append(result.New, id)
			}
		}
	}

	sort.Strings(result.ToDelist)
	sort.Strings(result.ToRestore)
	sort.Strings(result.New)
	clear(misses)
	for id, count := range nextMisses {
		misses[id] = count
	}
	return result, nil
}
