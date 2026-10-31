package admin

// HTTP adapters for request-log clearing and the version check. Behaviour lives
// in requestlog and buildinfo; these functions parse, check the method and build
// the envelope.

import (
	"net/http"

	"routenest/internal/buildinfo"
	"routenest/internal/httpx"
	"routenest/internal/i18n"
	"routenest/internal/requestlog"
)

// POST /admin/api/request-logs/clear
func handleRequestLogsClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteAPI(w, http.StatusMethodNotAllowed, httpx.Response{Error: i18n.TAPI(r, "method_not_allowed")})
		return
	}
	requestlog.Clear()
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true})
}

// GET /admin/api/version-check?force=1
func handleAdminVersionCheck(w http.ResponseWriter, r *http.Request) {
	st := buildinfo.CurrentVersionStatus(r.URL.Query().Get("force") == "1")
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: versionCheckData(st)})
}

// versionCheckData maps a version status to the admin envelope. The error key is
// present only when the check failed.
func versionCheckData(st buildinfo.VersionStatus) map[string]any {
	data := map[string]any{
		"current":         st.Current,
		"latest":          st.Latest,
		"releaseURL":      st.ReleaseURL,
		"publishedAt":     st.PublishedAt,
		"checkedAt":       st.CheckedAtMillis,
		"updateAvailable": st.UpdateAvailable,
	}
	if st.Error != "" {
		data["error"] = st.Error
	}
	return data
}
