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
	// ProviderPolicy is the administrator's Free-only setting for the provider
	// that owns this route. It is read when the route is checked and is not
	// stored per model, so changing it needs no re-sync. Empty means detect.
	ProviderPolicy ProviderAccessPolicy `json:"providerAccessPolicy,omitempty"`
}

func (offering ModelOffering) Key() OfferingKey {
	return OfferingKey{ProviderID: offering.ProviderID, ModelID: offering.ModelID}
}

// FreeOnlyDecision applies the administrator's choices to this route: a tier
// set by hand on the model, then the provider's Free-only setting, then the
// model's detected access class. Only TierSignal "manual" counts as a hand-set
// tier; the Manual and Custom flags describe how the model entered the catalog.
func (offering ModelOffering) FreeOnlyDecision() FreeOnlyDecision {
	return DecideFreeOnly(offering.Access, offering.AllowanceVerified, offering.TierSignal == "manual", offering.ProviderPolicy)
}

func (offering ModelOffering) FreeOnlyEligible() bool {
	return offering.FreeOnlyDecision().Eligible
}
