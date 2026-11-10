package adminauth

import (
	"context"
	"fmt"
	"sort"
	"time"
)

func (s *Service) CreateLocalUser(ctx context.Context, p Principal, username, password string, roles []Role) (UserView, error) {
	if e := s.validatePrincipal(ctx, p); e != nil {
		return UserView{}, e
	}
	if e := p.Authorize(PermissionSecurityWrite); e != nil {
		return UserView{}, e
	}
	username = NormalizeUsername(username)
	if username == "" || len(username) > MaxUsernameBytes {
		return UserView{}, fmt.Errorf("invalid administrator username")
	}
	roles, e := normalizeAssignableRoles(roles)
	if e != nil {
		return UserView{}, e
	}
	phc, e := s.hasher.Hash(ctx, password)
	if e != nil {
		return UserView{}, e
	}
	id, e := randomToken(s.random)
	if e != nil {
		return UserView{}, e
	}
	u := User{ID: "local-" + id, Username: username, PasswordPHC: phc, Roles: append([]Role(nil), roles...), Revision: 1, CreatedAt: s.now().UTC()}
	e = s.Mutate(ctx, p, AuditUserChange, u.ID, func(st *State) error {
		for _, existing := range st.Users {
			if existing.Username == username {
				return fmt.Errorf("administrator username already exists")
			}
		}
		st.Users[u.ID] = u
		return nil
	})
	if e != nil {
		return UserView{}, e
	}
	return u.Redacted(), nil
}
func (s *Service) ListUsers(ctx context.Context, p Principal) ([]UserView, error) {
	if e := s.validatePrincipal(ctx, p); e != nil {
		return nil, e
	}
	if e := p.Authorize(PermissionSecurityWrite); e != nil {
		return nil, e
	}
	st, e := s.repo.Load(ctx)
	if e != nil {
		return nil, e
	}
	users := make([]UserView, 0, len(st.Users))
	for _, u := range st.Users {
		users = append(users, u.Redacted())
	}
	return users, nil
}
func (s *Service) SetLocalPassword(ctx context.Context, p Principal, userID, password string) error {
	return s.SetLocalPasswordAtRevision(ctx, p, userID, 0, password)
}

func (s *Service) SetLocalPasswordAtRevision(ctx context.Context, p Principal, userID string, expectedRevision uint64, password string) error {
	if userID == BreakGlassID || userID == p.UserID {
		return ErrForbidden
	}
	if e := s.validatePrincipal(ctx, p); e != nil {
		return e
	}
	if e := p.Authorize(PermissionSecurityWrite); e != nil {
		return e
	}
	phc, e := s.hasher.Hash(ctx, password)
	if e != nil {
		return e
	}
	return s.Mutate(ctx, p, AuditUserChange, userID, func(st *State) error {
		u, ok := st.Users[userID]
		if !ok || u.Provider != "" {
			return ErrForbidden
		}
		if expectedRevision != 0 && u.Revision != expectedRevision {
			return ErrConflict
		}
		u.PasswordPHC = phc
		u.Legacy = nil
		st.Users[userID] = u
		return nil
	})
}

func (s *Service) UpdateUser(ctx context.Context, p Principal, userID string, expectedRevision uint64, roles []Role, disabled bool) (UserView, error) {
	if userID == "" || userID == BreakGlassID || userID == p.UserID || expectedRevision == 0 {
		return UserView{}, ErrForbidden
	}
	if e := s.validatePrincipal(ctx, p); e != nil {
		return UserView{}, e
	}
	if e := p.Authorize(PermissionSecurityWrite); e != nil {
		return UserView{}, e
	}
	roles, e := normalizeAssignableRoles(roles)
	if e != nil {
		return UserView{}, e
	}
	e = s.Mutate(ctx, p, AuditRoleChange, userID, func(st *State) error {
		u, ok := st.Users[userID]
		if !ok {
			return ErrForbidden
		}
		if u.Revision != expectedRevision {
			return ErrConflict
		}
		if u.Provider != "" && !equalRoles(u.Roles, roles) {
			return ErrForbidden
		}
		u.Roles = append([]Role(nil), roles...)
		u.Disabled = disabled
		st.Users[userID] = u
		return nil
	})
	if e != nil {
		return UserView{}, e
	}
	state, e := s.repo.Load(ctx)
	if e != nil {
		return UserView{}, e
	}
	updated, ok := state.Users[userID]
	if !ok {
		return UserView{}, ErrInvalidState
	}
	return updated.Redacted(), nil
}

func (s *Service) DeleteLocalUser(ctx context.Context, p Principal, userID string, expectedRevision uint64) error {
	if userID == "" || userID == BreakGlassID || userID == p.UserID || expectedRevision == 0 {
		return ErrForbidden
	}
	if e := s.validatePrincipal(ctx, p); e != nil {
		return e
	}
	if e := p.Authorize(PermissionSecurityWrite); e != nil {
		return e
	}
	return s.Mutate(ctx, p, AuditUserChange, userID, func(st *State) error {
		u, ok := st.Users[userID]
		if !ok || u.Provider != "" {
			return ErrForbidden
		}
		if u.Revision != expectedRevision {
			return ErrConflict
		}
		delete(st.Users, userID)
		return nil
	})
}

func normalizeAssignableRoles(roles []Role) ([]Role, error) {
	if len(roles) == 0 {
		return nil, ErrForbidden
	}
	seen := make(map[Role]struct{}, len(roles))
	result := make([]Role, 0, len(roles))
	for _, role := range roles {
		if !ValidRole(role) || role == RoleBreakGlass {
			return nil, ErrForbidden
		}
		if _, duplicate := seen[role]; duplicate {
			return nil, ErrForbidden
		}
		seen[role] = struct{}{}
		result = append(result, role)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}

type SessionView struct {
	ID        string     `json:"id"`
	UserID    string     `json:"userID"`
	Method    AuthMethod `json:"method"`
	Provider  string     `json:"provider,omitempty"`
	DeviceID  string     `json:"deviceID,omitempty"`
	IssuedAt  time.Time  `json:"issuedAt"`
	ExpiresAt time.Time  `json:"expiresAt"`
	Current   bool       `json:"current"`
}

func (s *Service) ListSessions(ctx context.Context, p Principal) ([]SessionView, error) {
	if e := s.validatePrincipal(ctx, p); e != nil {
		return nil, e
	}
	if e := p.Authorize(PermissionSecurityWrite); e != nil {
		return nil, e
	}
	st, e := s.repo.Load(ctx)
	if e != nil {
		return nil, e
	}
	out := []SessionView{}
	for h, v := range st.Sessions {
		if !v.ExpiresAt.After(s.now()) {
			continue
		}
		out = append(out, SessionView{ID: h, UserID: v.UserID, Method: v.Method, Provider: v.Provider, DeviceID: v.DeviceID, IssuedAt: v.IssuedAt, ExpiresAt: v.ExpiresAt, Current: h == p.SessionHash})
	}
	return out, nil
}
