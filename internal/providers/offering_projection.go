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
	claims := make(map[domain.ProviderInstanceID][]string)
	for _, provider := range List() {
		claims[domain.ProviderInstanceID(provider.ID)] = append([]string(nil), provider.ModelIDs...)
	}
	sources := make([]models.OfferingSource, 0, len(instances))
	for _, instance := range instances {
		sources = append(sources, models.OfferingSource{
			Instance: instance,
			ModelIDs: claims[instance.ID],
		})
	}
	return models.ProjectOfferings(models.Snapshot(), sources), nil
}
