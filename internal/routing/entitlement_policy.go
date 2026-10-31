package routing

import (
	"fmt"
	"routenest/internal/models"
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
	bare := strings.TrimPrefix(id, "opencode/")
	if m, ok := models.Find(func(m domain.Model) bool { return m.ID == bare }); ok {
		return dispatch.RouteAccess{Class: dispatch.ModelAccessClass(m.Cost, m.TierSignal, m.Source)}
	}
	if zm, ok := zen.ResolveInfo(id); ok {
		return dispatch.RouteAccess{Class: dispatch.ModelAccessClass(zm.Cost, zm.TierSignal, zm.Source)}
	}
	return dispatch.RouteAccess{Class: domain.AccessUnknown}
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
	if !ConfigProvider().OnlyFree || model == "" {
		return ""
	}
	if _, ok := FindCombo(model); ok {
		return ""
	}
	if ModelFreeOnlyEligible(model) {
		return ""
	}
	class := ModelRouteAccess(model).Class
	return fmt.Sprintf("model %q is not eligible while onlyFree is enabled (access class: %s)", model, class)
}
