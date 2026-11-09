// Package httpapi holds transport-layer HTTP concerns shared by public and
// admin endpoints: request authentication and health reporting. Route wiring
// and provider/dispatch behavior stay in the composition root.
package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"routenest/internal/domain"
	"routenest/internal/httpx"
)

var errClientAuthSnapshotUnavailable = errors.New("client authentication snapshot unavailable")

// APIKeyAuth guards public endpoints with pool-managed API keys. When no keys
// are configured the endpoint stays open, matching the historical behavior.
type APIKeyAuth struct {
	// Snapshot returns a detached, internally consistent key snapshot. An empty
	// successful snapshot means "no auth"; an error fails closed.
	Snapshot func() ([]string, error)
	// Unauthorized writes the rejection body. It is injected so each protocol
	// keeps its own error shape.
	Unauthorized func(w http.ResponseWriter, r *http.Request)
	Unavailable  func(w http.ResponseWriter, r *http.Request)
}

// ExtractKey reads x-api-key or a bearer Authorization header.
func ExtractKey(r *http.Request) string {
	if key := r.Header.Get("x-api-key"); key != "" {
		return key
	}
	if b := r.Header.Get("Authorization"); strings.HasPrefix(b, "Bearer ") {
		return b[len("Bearer "):]
	}
	return ""
}

// Wrap applies CORS and the key check around next.
func (a *APIKeyAuth) Wrap(next http.HandlerFunc) http.HandlerFunc {
	return httpx.CORS(func(w http.ResponseWriter, r *http.Request) {
		var keys []string
		err := errClientAuthSnapshotUnavailable
		if a.Snapshot != nil {
			keys, err = a.Snapshot()
		}
		if err != nil {
			if a.Unavailable != nil {
				a.Unavailable(w, r)
			} else {
				httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{
					"error": map[string]string{
						"message": "client API-key storage is unavailable; restore or repair the account-pool file, then restart RouteNest",
						"type":    "auth_storage_error",
					},
				})
			}
			return
		}
		if len(keys) == 0 {
			next(w, r)
			return
		}
		key := ExtractKey(r)
		for _, k := range keys {
			if k == key {
				next(w, r)
				return
			}
		}
		a.Unauthorized(w, r)
	})
}

// Health reports liveness, the active Cline account count, and redacted runtime
// enforcement status. RuntimeState supplies public codes, never raw errors.
type Health struct {
	Version        func() string
	ActiveAccounts func() int
	RuntimeState   func() domain.RuntimeStateHealth
}

// Handler writes the health payload.
func (h Health) Handler(w http.ResponseWriter, r *http.Request) {
	runtimeState := domain.RuntimeStateHealth{
		Backend:     "unknown",
		Degraded:    true,
		Stale:       true,
		Enforcement: "unavailable",
		Reason:      "runtime_state_unavailable",
	}
	if h.RuntimeState != nil {
		runtimeState = h.RuntimeState()
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"version":        h.Version(),
		"activeAccounts": h.ActiveAccounts(),
		"runtimeState":   runtimeState,
	})
}

// Readiness reports whether security-sensitive storage is available for
// request processing. It deliberately exposes only a stable status code, never
// a storage path, key, or underlying error.
type Readiness struct {
	AuthenticationStorage func() error
}

func (h Readiness) Handler(w http.ResponseWriter, r *http.Request) {
	if h.AuthenticationStorage == nil || h.AuthenticationStorage() != nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status":                "not_ready",
			"authenticationStorage": "unavailable",
		})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"status":                "ready",
		"authenticationStorage": "ready",
	})
}
