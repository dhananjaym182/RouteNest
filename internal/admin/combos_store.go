package admin

import (
	"net/http"

	"cline-go-proxy/internal/adminapi"
	"cline-go-proxy/internal/combos"
	"cline-go-proxy/internal/domain"
	"cline-go-proxy/internal/i18n"
)

// rootComboStore adapts the combo registry to adminapi.ComboStore.
type rootComboStore struct{}

func (rootComboStore) List() []domain.ModelCombo { return combos.ListCombos() }

func (rootComboStore) Catalog() []map[string]any { return combos.Catalog() }

func (rootComboStore) Find(id string) (domain.ModelCombo, bool) { return combos.FindCombo(id) }

func (rootComboStore) Upsert(c domain.ModelCombo) (domain.ModelCombo, error) {
	return combos.UpsertCombo(c)
}

func (rootComboStore) Delete(id string) bool { return combos.DeleteCombo(id) }

func (rootComboStore) SetEnabled(id string, enabled bool) (domain.ModelCombo, error) {
	return combos.SetComboEnabled(id, enabled)
}

func (rootComboStore) Clone(id string) (domain.ModelCombo, error) { return combos.CloneCombo(id) }

var adminCombos = &adminapi.Combos{
	Store:         rootComboStore{},
	LocalizeError: combos.LocalizeComboError,
	Translate:     func(r *http.Request, key string) string { return i18n.TAPI(r, key) },
}
