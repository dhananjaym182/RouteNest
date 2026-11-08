package admin

import (
	"errors"
	"net/http"

	"routenest/internal/adminapi"
	"routenest/internal/domain"
	"routenest/internal/providers"
)

var adminModelRemoval = &adminapi.ModelRemoval{Remove: providers.RemoveDelistedModel}

var adminExplicitModelRemoval = &adminapi.ModelRemoval{
	Remove: providers.RemoveModel,
	Validate: func(refs []domain.ModelRemovalRef) error {
		providerID := refs[0].ProviderID
		if providerID == "" {
			return errors.New("explicit model removal requires a stable providerId")
		}
		for _, ref := range refs {
			if ref.ProviderID != providerID {
				return errors.New("explicit model removal must target one stable providerId per request")
			}
		}
		return nil
	},
}

func handleDelistedModelRemoval(w http.ResponseWriter, r *http.Request) {
	adminModelRemoval.ServeHTTP(w, r)
}

func handleModelRemoval(w http.ResponseWriter, r *http.Request) {
	adminExplicitModelRemoval.ServeHTTP(w, r)
}
