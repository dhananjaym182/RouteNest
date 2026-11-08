package adminapi

import (
	"encoding/json"
	"io"
	"net/http"

	"routenest/internal/domain"
	"routenest/internal/httpx"
)

type ModelRemoval struct {
	Remove func(domain.ModelRemovalRef) (bool, error)
}

// ServeHTTP handles explicit individual or bulk cleanup. Validation is completed
// for the whole bounded request before any model mutation starts.
func (handler *ModelRemoval) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteAPI(w, http.StatusMethodNotAllowed, httpx.Response{Error: "method_not_allowed"})
		return
	}
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10))
	decoder.DisallowUnknownFields()
	var request struct {
		Models []domain.ModelRemovalRef `json:"models"`
	}
	if err := decoder.Decode(&request); err != nil || len(request.Models) == 0 || len(request.Models) > 500 {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "invalid model removal request"})
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: "invalid model removal request"})
		return
	}
	for index, model := range request.Models {
		ref, err := model.Normalize()
		if err != nil {
			httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: err.Error()})
			return
		}
		request.Models[index] = ref
	}
	result := domain.ModelRemovalResult{Removed: []domain.ModelRemovalRef{}, Skipped: []domain.ModelRemovalRef{}, Errors: []domain.ModelRemovalError{}}
	for _, model := range request.Models {
		removed, err := handler.Remove(model)
		switch {
		case err != nil:
			result.Errors = append(result.Errors, domain.ModelRemovalError{Model: model, Error: err.Error()})
		case removed:
			result.Removed = append(result.Removed, model)
		default:
			result.Skipped = append(result.Skipped, model)
		}
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: result})
}
