package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"routenest/internal/domain"
)

// The editor and the enable/disable toggle both depend on the provider API:
// the editor shows whatever the list returns, and the toggle posts the whole
// record back. If the list dropped the Free-only setting, the next toggle would
// silently reset the administrator's choice to auto-detect.
func TestProviderAPIRoundTripsTheFreeOnlySetting(t *testing.T) {
	installOfferingNameViewFixture(t, nil, nil, map[string]string{})

	save := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handleProviderSave(rec, httptest.NewRequest(http.MethodPost, "/admin/api/providers/save", strings.NewReader(body)))
		return rec
	}
	list := func() []domain.CustomProvider {
		rec := httptest.NewRecorder()
		handleProvidersList(rec, httptest.NewRequest(http.MethodGet, "/admin/api/providers", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("list status = %d: %s", rec.Code, rec.Body.String())
		}
		var out []domain.CustomProvider
		if err := json.Unmarshal(decodeProviderSystemData(t, rec)["providers"], &out); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		return out
	}

	// Saved with a choice: the list returns it.
	if rec := save(`{"name":"Mixed","baseURL":"https://mixed.example.com/v1","enabled":true,"free":true,"accessPolicy":"allow_all"}`); rec.Code != http.StatusOK {
		t.Fatalf("save status = %d: %s", rec.Code, rec.Body.String())
	}
	providersNow := list()
	if len(providersNow) != 1 || providersNow[0].AccessPolicy != domain.AccessPolicyAllowAll {
		t.Fatalf("list after save = %+v", providersNow)
	}

	// The toggle posts the list's own record back with one field changed.
	toggled := providersNow[0]
	toggled.Enabled = false
	body, err := json.Marshal(toggled)
	if err != nil {
		t.Fatal(err)
	}
	if rec := save(string(body)); rec.Code != http.StatusOK {
		t.Fatalf("toggle status = %d: %s", rec.Code, rec.Body.String())
	}
	if after := list(); len(after) != 1 || after[0].AccessPolicy != domain.AccessPolicyAllowAll || after[0].Enabled {
		t.Fatalf("after toggle = %+v, want allow_all kept and enabled=false", after)
	}

	// A body without the field (an older client) must not grant anything.
	if rec := save(`{"name":"Plain","baseURL":"https://plain.example.com/v1","enabled":true,"free":true}`); rec.Code != http.StatusOK {
		t.Fatalf("plain save status = %d: %s", rec.Code, rec.Body.String())
	}
	for _, p := range list() {
		if p.Name == "Plain" && (p.AccessPolicy != "" || !p.Free) {
			t.Fatalf("a provider saved without the field = %+v, want the default policy (the free flag is only a hint)", p)
		}
	}
}

func TestProviderAPIRejectsAnUnknownFreeOnlySetting(t *testing.T) {
	installOfferingNameViewFixture(t, nil, nil, map[string]string{})
	for _, bad := range []string{"both", "free", "ALLOW-ALL", "true", "1"} {
		rec := httptest.NewRecorder()
		handleProviderSave(rec, httptest.NewRequest(http.MethodPost, "/admin/api/providers/save",
			strings.NewReader(`{"name":"Bad","baseURL":"https://bad.example.com/v1","accessPolicy":"`+bad+`"}`)))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "accessPolicy") {
			t.Errorf("accessPolicy %q: status %d body %s, want 400 naming accessPolicy", bad, rec.Code, rec.Body.String())
		}
	}
}
