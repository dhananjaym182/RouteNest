package adminauth

import (
	"context"
	"time"
)

func (s *Service) newSession(u User, method AuthMethod, provider string) (Credentials, Session, error) {
	token, e := randomToken(s.random)
	if e != nil {
		return Credentials{}, Session{}, e
	}
	csrf, e := randomToken(s.random)
	if e != nil {
		return Credentials{}, Session{}, e
	}
	now := s.now().UTC()
	session := Session{TokenHash: hashToken(token), CSRFHash: hashToken(csrf), UserID: u.ID, UserRevision: u.Revision, Method: method, Provider: provider, IssuedAt: now, ExpiresAt: now.Add(s.ttl)}
	p := principalFor(u, session)
	return Credentials{Token: token, CSRFToken: csrf, Principal: p, ExpiresAt: session.ExpiresAt}, session, nil
}
func principalFor(u User, session Session) Principal {
	return Principal{UserID: u.ID, Username: u.Username, Roles: append([]Role(nil), u.Roles...), Method: session.Method, Provider: session.Provider, SessionHash: session.TokenHash, CSRFHash: session.CSRFHash, ExpiresAt: session.ExpiresAt}
}
func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	if e := s.check(); e != nil {
		return Principal{}, e
	}
	if len(token) != 43 {
		return Principal{}, ErrUnauthorized
	}
	return s.authenticateHash(ctx, hashToken(token))
}
func (s *Service) authenticateHash(ctx context.Context, hash string) (Principal, error) {
	st, e := s.repo.Load(ctx)
	if e != nil {
		return Principal{}, e
	}
	if !st.Initialized {
		return Principal{}, ErrNotInitialized
	}
	session, ok := st.Sessions[hash]
	if !ok || !session.ExpiresAt.After(s.now()) {
		return Principal{}, ErrUnauthorized
	}
	u, ok := st.Users[session.UserID]
	if !ok || u.Disabled || u.Revision != session.UserRevision || (u.ID == BreakGlassID && session.Method != MethodLocal) {
		return Principal{}, ErrUnauthorized
	}
	return principalFor(u, session), nil
}
func (s *Service) validatePrincipal(ctx context.Context, p Principal) error {
	if e := s.check(); e != nil {
		return e
	}
	current, e := s.authenticateHash(ctx, p.SessionHash)
	if e != nil {
		return e
	}
	if current.UserID != p.UserID || current.CSRFHash != p.CSRFHash || current.Method != p.Method || current.Provider != p.Provider {
		return ErrUnauthorized
	}
	if len(current.Roles) != len(p.Roles) {
		return ErrUnauthorized
	}
	for i, r := range current.Roles {
		if p.Roles[i] != r {
			return ErrUnauthorized
		}
	}
	return nil
}
func (s *Service) ValidateCSRF(p Principal, token string) error {
	if e := s.validatePrincipal(context.Background(), p); e != nil {
		return e
	}
	return ValidateCSRF(p, token)
}
func (s *Service) Logout(ctx context.Context, p Principal) error {
	if e := s.validatePrincipal(ctx, p); e != nil {
		return e
	}
	e := s.apply(ctx, AuditLogout, p.UserID, p.UserID, "", func(st *State) error {
		if e := validatePrincipalState(*st, p, s.now()); e != nil {
			return e
		}
		delete(st.Sessions, p.SessionHash)
		return nil
	})
	if e != nil {
		s.lockdown()
	}
	return e
}
func (s *Service) RevokeSession(ctx context.Context, p Principal, hash string) error {
	if e := s.validatePrincipal(ctx, p); e != nil {
		return e
	}
	if e := p.Authorize(PermissionSecurityWrite); e != nil {
		return e
	}
	e := s.apply(ctx, AuditSessionRevoke, p.UserID, hash, "", func(st *State) error {
		if e := validatePrincipalState(*st, p, s.now()); e != nil {
			return e
		}
		delete(st.Sessions, hash)
		return nil
	})
	if e != nil {
		s.lockdown()
	}
	return e
}
func revokeUserSessions(st *State, id string) {
	for h, v := range st.Sessions {
		if v.UserID == id {
			delete(st.Sessions, h)
		}
	}
}
func pruneSessions(st *State, now time.Time) {
	for h, v := range st.Sessions {
		if !v.ExpiresAt.After(now) {
			delete(st.Sessions, h)
		}
	}
	for len(st.Sessions) >= 4096 {
		var oldest string
		var at time.Time
		for h, v := range st.Sessions {
			if oldest == "" || v.IssuedAt.Before(at) {
				oldest = h
				at = v.IssuedAt
			}
		}
		delete(st.Sessions, oldest)
	}
}

