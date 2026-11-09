package models

import (
	"routenest/internal/accounts"
	"routenest/internal/domain"
)

// DeleteOutcome reports how Delete handled a request.
type DeleteOutcome int

const (
	// DeleteNotFound: no record has the id.
	DeleteNotFound DeleteOutcome = iota
	// DeleteDeleted: the first record with the id was removed.
	DeleteDeleted
	// DeleteProtected: the first record is a synchronized, non-delisted model,
	// which is maintained by its source and cannot be removed by hand.
	DeleteProtected
	// DeleteAmbiguous: more than one compatibility record has the requested ID,
	// so an ID-only request cannot identify one model offering safely.
	DeleteAmbiguous
)

// Capabilities are optional metadata updates. A nil slice leaves that field
// unchanged; an empty non-nil slice clears it.
type Capabilities struct {
	ThinkingLevels   []string
	InputModalities  []string
	OutputModalities []string
}

// Add appends a record. Callers check for existing ids first.
func Add(m domain.Model) {
	Edit(func(p *domain.AccountPool) bool {
		p.Models = append(p.Models, m)
		return true
	})
}

// Delete removes an unambiguous compatibility record with id. Custom and
// delisted records may be removed; other synchronized records are protected.
// More than one matching record fails closed before mutation. Persistence uses
// a detached candidate: pre-replacement failures leave live state unchanged,
// while uncertain post-replacement failures reconcile live state from disk.
func Delete(id string) (DeleteOutcome, error) {
	accounts.LoadPool()
	accounts.PoolMu.Lock()
	defer accounts.PoolMu.Unlock()

	current := accounts.Pool
	match := -1
	for i, model := range current.Models {
		if model.ID != id {
			continue
		}
		if match >= 0 {
			return DeleteAmbiguous, nil
		}
		match = i
	}
	if match < 0 {
		return DeleteNotFound, nil
	}
	model := current.Models[match]
	if !model.Custom && !model.Delisted {
		return DeleteProtected, nil
	}

	nextValue := *current
	kept := make([]domain.Model, 0, len(current.Models)-1)
	kept = append(kept, current.Models[:match]...)
	kept = append(kept, current.Models[match+1:]...)
	nextValue.Models = kept
	if !model.Custom && (model.Source == SourceRemote || model.Source == SourceZen || model.Source == SourceSeed) {
		nextValue.ModelCatalogInitialized = true
	}
	if nextValue.DefaultModel == id {
		nextValue.DefaultModel = ""
	}
	if err := accounts.SavePoolCandidateStrictLocked(&nextValue); err != nil {
		return DeleteNotFound, err
	}
	return DeleteDeleted, nil
}

// UpdateContext sets the context and output limits of the first record with id.
// Zero for both returns the limits to unknown and releases the metadata lock, so
// a later sync may set them again. Any other value locks them against syncs.
// It reports whether a record was found.
func UpdateContext(id string, context, output int) bool {
	found := false
	Edit(func(p *domain.AccountPool) bool {
		for i := range p.Models {
			if p.Models[i].ID != id {
				continue
			}
			p.Models[i].Context = context
			p.Models[i].Output = output
			p.Models[i].MetaLocked = !(context == 0 && output == 0)
			found = true
			return true
		}
		return false
	})
	return found
}

// UpdateCapabilities updates the capability lists of the first record with id.
// Nil fields are left unchanged. It reports whether a record was found.
func UpdateCapabilities(id string, caps Capabilities) bool {
	found := false
	Edit(func(p *domain.AccountPool) bool {
		for i := range p.Models {
			if p.Models[i].ID != id {
				continue
			}
			if caps.ThinkingLevels != nil {
				p.Models[i].ThinkingLevels = caps.ThinkingLevels
			}
			if caps.InputModalities != nil {
				p.Models[i].InputModalities = caps.InputModalities
			}
			if caps.OutputModalities != nil {
				p.Models[i].OutputModalities = caps.OutputModalities
			}
			found = true
			return true
		}
		return false
	})
	return found
}

// SetDisabled sets the disabled flag on every record whose id is listed; the
// flag is ID-wide across sources. Disabling a record that is the default model
// clears the default. It returns the number of records changed.
func SetDisabled(ids []string, disabled bool) int {
	target := make(map[string]bool, len(ids))
	for _, id := range ids {
		target[id] = true
	}
	changed := 0
	Edit(func(p *domain.AccountPool) bool {
		for i := range p.Models {
			if !target[p.Models[i].ID] {
				continue
			}
			p.Models[i].Disabled = disabled
			changed++
			if disabled && p.DefaultModel == p.Models[i].ID {
				p.DefaultModel = ""
			}
		}
		return changed > 0
	})
	return changed
}
