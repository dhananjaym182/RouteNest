package domain

// ModelOffering is one Provider Instance's route to a logical model. It is a
// read model during migration; existing persistence remains domain.Model.
type ModelOffering struct {
	ProviderID        ProviderInstanceID `json:"providerId"`
	ModelID           string             `json:"modelId"`
	UpstreamModelID   string             `json:"upstreamModelId"`
	Access            AccessClass        `json:"access"`
	AllowanceVerified bool               `json:"allowanceVerified"`
	TierSignal        string             `json:"tierSignal,omitempty"`
	TierDecidedAt     int64              `json:"tierDecidedAt,omitempty"`
	ProviderEnabled   bool               `json:"providerEnabled"`
	Status            string             `json:"status,omitempty"`
	Disabled          bool               `json:"disabled,omitempty"`
	Delisted          bool               `json:"delisted,omitempty"`
	Manual            bool               `json:"manual,omitempty"`
	Custom            bool               `json:"custom,omitempty"`
	Context           int                `json:"context,omitempty"`
	Output            int                `json:"output,omitempty"`
	MetaLocked        bool               `json:"metaLocked,omitempty"`
	ThinkingLevels    []string           `json:"thinkingLevels,omitempty"`
	InputModalities   []string           `json:"inputModalities,omitempty"`
	OutputModalities  []string           `json:"outputModalities,omitempty"`
}

func (offering ModelOffering) Key() OfferingKey {
	return OfferingKey{ProviderID: offering.ProviderID, ModelID: offering.ModelID}
}

func (offering ModelOffering) FreeOnlyEligible() bool {
	return offering.Access.Valid() && offering.Access.EligibleForFreeOnly(offering.AllowanceVerified)
}
