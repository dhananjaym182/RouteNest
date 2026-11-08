package adminapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"routenest/internal/domain"
)

func TestModelRemovalBatchValidationRunsAfterNormalizationBeforeWrites(t *testing.T) {
	var validated, removed []domain.ModelRemovalRef
	handler := &ModelRemoval{
		Validate: func(refs []domain.ModelRemovalRef) error {
			validated = append([]domain.ModelRemovalRef(nil), refs...)
			return nil
		},
		Remove: func(ref domain.ModelRemovalRef) (bool, error) {
			if len(validated) != 2 {
				t.Fatal("mutation ran before whole-batch validation")
			}
			removed = append(removed, ref)
			return true, nil
		},
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/models/remove", strings.NewReader(`{"models":[{"id":" org/model@revision ","source":" cline ","providerId":" cline "},{"id":"second","source":"remote","providerId":"cline"}]}`)))
	want := []domain.ModelRemovalRef{{ID: "org/model@revision", Source: "remote", ProviderID: "cline"}, {ID: "second", Source: "remote", ProviderID: "cline"}}
	if rec.Code != http.StatusOK || !reflect.DeepEqual(validated, want) || !reflect.DeepEqual(removed, want) {
		t.Fatalf("batch normalization/order failed: status=%d validated=%+v removed=%+v", rec.Code, validated, removed)
	}
}

func TestModelRemovalBatchValidationRejectsBeforeAnyMutation(t *testing.T) {
	writes := 0
	handler := &ModelRemoval{
		Validate: func([]domain.ModelRemovalRef) error { return errors.New("batch scope rejected") },
		Remove:   func(domain.ModelRemovalRef) (bool, error) { writes++; return true, nil },
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/models/remove", strings.NewReader(`{"models":[{"id":"active","source":"remote","providerId":"cline"}]}`)))
	if rec.Code != http.StatusBadRequest || writes != 0 || !strings.Contains(rec.Body.String(), "batch scope rejected") {
		t.Fatalf("rejected batch caused writes: status=%d writes=%d body=%s", rec.Code, writes, rec.Body.String())
	}
}

func TestModelRemovalMalformedOrOversizedRequestsDoNotReachBatchValidation(t *testing.T) {
	validations, writes := 0, 0
	handler := &ModelRemoval{
		Validate: func([]domain.ModelRemovalRef) error { validations++; return nil },
		Remove:   func(domain.ModelRemovalRef) (bool, error) { writes++; return true, nil },
	}
	for _, body := range []string{
		`{"models":[{"id":"active","source":"remote","providerId":"cline"},{"id":"bad","source":"invalid"}]}`,
		`{"models":[{"id":"active","source":"remote","providerId":"cline","all":true}]}`,
		`{"models":[{"id":"active","source":"remote","providerId":"cline"}]} {}`,
		`{"models":[{"id":"` + strings.Repeat("a", 129<<10) + `","source":"remote","providerId":"cline"}]}`,
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/models/remove", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest || validations != 0 || writes != 0 {
			t.Fatalf("invalid bounded request reached validation/mutation: status=%d validations=%d writes=%d", rec.Code, validations, writes)
		}
	}
}