// IssueFederatedSession accepts ONLY an adapter-verified subject; claims must be
// mapped to explicit configured roles before calling. Local identities and the
// reserved break-glass role cannot be authenticated through this seam.
func (s *Service) IssueFederatedSession(ctx context.Context, provider string, expectedProviderRevision uint64, subject, username string, roles []Role, method AuthMethod, source string) (Credentials, error) {
	if method != MethodOIDC && method != MethodSAML || provider == "" || expectedProviderRevision == 0 || subject == "" || len(subject) > 512 {
		return Credentials{}, ErrForbidden
	}
	if len(roles) == 0 {
		return Credentials{}, ErrForbidden
	}
	for _, r := range roles {
		if !ValidRole(r) || r == RoleBreakGlass {
			return Credentials{}, ErrForbidden
		}
	}
	if e := s.check(); e != nil {
		return Credentials{}, e
	}
	st, e := s.repo.Load(ctx)
	if e != nil {
		return Credentials{}, e
	}
	if !st.Initialized || st.RecoveryPHC != "" {
		return Credentials{}, ErrNotInitialized
	}
	configured, ok := st.Providers[provider]
	if !ok || !configured.Enabled || configured.Type != method || configured.Revision != expectedProviderRevision {
		return Credentials{}, ErrForbidden
	}
	var u User
	for _, v := range st.Users {
		if v.Provider == provider && v.Subject == subject {
			u = v
			break
		}
	}
	expectedRevision := u.Revision
	if u.ID == "" {
		id, e := randomToken(s.random)
		if e != nil {
			return Credentials{}, e
		}
		u = User{ID: "sso-" + id, Username: NormalizeUsername(username), Provider: provider, Subject: subject, Roles: append([]Role(nil), roles...), Revision: 1, CreatedAt: s.now().UTC()}
	} else {
		if u.Disabled {
			return Credentials{}, ErrUnauthorized
		}
		if len(roles) != len(u.Roles) || !equalRoles(roles, u.Roles) {
			u.Roles = append([]Role(nil), roles...)
			u.Revision++
		}
	}
	c, session, e := s.newSession(u, method, provider)
	if e != nil {
		return Credentials{}, e
	}
	e = s.apply(ctx, AuditSSOLogin, u.ID, u.ID, source, func(next *State) error {
		currentProvider, ok := next.Providers[provider]
		if !ok || !currentProvider.Enabled || currentProvider.Type != method || currentProvider.Revision != configured.Revision {
			return ErrForbidden
		}
		old, exists := next.Users[u.ID]
		if exists && (old.Disabled || old.Revision != expectedRevision) {
			return ErrUnauthorized
		}
		if exists && old.Revision != u.Revision {
			revokeUserSessions(next, u.ID)
		}
		if !exists && expectedRevision != 0 {
			return ErrUnauthorized
		}
		next.Users[u.ID] = u
		pruneSessions(next, s.now())
		next.Sessions[session.TokenHash] = session
		return nil
	})
	if e != nil {
		return Credentials{}, e
	}
	return c, nil
}
func equalRoles(a, b []Role) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func validatePrincipalState(st State, p Principal, now time.Time) error {
	session, ok := st.Sessions[p.SessionHash]
	if !ok || session.UserID != p.UserID || session.CSRFHash != p.CSRFHash || session.Method != p.Method || session.Provider != p.Provider || !session.ExpiresAt.After(now) {
		return ErrUnauthorized
	}
	u, ok := st.Users[p.UserID]
	if !ok || u.Disabled || u.Revision != session.UserRevision || !equalRoles(u.Roles, p.Roles) {
		return ErrUnauthorized
	}
	return nil
}
