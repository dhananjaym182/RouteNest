package admin

import (
	"fmt"
	"net/http"
	"routenest/internal/routing"
	"routenest/internal/zen"

	"routenest/internal/adminapi"
	"routenest/internal/domain"
	"routenest/internal/i18n"
	"routenest/internal/models"
	"routenest/internal/providers"
)

// rootModelStore adapts the live model catalogue (package models) to
// adminapi.ModelStore. Locking and persistence live in the models package.
type rootModelStore struct{}

func (rootModelStore) List() []domain.Model {
	return routing.AllModels()
}

func (rootModelStore) Add(m domain.Model) {
	models.Add(m)
}

func (rootModelStore) Delete(id string) (adminapi.DeleteModelResult, error) {
	offerings, err := providers.SnapshotModelOfferings()
	if err != nil {
		return adminapi.ModelNotFound, fmt.Errorf("read model offerings: %w", err)
	}
	offeringMatches := 0
	for _, offering := range offerings {
		if offering.ModelID == id {
			offeringMatches++
		}
	}
	if offeringMatches > 1 {
		return adminapi.ModelAmbiguous, nil
	}
	effectiveMatches := 0
	for _, model := range routing.AllModels() {
		if model.ID == id {
			effectiveMatches++
		}
	}
	if offeringMatches == 0 && effectiveMatches > 1 {
		return adminapi.ModelAmbiguous, nil
	}

	outcome, err := models.Delete(id)
	if err != nil {
		return adminapi.ModelNotFound, err
	}
	switch outcome {
	case models.DeleteDeleted:
		return adminapi.ModelDeleted, nil
	case models.DeleteProtected:
		return adminapi.ModelBuiltin, nil
	case models.DeleteAmbiguous:
		return adminapi.ModelAmbiguous, nil
	default:
		if offeringMatches == 1 || effectiveMatches == 1 {
			return adminapi.ModelBuiltin, nil
		}
		return adminapi.ModelNotFound, nil
	}
}

func (rootModelStore) UpdateContext(id string, context, output int) bool {
	return models.UpdateContext(id, context, output)
}

func (rootModelStore) UpdateCapabilities(id string, caps adminapi.ModelCapabilities) bool {
	// nil means "leave unchanged"; the handler distinguishes it from an explicit
	// empty list, which clears the field.
	return models.UpdateCapabilities(id, models.Capabilities{
		ThinkingLevels:   caps.ThinkingLevels,
		InputModalities:  caps.InputModalities,
		OutputModalities: caps.OutputModalities,
	})
}

func (rootModelStore) SetDisabled(ids []string, disabled bool) int {
	return models.SetDisabled(ids, disabled)
}

// normalizeAdminModels applies the billing normalization the admin UI expects:
// zen free models always display as free, matching route classification.
func normalizeAdminModels(models []domain.Model) []domain.Model {
	for i := range models {
		if zen.IsSource(models[i]) && zen.IsFreeModel(models[i]) && models[i].Cost != "free" {
			models[i].Cost = "free"
		}
	}
	return models
}

var adminModels = &adminapi.Models{
	Store:     rootModelStore{},
	LastSync:  func() any { return routing.GetModelSyncResult() },
	Normalize: normalizeAdminModels,
	Translate: func(r *http.Request, key string) string { return i18n.TAPI(r, key) },
}
