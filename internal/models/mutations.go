package models

import "routenest/internal/domain"

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

// Delete removes the first record with id. Custom and delisted records may be
// removed; other synchronized records are protected. Removing the record that
// is the default model clears the default.
func Delete(id string) DeleteOutcome {
	outcome := DeleteNotFound
	Edit(func(p *domain.AccountPool) bool {
		for i, m := range p.Models {
			if m.ID != id {
				continue
			}
			if !m.Custom && !m.Delisted {
				outcome = DeleteProtected
				return false
			}
			p.Models = append(p.Models[:i], p.Models[i+1:]...)
			if !m.Custom && (m.Source == SourceRemote || m.Source == SourceZen || m.Source == SourceSeed) {
				p.ModelCatalogInitialized = true
			}
			if p.DefaultModel == id {
				p.DefaultModel = ""
			}
			outcome = DeleteDeleted
			return true
		}
		return false
	})
	return outcome
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
