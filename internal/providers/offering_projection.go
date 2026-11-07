package providers

import (
	"routenest/internal/domain"
	"routenest/internal/models"
)

// SnapshotModelOfferings expands the legacy ID-wide model list into a
// provider-scoped read model. Persistence remains unchanged during migration.
func SnapshotModelOfferings() ([]domain.ModelOffering, error) {
	instances, err := SnapshotInstances()
	if err != nil {
		return nil, err
	}
	records := models.Snapshot()
	claims := make(map[domain.ProviderInstanceID][]string)
	states := make(map[domain.ProviderInstanceID]map[string]domain.ModelOfferingState)
	for _, provider := range List() {
		providerID := domain.ProviderInstanceID(provider.ID)
		claims[providerID] = append([]string(nil), provider.ModelIDs...)
		states[providerID] = providerOfferingStates(provider, records)
	}
	sources := make([]models.OfferingSource, 0, len(instances))
	for _, instance := range instances {
		sources = append(sources, models.OfferingSource{
			Instance: instance,
			ModelIDs: claims[instance.ID],
			States:   states[instance.ID],
		})
	}
	return models.ProjectOfferings(records, sources), nil
}
