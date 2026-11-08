package adminauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

var ErrPasswordRotationRequired = fmt.Errorf("legacy administrator password must be rotated")

type Options struct {
	PasswordParams PasswordParams
	Random         io.Reader
	SessionTTL     time.Duration
	Now            func() time.Time
	// BootstrapPassword is consumed only before initialization. DeliverBootstrap
	// must target a controlling TTY or a pre-server native dialog, never a log/HTTP.
	BootstrapPassword string
	DeliverBootstrap  func(string) error
	Legacy            *LegacyCredential
	// MarkLegacyMigrated writes an unmatchable sentinel only AFTER durable rotation.
	MarkLegacyMigrated func() error
}
type Service struct {
	repo       Repository
	hasher     *PasswordHasher
	random     io.Reader
	now        func() time.Time
	ttl        time.Duration
	throttle   *Throttler
	flows      *AuthFlowStore
	markLegacy func() error
	mu         sync.RWMutex
	degraded   bool
	denials    *denialBuffer
}

func NewService(repo Repository, opts Options) (*Service, error) {
	if repo == nil {
		return nil, fmt.Errorf("administrator repository required")
	}
	if opts.Random == nil {
		opts.Random = rand.Reader
	}
	opts.Random = &lockedReader{r: opts.Random}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.SessionTTL == 0 {
		opts.SessionTTL = 12 * time.Hour
	}
	if opts.SessionTTL <= 0 || opts.SessionTTL > 24*time.Hour {
		return nil, fmt.Errorf("invalid session lifetime")
	}
	if opts.PasswordParams.Memory == 0 {
		opts.PasswordParams = DefaultPasswordParams
	}
	h, e := NewPasswordHasher(opts.PasswordParams, opts.Random)
	if e != nil {
		return nil, e
	}
	s := &Service{repo: repo, hasher: h, random: opts.Random, now: opts.Now, ttl: opts.SessionTTL, throttle: NewThrottler(), flows: NewAuthFlowStore(), markLegacy: opts.MarkLegacyMigrated, denials: newDenialBuffer()}
	state, e := repo.Load(context.Background())
	if e != nil {
		return nil, e
	}
	if e = ValidateState(state); e != nil {
		return nil, e
	}
	if state.Initialized {
		u := state.Users[BreakGlassID]
		if opts.Legacy != nil && ValidLegacy(*opts.Legacy) && u.Legacy == nil && u.PasswordPHC != "" && s.markLegacy != nil {
			if e = s.markLegacy(); e != nil {
				return nil, fmt.Errorf("legacy downgrade sentinel persistence failed: %w", e)
			}
		}
		return s, nil
	}
	if opts.Legacy != nil {
		if !ValidLegacy(*opts.Legacy) {
			return nil, fmt.Errorf("invalid legacy password authority")
		}
		legacy := *opts.Legacy
		e = s.apply(context.Background(), AuditLegacyMigrate, "", BreakGlassID, "", func(st *State) error {
			if st.Initialized {
				return ErrAlreadyInitialized
			}
			st.Initialized = true
			st.BootstrapPHC = ""
			st.Users[BreakGlassID] = User{ID: BreakGlassID, Username: BreakGlassUsername, Legacy: &legacy, Roles: []Role{RoleBreakGlass, RoleSecurityAdmin}, Revision: 1, CreatedAt: s.now().UTC()}
			return nil
		})
		return s, e
	}
	secret := opts.BootstrapPassword
	if secret == "" {
		secret, e = randomToken(s.random)
		if e != nil {
			return nil, e
		}
		if opts.DeliverBootstrap == nil {
			return nil, fmt.Errorf("bootstrap needs a controlling TTY or native dialog; configure ROUTENEST_ADMIN_BOOTSTRAP_PASSWORD for a fresh headless installation")
		}
	}
	if len(secret) < MinBootstrapSecretBytes || len(secret) > MaxPasswordBytes {
		return nil, fmt.Errorf("bootstrap secret must contain %d to %d bytes", MinBootstrapSecretBytes, MaxPasswordBytes)
	}
	phc, e := s.hasher.hash(context.Background(), secret)
	if e != nil {
		return nil, e
	}
	e = s.apply(context.Background(), AuditBootstrapPending, "", BreakGlassID, "", func(st *State) error {
		if st.Initialized {
			return ErrAlreadyInitialized
		}
		st.BootstrapPHC = phc
		return nil
	})
	if e != nil {
		return nil, e
	}
	if opts.BootstrapPassword == "" {
		if e = opts.DeliverBootstrap(secret); e != nil {
			return nil, fmt.Errorf("deliver local bootstrap: %w", e)
		}
	}
	return s, nil
}
func (s *Service) lockdown() { s.mu.Lock(); s.degraded = true; s.mu.Unlock(); s.flows.Clear() }
func (s *Service) check() error {
	s.mu.RLock()
	v := s.degraded
	s.mu.RUnlock()
	if v {
		return ErrDegraded
	}
	return nil
}
func (s *Service) apply(ctx context.Context, kind AuditKind, actor, target, source string, mutate func(*State) error) error {
	if e := s.check(); e != nil {
		return e
	}
	id, e := randomToken(s.random)
	if e != nil {
		return e
	}
	if ip := net.ParseIP(source); ip != nil {
		source = ip.String()
	} else {
		source = ""
	}
	event := AuditEvent{ID: id, Kind: kind, ActorID: actor, TargetID: target, Source: source, Outcome: "success", At: s.now().UTC()}
	return s.repo.Apply(ctx, event, func(st *State) error {
		wasInitialized := st.Initialized
		if e := mutate(st); e != nil {
			return e
		}
		if wasInitialized && !st.Initialized {
			return ErrInvalidState
		}
		return ValidateState(*st)
	})
}
func (s *Service) CompleteBootstrap(ctx context.Context, secret, password string, source ...string) (Credentials, error) {
	src := ""
	if len(source) > 0 {
		src = source[0]
	}
	if ok, retry := s.allow(src, BreakGlassUsername); !ok {
		return Credentials{}, &RateLimitError{RetryAfter: retry}
	}
	if e := s.check(); e != nil {
		return Credentials{}, e
	}
	st, e := s.repo.Load(ctx)
	if e != nil {
		return Credentials{}, e
	}
	recovering := st.Initialized && st.RecoveryPHC != ""
	if st.Initialized && !recovering {
		return Credentials{}, ErrAlreadyInitialized
	}
	expected := st.BootstrapPHC
	if recovering {
		expected = st.RecoveryPHC
	}
	ok, e := s.hasher.Verify(ctx, expected, secret)
	if e != nil || !ok {
		return Credentials{}, ErrUnauthorized
	}
	phc, e := s.hasher.Hash(ctx, password)
	if e != nil {
		return Credentials{}, e
	}
	u := User{ID: BreakGlassID, Username: BreakGlassUsername, Roles: []Role{RoleBreakGlass, RoleSecurityAdmin}, Revision: 1, CreatedAt: s.now().UTC()}
	if recovering {
		u = st.Users[BreakGlassID]
		u.Revision++
	}
	u.PasswordPHC = phc
	u.Legacy = nil
	c, session, e := s.newSession(u, MethodLocal, "")
	if e != nil {
		return Credentials{}, e
	}
	kind := AuditBootstrapComplete
	if recovering {
		kind = AuditRecoveryComplete
	}
	e = s.apply(ctx, kind, BreakGlassID, BreakGlassID, src, func(next *State) error {
		if recovering {
			if !next.Initialized || next.RecoveryPHC != expected || next.Users[BreakGlassID].Revision+1 != u.Revision {
				return ErrUnauthorized
			}
		} else if next.Initialized || next.BootstrapPHC != expected {
			return ErrUnauthorized
		}
		next.Initialized = true
		next.BootstrapPHC = ""
		next.RecoveryPHC = ""
		next.Users[BreakGlassID] = u
		next.Sessions[session.TokenHash] = session
		return nil
	})
	if e != nil {
		return Credentials{}, e
	}
	return c, nil
}

