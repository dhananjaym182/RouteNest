package adminauth

import (
	"io"
	"sync"
	"time"
)

type UserView struct {
	ID                     string    `json:"id"`
	Username               string    `json:"username"`
	Roles                  []Role    `json:"roles"`
	Disabled               bool      `json:"disabled"`
	Revision               uint64    `json:"revision"`
	Provider               string    `json:"provider,omitempty"`
	PasswordConfigured     bool      `json:"passwordConfigured"`
	LegacyRotationRequired bool      `json:"legacyRotationRequired"`
	CreatedAt              time.Time `json:"createdAt"`
}

func (u User) Redacted() UserView {
	return UserView{ID: u.ID, Username: u.Username, Roles: append([]Role(nil), u.Roles...), Disabled: u.Disabled, Revision: u.Revision, Provider: u.Provider, PasswordConfigured: u.PasswordPHC != "" || u.Legacy != nil, LegacyRotationRequired: u.Legacy != nil, CreatedAt: u.CreatedAt}
}

type lockedReader struct {
	mu sync.Mutex
	r  io.Reader
}

func (r *lockedReader) Read(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.r.Read(b)
}
