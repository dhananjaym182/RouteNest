package models

import (
	"errors"

	"routenest/internal/domain"
)

// ErrEmptySource is returned by ReplaceSource when the build function produces
// no records. Nothing is written, so an empty upstream list never clears a
// catalogue.
var ErrEmptySource = errors.New("source returned an empty list")

// SourceUpdate reports what ReplaceSource changed.
type SourceUpdate struct {
	// Added lists ids present in the new list but not previously in the source.
	Added []string
	// Removed lists ids that left the source and were not already delisted.
	Removed []string
	// Total is the number of records in the new list.
	Total int
}

// ReplaceSource rebuilds the records of one synchronized source under the pool
// lock. build receives copies of the source's current records and returns the
// desired list; it must be pure, because it runs while the lock is held.
//
// Records of other sources are kept as they are. Records of this source that
// are missing from the desired list are kept and marked delisted: upstream
// removal does not delete a model, because models often keep working. Marks
// clear when the model returns, because the rebuilt record is unmarked.
func ReplaceSource(source string, build func(current []domain.Model) []domain.Model) (SourceUpdate, error) {
	var update SourceUpdate
	var err error
	Edit(func(p *domain.AccountPool) bool {
		var current []domain.Model
		for _, m := range p.Models {
			if m.Source == source {
				current = append(current, cloneModel(m))
			}
		}
		desired := build(current)
		if len(desired) == 0 {
			err = ErrEmptySource
			return false
		}

		oldIDs := make(map[string]bool, len(current))
		for _, m := range current {
			oldIDs[m.ID] = true
		}
		seen := make(map[string]bool, len(desired))
		for _, m := range desired {
			seen[m.ID] = true
		}

		kept := make([]domain.Model, 0, len(p.Models)+len(desired))
		for _, m := range p.Models {
			if m.Source != source {
				kept = append(kept, m)
			}
		}
		for _, m := range current {
			if seen[m.ID] {
				continue
			}
			if !m.Delisted {
				update.Removed = append(update.Removed, m.ID)
			}
			m.Delisted = true
			kept = append(kept, m)
		}
		kept = append(kept, desired...)
		for _, m := range desired {
			if !oldIDs[m.ID] {
				update.Added = append(update.Added, m.ID)
			}
		}
		update.Total = len(desired)
		p.Models = kept
		return true
	})
	return update, err
}

// RemoveDelisted removes the first record with id that is delisted and not
// custom. Custom and live records are never removed here. Removing the default
// model clears the default. It reports whether a record was removed.
func RemoveDelisted(id string) bool {
	removed := false
	Edit(func(p *domain.AccountPool) bool {
		for i, m := range p.Models {
			if m.ID != id || !m.Delisted || m.Custom {
				continue
			}
			p.Models = append(p.Models[:i], p.Models[i+1:]...)
			if p.DefaultModel == id {
				p.DefaultModel = ""
			}
			removed = true
			return true
		}
		return false
	})
	return removed
}