func (s *Service) allow(source, username string) (bool, time.Duration) {
	ok, retry := s.throttle.Allow(source)
	if !ok {
		return false, retry
	}
	if username == BreakGlassUsername {
		return s.throttle.Allow("reserved-local:" + source)
	}
	return true, 0
}

func (s *Service) Login(ctx context.Context, username, password, source string) (credentials Credentials, err error) {
	defer func() {
		if err != nil {
			s.RecordDenial(AuditLoginDenied, source)
		}
	}()
	if e := s.check(); e != nil {
		return Credentials{}, e
	}
	username = NormalizeUsername(username)
	if len(username) == 0 || len(username) > MaxUsernameBytes || len(password) > MaxPasswordBytes {
		return Credentials{}, ErrUnauthorized
	}
	if ok, retry := s.allow(source, username); !ok {
		return Credentials{}, &RateLimitError{RetryAfter: retry}
	}
	st, e := s.repo.Load(ctx)
	if e != nil {
		return Credentials{}, e
	}
	if !st.Initialized || st.RecoveryPHC != "" {
		return Credentials{}, ErrNotInitialized
	}
	var u User
	found := false
	for _, v := range st.Users {
		if v.Username == username {
			u = v
			found = true
			break
		}
	}
	if !found || u.Disabled || u.Provider != "" {
		_ = s.hasher.DummyVerify(ctx, password)
		return Credentials{}, ErrUnauthorized
	}
	if u.Legacy != nil {
		if !VerifyLegacy(*u.Legacy, password) {
			_ = s.hasher.DummyVerify(ctx, password)
			return Credentials{}, ErrUnauthorized
		}
		return Credentials{}, ErrPasswordRotationRequired
	}
	ok, e := s.hasher.Verify(ctx, u.PasswordPHC, password)
	if e != nil || !ok {
		return Credentials{}, ErrUnauthorized
	}
	c, session, e := s.newSession(u, MethodLocal, "")
	if e != nil {
		return Credentials{}, e
	}
	var rehash string
	if s.hasher.NeedsRehash(u.PasswordPHC) {
		rehash, e = s.hasher.hash(ctx, password)
		if e != nil {
			return Credentials{}, e
		}
	}
	e = s.apply(ctx, AuditLoginSuccess, u.ID, u.ID, source, func(next *State) error {
		current, ok := next.Users[u.ID]
		if !ok || current.Disabled || current.Revision != u.Revision || current.PasswordPHC != u.PasswordPHC {
			return ErrUnauthorized
		}
		if rehash != "" {
			current.PasswordPHC = rehash
			next.Users[u.ID] = current
		}
		pruneSessions(next, s.now())
		next.Sessions[session.TokenHash] = session
		return nil
	})
	if e != nil {
		return Credentials{}, e
	}
	return c, nil
}
func (s *Service) CompleteLegacyRotation(ctx context.Context, username, oldPassword, newPassword, source string) (Credentials, error) {
	if ok, retry := s.allow(source, NormalizeUsername(username)); !ok {
		return Credentials{}, &RateLimitError{RetryAfter: retry}
	}
	if e := s.check(); e != nil {
		return Credentials{}, e
	}
	st, e := s.repo.Load(ctx)
	if e != nil {
		return Credentials{}, e
	}
	u, ok := st.Users[BreakGlassID]
	if !ok || u.Username != NormalizeUsername(username) || u.Legacy == nil || !VerifyLegacy(*u.Legacy, oldPassword) {
		_ = s.hasher.DummyVerify(ctx, oldPassword)
		return Credentials{}, ErrUnauthorized
	}
	phc, e := s.hasher.Hash(ctx, newPassword)
	if e != nil {
		return Credentials{}, e
	}
	legacy := *u.Legacy
	u.PasswordPHC = phc
	u.Legacy = nil
	u.Revision++
	c, session, e := s.newSession(u, MethodLocal, "")
	if e != nil {
		return Credentials{}, e
	}
	e = s.apply(ctx, AuditLegacyMigrate, u.ID, u.ID, source, func(next *State) error {
		current, ok := next.Users[u.ID]
		if !ok || current.Legacy == nil || *current.Legacy != legacy || current.Revision+1 != u.Revision {
			return ErrUnauthorized
		}
		next.Users[u.ID] = u
		revokeUserSessions(next, u.ID)
		next.Sessions[session.TokenHash] = session
		return nil
	})
	if e != nil {
		return Credentials{}, e
	}
	if s.markLegacy != nil {
		if e = s.markLegacy(); e != nil {
			s.lockdown()
			return Credentials{}, fmt.Errorf("legacy downgrade sentinel persistence failed: %w", e)
		}
	}
	return c, nil
}
func (s *Service) RotatePassword(ctx context.Context, p Principal, oldPassword, newPassword string) error {
	if p.Method != MethodLocal {
		return ErrForbidden
	}
	if e := s.validatePrincipal(ctx, p); e != nil {
		return e
	}
	st, e := s.repo.Load(ctx)
	if e != nil {
		return e
	}
	u := st.Users[p.UserID]
	ok, e := s.hasher.Verify(ctx, u.PasswordPHC, oldPassword)
	if e != nil || !ok {
		return ErrUnauthorized
	}
	phc, e := s.hasher.Hash(ctx, newPassword)
	if e != nil {
		return e
	}
	return s.apply(ctx, AuditPasswordRotate, p.UserID, p.UserID, "", func(next *State) error {
		if e := validatePrincipalState(*next, p, s.now()); e != nil {
			return e
		}
		current, ok := next.Users[p.UserID]
		if !ok || current.Revision != u.Revision || current.PasswordPHC != u.PasswordPHC {
			return ErrUnauthorized
		}
		current.PasswordPHC = phc
		current.Revision++
		next.Users[current.ID] = current
		revokeUserSessions(next, current.ID)
		return nil
	})
}

