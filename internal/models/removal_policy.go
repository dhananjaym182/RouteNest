package models

import "routenest/internal/domain"

// OfferingRemovalReserved reads an already-locked pool. Only builtin providers
// need reservations; custom providers own their persisted model declarations.
func OfferingRemovalReserved(pool *domain.AccountPool, providerID, modelID string) bool {
	if pool == nil {
		return false
	}
	for _, ref := range pool.RemovedModelOfferings {
		if ref.ProviderID == providerID && ref.ID == modelID {
			return true
		}
	}
	return false
}

// ReserveOfferingRemoval changes only the supplied pool. The caller must hold
// its lock and persist/roll back this reservation with the catalog mutation.
func ReserveOfferingRemoval(pool *domain.AccountPool, input domain.ModelRemovalRef) bool {
	// Reconciliation may also carry a display name for pruning legacy config
	// references; reservations contain only the canonical builtin identity.
	if input.Source == SourceRemote || input.Source == SourceZen {
		input.Provider = ""
	}
	ref, err := input.Normalize()
	if pool == nil || err != nil || ref.Source != SourceRemote && ref.Source != SourceZen {
		return false
	}
	if ref.Source == SourceRemote {
		ref.ProviderID = "cline"
	} else {
		ref.ProviderID = "opencode"
	}
	if OfferingRemovalReserved(pool, ref.ProviderID, ref.ID) {
		return false
	}
	next := append([]domain.ModelRemovalRef(nil), pool.RemovedModelOfferings...)
	pool.RemovedModelOfferings = append(next, ref)
	return true
}

// FilterRemovedModelRecords keeps source identity while projecting synced or
// offline records. Equal IDs belonging to another provider remain untouched.
func FilterRemovedModelRecords(pool *domain.AccountPool, records []domain.Model) []domain.Model {
	if pool == nil || len(pool.RemovedModelOfferings) == 0 {
		return records
	}
	kept := make([]domain.Model, 0, len(records))
	for _, model := range records {
		providerID := "cline"
		switch model.Source {
		case SourceZen, SourceSeed:
			providerID = "opencode"
		case SourceProvider:
			providerID = ""
		}
		if providerID == "" || !OfferingRemovalReserved(pool, providerID, model.ID) {
			kept = append(kept, model)
		}
	}
	return kept
}

// OfferingRemovalReservations snapshots policy for request-local projections
// without holding the pool lock while provider/credential stores are read.
func OfferingRemovalReservations() map[domain.OfferingKey]bool {
	reserved := make(map[domain.OfferingKey]bool)
	View(func(pool *domain.AccountPool) {
		for _, ref := range pool.RemovedModelOfferings {
			reserved[domain.OfferingKey{ProviderID: domain.ProviderInstanceID(ref.ProviderID), ModelID: ref.ID}] = true
		}
	})
	return reserved
}
