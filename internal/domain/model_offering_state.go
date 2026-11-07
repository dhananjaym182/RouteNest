package domain

// ModelOfferingState is the persisted provider-scoped overlay for one logical
// model. Empty optional fields inherit from the legacy domain.Model projection;
// pointer fields distinguish an explicit zero/false/empty value from inheritance.
//
// Cost is retained during the compatibility migration because the import and
// admin contracts use "free" / "pass". Access is the routing-facing equivalent.
type ModelOfferingState struct {
	UpstreamModelID   string      `json:"upstreamModelId,omitempty"`
	Cost              string      `json:"cost,omitempty"`
	Access            AccessClass `json:"access,omitempty"`
	AllowanceVerified *bool       `json:"allowanceVerified,omitempty"`
	TierSignal        string      `json:"tierSignal,omitempty"`
	TierDecidedAt     int64       `json:"tierDecidedAt,omitempty"`
	Status            *string     `json:"status,omitempty"`
	Disabled          *bool       `json:"disabled,omitempty"`
	Delisted          *bool       `json:"delisted,omitempty"`
	Manual            *bool       `json:"manual,omitempty"`
	Custom            *bool       `json:"custom,omitempty"`
	Context           *int        `json:"context,omitempty"`
	Output            *int        `json:"output,omitempty"`
	MetaLocked        *bool       `json:"metaLocked,omitempty"`
	ThinkingLevels    *[]string   `json:"thinkingLevels,omitempty"`
	InputModalities   *[]string   `json:"inputModalities,omitempty"`
	OutputModalities  *[]string   `json:"outputModalities,omitempty"`
}

// Valid reports whether the overlay contains values RouteNest can safely use.
// Empty fields are valid because they mean "inherit the compatibility model".
func (state ModelOfferingState) Valid() bool {
	if state.Cost != "" && state.Cost != "free" && state.Cost != "pass" {
		return false
	}
	if state.Access != "" && !state.Access.Valid() {
		return false
	}
	if state.Context != nil && *state.Context < 0 {
		return false
	}
	if state.Output != nil && *state.Output < 0 {
		return false
	}
	return true
}

// Clone returns a deep copy suitable for snapshots and persistence boundaries.
func (state ModelOfferingState) Clone() ModelOfferingState {
	cloneBool := func(in *bool) *bool {
		if in == nil {
			return nil
		}
		out := *in
		return &out
	}
	cloneInt := func(in *int) *int {
		if in == nil {
			return nil
		}
		out := *in
		return &out
	}
	cloneString := func(in *string) *string {
		if in == nil {
			return nil
		}
		out := *in
		return &out
	}
	cloneStrings := func(in *[]string) *[]string {
		if in == nil {
			return nil
		}
		// Preserve an explicit empty slice. Pointer fields use nil to mean
		// "inherit", while a non-nil empty slice means "override with none".
		out := append([]string{}, (*in)...)
		return &out
	}
	state.AllowanceVerified = cloneBool(state.AllowanceVerified)
	state.Status = cloneString(state.Status)
	state.Disabled = cloneBool(state.Disabled)
	state.Delisted = cloneBool(state.Delisted)
	state.Manual = cloneBool(state.Manual)
	state.Custom = cloneBool(state.Custom)
	state.Context = cloneInt(state.Context)
	state.Output = cloneInt(state.Output)
	state.MetaLocked = cloneBool(state.MetaLocked)
	state.ThinkingLevels = cloneStrings(state.ThinkingLevels)
	state.InputModalities = cloneStrings(state.InputModalities)
	state.OutputModalities = cloneStrings(state.OutputModalities)
	return state
}
