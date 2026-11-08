package adminapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"routenest/internal/domain"
)

func TestModelRemovalValidatesWholeRequestBeforeMutating(t *testing.T) {
	mutations := 0
	handler := &ModelRemoval{Remove: func(domain.ModelRemovalRef) (bool, error) { mutations++; return true, nil }}
	bodies := []string{
		`{"models":[{"id":"old","source":"remote"},{"id":"live","source":"unknown"}]}`,
		`{"models":[{"id":"old","source":"provider","providerId":"opencode"}]}`,
		`{"models":[{"id":"old","source":"remote","unused":true}]}`,
		`{"models":[{"id":"old","source":"remote"}]} []`,
		`{"models":[{"id":"` + strings.Repeat("x", 129<<10) + `","source":"remote"}]}`,
	}
	for _, body := range bodies {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/models/delisted/remove", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest || mutations != 0 {
			t.Fatalf("invalid request caused a mutation: status=%d count=%d", rec.Code, mutations)
		}
	}
}

func TestModelRemovalMixedResultsPreserveRequestedIdentities(t *testing.T) {
	handler := &ModelRemoval{Remove: func(ref domain.ModelRemovalRef) (bool, error) {
		if ref.ID == "failed" {
			return false, errors.New("save failed")
		}
		return ref.ID == "retired", nil
	}}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/models/delisted/remove", strings.NewReader(`{"models":[{"id":"retired","source":"provider","providerId":"alpha"},{"id":"live","source":"zen"},{"id":"failed","source":"remote"}]}`)))
	var envelope struct {
		Success bool                      `json:"success"`
		Data    domain.ModelRemovalResult `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil || rec.Code != http.StatusOK || !envelope.Success || len(envelope.Data.Removed) != 1 || len(envelope.Data.Skipped) != 1 || len(envelope.Data.Errors) != 1 {
		t.Fatalf("mixed cleanup = %d %s, %v", rec.Code, rec.Body.String(), err)
	}
	if envelope.Data.Removed[0].ProviderID != "alpha" || envelope.Data.Errors[0].Model.ID != "failed" || envelope.Data.Errors[0].Error != "save failed" {
		t.Fatalf("result identities = %+v", envelope.Data)
	}
}
