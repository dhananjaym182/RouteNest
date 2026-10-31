package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"routenest/internal/buildinfo"
	"routenest/internal/requestlog"
)

// Method rejection and the cleared state for the request-log clear endpoint.
func TestRequestLogsClearAdapter(t *testing.T) {
	requestlog.Mu.Lock()
	oldLogs := requestlog.Logs
	requestlog.Logs = nil
	requestlog.Mu.Unlock()
	t.Cleanup(func() {
		requestlog.Mu.Lock()
		requestlog.Logs = oldLogs
		requestlog.Mu.Unlock()
	})

	rec := httptest.NewRecorder()
	handleRequestLogsClear(rec, httptest.NewRequest(http.MethodGet, "/admin/api/request-logs/clear", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", rec.Code)
	}
	rec = httptest.NewRecorder()
	handleRequestLogsClear(rec, httptest.NewRequest(http.MethodPost, "/admin/api/request-logs/clear", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200", rec.Code)
	}
}

// The version envelope keeps its keys; error appears only when the check failed.
func TestVersionCheckEnvelope(t *testing.T) {
	ok := versionCheckData(buildinfo.VersionStatus{Current: "v1", Latest: "v2", ReleaseURL: "u", PublishedAt: "p", CheckedAtMillis: 7, UpdateAvailable: true})
	for _, k := range []string{"current", "latest", "releaseURL", "publishedAt", "checkedAt", "updateAvailable"} {
		if _, found := ok[k]; !found {
			t.Errorf("envelope missing %q", k)
		}
	}
	if _, found := ok["error"]; found {
		t.Error("error key must be absent when the check succeeded")
	}
	failed := versionCheckData(buildinfo.VersionStatus{Error: "unreachable"})
	if failed["error"] != "unreachable" {
		t.Errorf("error = %v", failed["error"])
	}
}
