package routing

import (
	"fmt"
	"routenest/internal/models"
	"routenest/internal/providers"
	"routenest/internal/zen"
	"strings"

	"routenest/internal/dispatch"
	"routenest/internal/domain"
)

// ModelRouteAccess resolves the access class of a model's current route from
// catalog metadata. Unknown models are unknown: they never silently qualify for
// Free-only routing.
func ModelRouteAccess(id string) dispatch.RouteAccess {
	id = strings.TrimSpace(id)
	if id == "" {
		return dispatch.RouteAccess{Class: domain.AccessUnknown}
	}
	modelID, pin, err := ResolveModelReference(id)
	if err != nil {
		return dispatch.RouteAccess{Class: domain.AccessUnknown}
	}
	if pin.active() {
		if pin.ProviderID == "" {
			return ModelRouteAccess(modelID)
		}
		offerings, err := ModelOfferings()
		if err == nil {
			for _, offering := range offerings {
				if offering.ModelID == modelID && offering.ProviderID == pin.ProviderID {
					return dispatch.RouteAccessOf(offering)
				}
			}
		}
		return dispatch.RouteAccess{Class: domain.AccessUnknown}
	}
	bare := strings.TrimPrefix(id, "opencode/")
	access := dispatch.RouteAccess{Class: domain.AccessUnknown}
	if m, ok := models.Find(func(m domain.Model) bool { return m.ID == bare }); ok {
		access = dispatch.RouteAccess{Class: dispatch.ModelAccessClass(m.Cost, m.TierSignal, m.Source)}
	} else if zm, ok := zen.ResolveInfo(id); ok {
		access = dispatch.RouteAccess{Class: dispatch.ModelAccessClass(zm.Cost, zm.TierSignal, zm.Source)}
	}
	// The shared catalog entry for a bare name knows nothing about providers. A
	// provider the administrator set to allow its models is that administrator's
	// explicit choice, so it applies to the bare name too.
	if !dispatch.FreeOnlyEligible(access) && !strings.HasPrefix(id, "opencode/") {
		if granted, ok := providerGrantedAccess(bare); ok {
			return granted
		}
	}
	return access
}

// providerGrantedAccess finds a usable route for modelID on a provider whose
// Free-only setting allows its models. A model whose tier the administrator set
// by hand never qualifies this way, because its offering decides by that tier.
func providerGrantedAccess(modelID string) (dispatch.RouteAccess, bool) {
	if !providers.AnyAccessPolicyGrant() {
		return dispatch.RouteAccess{}, false
	}
	offerings, err := ModelOfferings()
	if err != nil {
		return dispatch.RouteAccess{}, false
	}
	for _, offering := range offerings {
		if offering.ModelID != modelID || !offering.ProviderEnabled || offering.Disabled || offering.Delisted ||
			(offering.Status != "" && offering.Status != "active") {
			continue
		}
		if access := dispatch.RouteAccessOf(offering); access.Basis.GrantsFreeOnly() && offering.FreeOnlyEligible() {
			return access, true
		}
	}
	return dispatch.RouteAccess{}, false
}

// FreeOnlyRefusalDetail says why Free-only routing refuses a model and what
// would allow it. It names the access class, then either the provider setting
// and the model tier (the two choices the administrator has), or, when the
// administrator already set this model's tier by hand, that the provider
// setting cannot override it. Built-in routes have no such setting.
func FreeOnlyRefusalDetail(id string) string {
	access := ModelRouteAccess(id)
	detail := "access class: " + string(access.Class)
	switch {
	case access.Basis == domain.BasisManual:
		detail += "; you set this model's tier by hand, so the provider's setting does not apply"
	case access.ProviderID != "" && access.ProviderID != "cline" && access.ProviderID != "opencode":
		detail += "; to allow it, change the provider's Free-only setting or set the model's tier to free"
	}
	return detail
}

// ModelFreeOnlyEligible reports whether a model may be used while Free-only
// routing is enabled. The "free" alias resolves to the explicit free chain.
func ModelFreeOnlyEligible(id string) bool {
	if strings.TrimSpace(id) == "free" {
		return true
	}
	return dispatch.FreeOnlyEligible(ModelRouteAccess(id))
}

// FreeOnlyRejection returns a user-facing error when the requested model may
// not be served under Free-only routing. Named combos are validated per entry
// during execution instead.
func FreeOnlyRejection(model string) string {
	if !liveConfig().OnlyFree || model == "" {
		return ""
	}
	if _, ok := comboCatalog.FindCombo(model); ok {
		return ""
	}
	if ModelFreeOnlyEligible(model) {
		return ""
	}
	return fmt.Sprintf("model %q is not eligible while onlyFree is enabled (%s)", model, FreeOnlyRefusalDetail(model))
}