// Mutate is the integration seam for audited user/role/provider/listener changes.
// It enforces current session permission and security administrator invariants.
func (s *Service) Mutate(ctx context.Context, p Principal, kind AuditKind, target string, fn func(*State) error) error {
	if e := s.validatePrincipal(ctx, p); e != nil {
		return e
	}
	if e := p.Authorize(PermissionSecurityWrite); e != nil {
		return e
	}
	switch kind {
	case AuditUserChange, AuditRoleChange, AuditSSOChange, AuditListenerChange:
	default:
		return ErrForbidden
	}
	changedProviders := []string{}
	err := s.apply(ctx, kind, p.UserID, target, "", func(st *State) error {
		if e := validatePrincipalState(*st, p, s.now()); e != nil {
			return e
		}
		before := CloneState(*st)
		if e := fn(st); e != nil {
			return e
		}
		if st.Initialized != before.Initialized || st.BootstrapPHC != before.BootstrapPHC || st.RecoveryPHC != before.RecoveryPHC {
			return ErrForbidden
		}
		reserved := st.Users[BreakGlassID]
		oldReserved := before.Users[BreakGlassID]
		if reserved.PasswordPHC != oldReserved.PasswordPHC || !equalLegacy(reserved.Legacy, oldReserved.Legacy) {
			return ErrForbidden
		}
		for id, old := range before.Users {
			current, ok := st.Users[id]
			if !ok {
				if id == BreakGlassID {
					return ErrForbidden
				}
				revokeUserSessions(st, id)
				continue
			}
			if old.Username != current.Username || old.Disabled != current.Disabled || strings.Join(rolesStrings(old.Roles), ",") != strings.Join(rolesStrings(current.Roles), ",") || old.PasswordPHC != current.PasswordPHC || old.Provider != current.Provider || old.Subject != current.Subject {
				current.Revision = old.Revision + 1
				revokeUserSessions(st, id)
			} else {
				current.Revision = old.Revision
			}
			st.Users[id] = current
		}
		for id, u := range st.Users {
			if _, exists := before.Users[id]; !exists {
				u.Revision = 1
				st.Users[id] = u
			}
		}
		for id, old := range before.Providers {
			current, ok := st.Providers[id]
			if !ok || old.Type != current.Type || old.Enabled != current.Enabled || !bytes.Equal(old.Configuration, current.Configuration) {
				changedProviders = append(changedProviders, id)
				for hash, session := range st.Sessions {
					if session.Provider == id {
						delete(st.Sessions, hash)
					}
				}
				if ok {
					current.Revision = old.Revision + 1
					st.Providers[id] = current
				}
			} else {
				current.Revision = old.Revision
				st.Providers[id] = current
			}
		}
		for id, provider := range st.Providers {
			if _, exists := before.Providers[id]; !exists {
				provider.Revision = 1
				st.Providers[id] = provider
			}
		}
		return nil
	})
	if err == nil {
		for _, id := range changedProviders {
			s.flows.RevokeProvider(id)
		}
	}
	return err
}
func rolesStrings(rs []Role) []string {
	v := make([]string, len(rs))
	for i, r := range rs {
		v[i] = string(r)
	}
	return v
}
func (s *Service) Flows() *AuthFlowStore { return s.flows }
func (s *Service) State(ctx context.Context) (State, error) {
	if e := s.check(); e != nil {
		return State{}, e
	}
	return s.repo.Load(ctx)
}
func (s *Service) Readiness() Readiness {
	r := Readiness{OIDCProviders: map[string]ProviderReadiness{}, SAMLProviders: map[string]ProviderReadiness{}}
	if s.check() == nil {
		st, e := s.repo.Load(context.Background())
		r.AuthStoreReady = e == nil
		r.AuditJournalReady = e == nil
		u, ok := st.Users[BreakGlassID]
		r.BreakGlassReady = e == nil && st.Initialized && st.RecoveryPHC == "" && ok && !u.Disabled
		r.CSRFReady = e == nil
	}
	r.Recompute()
	return r
}

func equalLegacy(a, b *LegacyCredential) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
