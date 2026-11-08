package admin

// HTTP adapters for the additive global provider-system views. These endpoints
// project provider instances, credentials, model offerings, runtime
// observations, and egress profiles without replacing the legacy account,
// connection, provider, Zen, proxy, or model endpoints.

import (
	"net/http"

	"routenest/internal/config"
	"routenest/internal/credentials"
	"routenest/internal/domain"
	"routenest/internal/egress"
	"routenest/internal/httpx"
	"routenest/internal/i18n"
	"routenest/internal/providers"
	"routenest/internal/providerstate"
)

func requireGET(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet {
		return true
	}
	httpx.WriteAPI(w, http.StatusMethodNotAllowed, httpx.Response{Error: i18n.TAPI(r, "method_not_allowed")})
	return false
}

// GET /admin/api/provider-instances
func handleProviderInstances(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	instances, err := providers.SnapshotInstances()
	if err != nil {
		httpx.WriteAPI(w, http.StatusInternalServerError, httpx.Response{Error: err.Error()})
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{
		"instances": instances,
	}})
}

// GET /admin/api/credentials
func handleProviderCredentials(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	if !credentials.HasDefault() {
		httpx.WriteAPI(w, http.StatusServiceUnavailable, httpx.Response{Error: "credentials registry is unavailable"})
		return
	}
	snapshot, err := credentials.Default().Snapshot()
	if err != nil {
		httpx.WriteAPI(w, http.StatusInternalServerError, httpx.Response{Error: err.Error()})
		return
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{
		"credentials": snapshot,
	}})
}

// Display aliases are added at the API boundary; the offering's stored and
// upstream model identities remain unchanged.
type modelOfferingView struct {
	domain.ModelOffering
	QualifiedID  string `json:"qualifiedId,omitempty"`
	ProviderName string `json:"providerName,omitempty"`
}

// GET /admin/api/model-offerings
func handleModelOfferings(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	offerings, err := providers.SnapshotModelOfferings()
	if err != nil {
		httpx.WriteAPI(w, http.StatusInternalServerError, httpx.Response{Error: err.Error()})
		return
	}
	instances, err := providers.SnapshotInstances()
	if err != nil {
		httpx.WriteAPI(w, http.StatusInternalServerError, httpx.Response{Error: err.Error()})
		return
	}
	names := make(map[domain.ProviderInstanceID]string, len(instances))
	for _, instance := range instances {
		names[instance.ID] = instance.Name
	}
	cfg := config.Live()
	views := make([]modelOfferingView, len(offerings))
	for i, offering := range offerings {
		views[i] = modelOfferingView{ModelOffering: offering, ProviderName: names[offering.ProviderID]}
		if prefix := cfg.ProviderModelPrefix(string(offering.ProviderID)); prefix != "" {
			views[i].QualifiedID = prefix + "/" + offering.ModelID
		}
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{
		"offerings": views,
	}})
}

// GET /admin/api/provider-state
//
// Until is the eligibility cooldown and ResetAt is a known quota reset; both
// are reported separately so a fallback cooldown is never presented as an exact
// upstream reset. Source and Confidence identify the evidence.
func handleProviderState(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	var observations any = []any{}
	if providerstate.HasDefault() {
		observations = providerstate.Default().Snapshot("")
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{
		"observations": observations,
		"runtimeState": providers.RuntimeStateHealth(),
	}})
}

// GET /admin/api/egress-profiles
func handleEgressProfiles(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	profiles := []egress.Profile{}
	globalID := ""
	if egress.HasDefault() {
		profiles = egress.Default().Profiles()
		globalID = egress.Default().GlobalID()
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{
		"profiles":        profiles,
		"globalProfileId": globalID,
	}})
}
