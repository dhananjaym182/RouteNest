package combos

import "routenest/internal/domain"

// RoutingCombos exposes the combo store to routing. The composition root installs it
// with routing.SetComboCatalog.
type RoutingCombos struct{}

// FindCombo returns the combo with the given ID, if one exists.
func (RoutingCombos) FindCombo(id string) (domain.ModelCombo, bool) { return FindCombo(id) }

// EnabledCombos returns the enabled combos in listing order.
func (RoutingCombos) EnabledCombos() []domain.ModelCombo { return EnabledList() }
