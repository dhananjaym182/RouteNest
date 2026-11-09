package adminapi

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"

	"routenest/internal/accounts"
	"routenest/internal/httpx"
)

const FinalKeyDeletionConfirmation = "disable-client-authentication"

type KeyAuditKind string

const (
	KeyAuditCreate      KeyAuditKind = "create"
	KeyAuditDelete      KeyAuditKind = "delete"
	KeyAuditDeleteFinal KeyAuditKind = "delete_final"
)

type KeyAuditEvent struct {
	Kind    KeyAuditKind
	Outcome string
}

// KeyManager serves the admin API-key endpoints. Storage lives in the account
// pool; callbacks keep this package independent of pool locking and persistence.
type KeyManager struct {
	ListKeys       func() ([]string, error)
	GenerateKey    func() (string, error)
	DeleteKey      func(string, bool, bool) (accounts.ClientKeyDeleteResult, error)
	CanDeleteFinal func(*http.Request) bool
	Audit          func(*http.Request, KeyAuditEvent) error
	Translate      func(*http.Request, string) string
}

func (m *KeyManager) msg(r *http.Request, key string) string {
	if m.Translate != nil {
		return m.Translate(r, key)
	}
	return key
}

// Get handles GET /admin/api/keys.
func (m *KeyManager) Get(w http.ResponseWriter, r *http.Request) {
	var keys []string
	if m.ListKeys != nil {
		var err error
		keys, err = m.ListKeys()
		if err != nil {
			httpx.WriteAPI(w, http.StatusServiceUnavailable, httpx.Response{Error: "key_store_unavailable"})
			return
		}
	}
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"keys": keys}})
}

// Generate handles POST /admin/api/keys/generate.
func (m *KeyManager) Generate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteAPI(w, http.StatusMethodNotAllowed, httpx.Response{Error: m.msg(r, "method_not_allowed")})
		return
	}
	if m.GenerateKey == nil {
		httpx.WriteAPI(w, http.StatusInternalServerError, httpx.Response{Error: "key store unavailable"})
		return
	}
	key, err := m.GenerateKey()
	if err != nil {
		m.audit(r, KeyAuditEvent{Kind: KeyAuditCreate, Outcome: "failed"})
		httpx.WriteAPI(w, keyMutationErrorStatus(err), httpx.Response{Error: keyMutationErrorCode(err)})
		return
	}
	m.audit(r, KeyAuditEvent{Kind: KeyAuditCreate, Outcome: "success"})
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: map[string]any{"key": key}})
}

// Delete handles POST /admin/api/keys/delete with body {"key": "..."}.
func (m *KeyManager) Delete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteAPI(w, http.StatusMethodNotAllowed, httpx.Response{Error: m.msg(r, "method_not_allowed")})
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: err.Error()})
		return
	}
	defer r.Body.Close()
	var req struct {
		Key          string `json:"key"`
		Confirmation string `json:"confirmation"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		httpx.WriteAPI(w, http.StatusBadRequest, httpx.Response{Error: m.msg(r, "invalid_json")})
		return
	}
	if m.DeleteKey == nil {
		httpx.WriteAPI(w, http.StatusInternalServerError, httpx.Response{Error: "key_store_unavailable"})
		return
	}
	confirmed := req.Confirmation == FinalKeyDeletionConfirmation
	elevated := m.CanDeleteFinal != nil && m.CanDeleteFinal(r)
	result, err := m.DeleteKey(req.Key, confirmed, elevated)
	auditKind := KeyAuditDelete
	if result.DeletedFinalKey || errors.Is(err, accounts.ErrFinalClientKeyConfirmationRequired) || errors.Is(err, accounts.ErrFinalClientKeyAuthorizationRequired) {
		auditKind = KeyAuditDeleteFinal
	}
	if err != nil {
		m.audit(r, KeyAuditEvent{Kind: auditKind, Outcome: "failed"})
		httpx.WriteAPI(w, keyMutationErrorStatus(err), httpx.Response{Error: keyMutationErrorCode(err)})
		return
	}
	m.audit(r, KeyAuditEvent{Kind: auditKind, Outcome: "success"})
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Message: m.msg(r, "key_deleted")})
}

func (m *KeyManager) audit(r *http.Request, event KeyAuditEvent) {
	if m.Audit == nil {
		return
	}
	if err := m.Audit(r, event); err != nil {
		log.Printf("Failed to persist redacted client API-key audit event (%s/%s): %v", event.Kind, event.Outcome, err)
	}
}

func keyMutationErrorStatus(err error) int {
	switch {
	case errors.Is(err, accounts.ErrClientKeyRequired):
		return http.StatusBadRequest
	case errors.Is(err, accounts.ErrClientKeyNotFound):
		return http.StatusNotFound
	case errors.Is(err, accounts.ErrFinalClientKeyConfirmationRequired):
		return http.StatusConflict
	case errors.Is(err, accounts.ErrFinalClientKeyAuthorizationRequired):
		return http.StatusForbidden
	case errors.Is(err, accounts.ErrClientKeyGeneration):
		return http.StatusInternalServerError
	case errors.Is(err, accounts.ErrPoolStorageUnavailable):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func keyMutationErrorCode(err error) string {
	switch {
	case errors.Is(err, accounts.ErrClientKeyRequired):
		return "key_required"
	case errors.Is(err, accounts.ErrClientKeyNotFound):
		return "key_not_found"
	case errors.Is(err, accounts.ErrFinalClientKeyConfirmationRequired):
		return "final_key_confirmation_required"
	case errors.Is(err, accounts.ErrFinalClientKeyAuthorizationRequired):
		return "permission_denied"
	case errors.Is(err, accounts.ErrClientKeyGeneration):
		return "key_generation_failed"
	case errors.Is(err, accounts.ErrPoolStorageUnavailable):
		return "key_store_unavailable"
	default:
		return "key_persistence_failed"
	}
}
