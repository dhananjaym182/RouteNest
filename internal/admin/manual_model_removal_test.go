package admin

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"routenest/internal/adminauth"
	"routenest/internal/domain"
)

func TestExplicitModelRemovalRequiresProviderPermissionAndCSRF(t *testing.T) {
	registry := newAdminRouteRegistry()
	registerModelsRoutes(registry)
	policy, exists := registry.Policy(http.MethodPost, "/admin/api/models/remove")
	if !exists || policy.CSRF != csrfSession || !reflect.DeepEqual(policy.Permissions, []adminauth.Permission{adminauth.PermissionProviderWrite}) {
		t.Fatalf("explicit model removal security policy = %+v, registered=%v", policy, exists)
	}
}

func TestExplicitModelRemovalValidatesOneStableProviderBeforeWrites(t *testing.T) {
	writes := 0
	before := adminExplicitModelRemoval.Remove
	adminExplicitModelRemoval.Remove = func(domain.ModelRemovalRef) (bool, error) { writes++; return true, nil }
	t.Cleanup(func() { adminExplicitModelRemoval.Remove = before })
	for _, body := range []string{
		`{"models":[{"id":"m","source":"remote"}]}`,
		`{"models":[{"id":"m","source":"zen"}]}`,
		`{"models":[{"id":"m","source":"provider","provider":"AntSeed"}]}`,
		`{"models":[{"id":"first","source":"provider","providerId":"alpha"},{"id":"second","source":"provider","providerId":"beta"}]}`,
		`{"models":[{"id":"first","source":"remote","providerId":"cline"},{"id":"second","source":"zen","providerId":"opencode"}]}`,
		`{"models":[{"id":"first","source":"provider","providerId":"alpha"},{"id":"second","source":"provider","provider":"Alpha"}]}`,
	} {
		rec := httptest.NewRecorder()
		handleModelRemoval(rec, httptest.NewRequest(http.MethodPost, "/admin/api/models/remove", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest || writes != 0 {
			t.Fatalf("invalid scope caused writes: status=%d writes=%d body=%s", rec.Code, writes, rec.Body.String())
		}
	}
}

func TestExplicitModelRemovalPreservesCanonicalProviderAndLiteralIDs(t *testing.T) {
	var got []domain.ModelRemovalRef
	before := adminExplicitModelRemoval.Remove
	adminExplicitModelRemoval.Remove = func(ref domain.ModelRemovalRef) (bool, error) { got = append(got, ref); return true, nil }
	t.Cleanup(func() { adminExplicitModelRemoval.Remove = before })
	for _, target := range []struct{ source, providerID string }{{"cline", "cline"}, {"zen", "opencode"}, {"provider", "antseed-instance"}} {
		got = nil
		body := `{"models":[{"id":"org/model@revision","source":"` + target.source + `","providerId":"` + target.providerID + `"},{"id":"model@second","source":"` + target.source + `","providerId":"` + target.providerID + `"}]}`
		rec := httptest.NewRecorder()
		handleModelRemoval(rec, httptest.NewRequest(http.MethodPost, "/admin/api/models/remove", strings.NewReader(body)))
		source := target.source
		if source == "cline" {
			source = "remote"
		}
		want := []domain.ModelRemovalRef{{ID: "org/model@revision", Source: source, ProviderID: target.providerID}, {ID: "model@second", Source: source, ProviderID: target.providerID}}
		if rec.Code != http.StatusOK || !reflect.DeepEqual(got, want) {
			t.Fatalf("canonical batch identity changed: status=%d got=%+v want=%+v", rec.Code, got, want)
		}
	}
}
