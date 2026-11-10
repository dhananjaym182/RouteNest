package adminauth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"sort"
	"time"
)

func (s *Service) EnrollTrustedDevice(ctx context.Context, p Principal, currentPassword string, metadata TrustedDeviceMetadata, replaceID string) (TrustedDeviceCredential, error) {
	return s.enrollTrustedDevice(ctx, p, currentPassword, metadata, replaceID, false)
}

func (s *Service) EnrollTrustedDeviceAfterAuthentication(ctx context.Context, p Principal, metadata TrustedDeviceMetadata, replaceID string) (TrustedDeviceCredential, error) {
	return s.enrollTrustedDevice(ctx, p, "", metadata, replaceID, true)
}

func (s *Service) trustedDevicePrincipal(ctx context.Context, p Principal) (State, User, error) {
	if p.Method != MethodLocal || p.Provider != "" {
		return State{}, User{}, ErrForbidden
	}
	if err := s.validatePrincipal(ctx, p); err != nil {
		return State{}, User{}, err
	}
	if err := p.Authorize(PermissionSecurityWrite); err != nil {
		return State{}, User{}, err
	}
	state, err := s.repo.Load(ctx)
	if err != nil {
		return State{}, User{}, err
	}
	if err := validatePrincipalState(state, p, s.now()); err != nil {
		return State{}, User{}, err
	}
	user := state.Users[p.UserID]
	if !state.Initialized || state.RecoveryPHC != "" || user.Provider != "" || user.PasswordPHC == "" || user.Legacy != nil {
		return State{}, User{}, ErrForbidden
	}
	return state, user, nil
}

func recentlyPasswordAuthenticated(session Session, now time.Time) bool {
	return session.Method == MethodLocal && session.Provider == "" && session.DeviceID == "" && !session.IssuedAt.IsZero() && !now.Before(session.IssuedAt) && now.Sub(session.IssuedAt) < trustedDeviceAuthenticationWindow
}

func (s *Service) enrollTrustedDevice(ctx context.Context, p Principal, currentPassword string, metadata TrustedDeviceMetadata, replaceID string, afterAuthentication bool) (credential TrustedDeviceCredential, err error) {
	defer func() {
		if err != nil {
			s.RecordDenial(AuditTrustedDeviceDenied, "")
		}
	}()
	state, user, err := s.trustedDevicePrincipal(ctx, p)
	if err != nil {
		return TrustedDeviceCredential{}, err
	}
	if !validTrustedDeviceMetadata(metadata) {
		return TrustedDeviceCredential{}, fmt.Errorf("%w: trusted device metadata", ErrInvalidState)
	}
	if replaceID != "" && !validTrustedDeviceToken(replaceID) {
		return TrustedDeviceCredential{}, ErrTrustedDeviceUntrusted
	}
	if afterAuthentication {
		if !recentlyPasswordAuthenticated(state.Sessions[p.SessionHash], s.now()) {
			return TrustedDeviceCredential{}, ErrForbidden
		}
	} else if err := s.verifyCurrentPassword(ctx, p, state, currentPassword, ""); err != nil {
		return TrustedDeviceCredential{}, err
	}
	id, err := randomToken(s.random)
	if err != nil {
		return TrustedDeviceCredential{}, err
	}
	secret, err := randomToken(s.random)
	if err != nil {
		return TrustedDeviceCredential{}, err
	}
	now := s.now().UTC()
	device := TrustedDevice{ID: id, UserID: user.ID, UserRevision: user.Revision, SecretHash: hashToken(secret), Metadata: metadata, CreatedAt: now, ExpiresAt: now.Add(TrustedDeviceTTL), AutomaticSignIn: true}
	kind := AuditTrustedDeviceEnroll
	if replaceID != "" {
		kind = AuditTrustedDeviceReplace
	}
	err = s.apply(ctx, kind, user.ID, id, "", func(next *State) error {
		now := s.now().UTC()
		if err := validatePrincipalState(*next, p, now); err != nil {
			return err
		}
		current := next.Users[user.ID]
		if current.Revision != user.Revision || current.PasswordPHC != user.PasswordPHC || next.RecoveryPHC != "" {
			return ErrUnauthorized
		}
		session := next.Sessions[p.SessionHash]
		if afterAuthentication && !recentlyPasswordAuthenticated(session, now) {
			return ErrForbidden
		}
		if now.Before(device.CreatedAt) || !now.Before(device.CreatedAt.Add(trustedDeviceEnrollmentLifetime)) || !device.ExpiresAt.After(now) {
			return ErrUnauthorized
		}
		if _, exists := next.TrustedDevices[id]; exists || id == replaceID {
			return ErrConflict
		}
		if replaceID != "" {
			previous, exists := next.TrustedDevices[replaceID]
			if !exists || previous.UserID != user.ID {
				return ErrTrustedDeviceUntrusted
			}
			revokeTrustedDevice(next, replaceID)
		}
		pruneTrustedDevices(next, now)
		if len(next.TrustedDevices) >= MaxTrustedDevices {
			return ErrRateLimited
		}
		if next.TrustedDevices == nil {
			next.TrustedDevices = map[string]TrustedDevice{}
		}
		next.TrustedDevices[id] = device
		session.DeviceID = id
		next.Sessions[p.SessionHash] = session
		return nil
	})
	if err != nil {
		return TrustedDeviceCredential{}, err
	}
	return TrustedDeviceCredential{ID: id, Secret: secret, ExpiresAt: device.ExpiresAt}, nil
}

func authenticateTrustedDeviceState(state State, id, verifier string, now time.Time) (TrustedDevice, User, error) {
	device, exists := state.TrustedDevices[id]
	expected := hashToken("")
	if validDigest(device.SecretHash) {
		expected = device.SecretHash
	}
	verified := subtle.ConstantTimeCompare([]byte(verifier), []byte(expected)) == 1
	if !exists || !verified {
		return TrustedDevice{}, User{}, ErrTrustedDeviceUntrusted
	}
	user, err := trustedDeviceUser(state, id, device)
	if err != nil || now.Before(device.CreatedAt) || device.ActivatedAt.After(now) || device.LastUsedAt.After(now) {
		return TrustedDevice{}, User{}, ErrTrustedDeviceUntrusted
	}
	if device.ActivatedAt.IsZero() {
		return TrustedDevice{}, User{}, ErrTrustedDeviceUntrusted
	}
	if !device.ExpiresAt.After(now) {
		return TrustedDevice{}, User{}, ErrTrustedDeviceExpired
	}
	if !device.AutomaticSignIn {
		return TrustedDevice{}, User{}, ErrTrustedDeviceAutoSignInDisabled
	}
	return device, user, nil
}

func validateTrustedDeviceActivation(state State, p Principal, id string, now time.Time) error {
	if p.Method != MethodLocal || p.Provider != "" {
		return ErrForbidden
	}
	if p.DeviceID != id {
		return ErrUnauthorized
	}
	if err := validatePrincipalState(state, p, now); err != nil {
		return err
	}
	if err := p.Authorize(PermissionSecurityWrite); err != nil {
		return err
	}
	device, exists := state.TrustedDevices[id]
	if !exists || device.UserID != p.UserID || device.UserRevision != state.Sessions[p.SessionHash].UserRevision || !device.ActivatedAt.IsZero() || !device.LastUsedAt.IsZero() || now.Before(device.CreatedAt) || !now.Before(device.CreatedAt.Add(trustedDeviceEnrollmentLifetime)) || !device.ExpiresAt.After(now) {
		return ErrTrustedDeviceUntrusted
	}
	return nil
}

func (s *Service) ActivateTrustedDevice(ctx context.Context, p Principal, id string) (err error) {
	defer func() {
		if err != nil {
			s.RecordDenial(AuditTrustedDeviceDenied, "")
		}
	}()
	if err := s.check(); err != nil {
		return err
	}
	if !validTrustedDeviceToken(id) {
		return ErrTrustedDeviceUntrusted
	}
	state, err := s.repo.Load(ctx)
	if err != nil {
		return err
	}
	if err := validateTrustedDeviceActivation(state, p, id, s.now()); err != nil {
		return err
	}
	return s.apply(ctx, AuditTrustedDeviceActivate, p.UserID, id, "", func(next *State) error {
		now := s.now().UTC()
		if err := validateTrustedDeviceActivation(*next, p, id, now); err != nil {
			return err
		}
		device := next.TrustedDevices[id]
		device.ActivatedAt = now
		next.TrustedDevices[id] = device
		return nil
	})
}

func (s *Service) InspectTrustedDevice(ctx context.Context, id, secret string) (TrustedDeviceView, error) {
	if err := s.check(); err != nil {
		return TrustedDeviceView{}, err
	}
	if err := ctx.Err(); err != nil {
		return TrustedDeviceView{}, err
	}
	if !validTrustedDeviceToken(id) || !validTrustedDeviceToken(secret) {
		return TrustedDeviceView{}, ErrTrustedDeviceUntrusted
	}
	state, err := s.repo.Load(ctx)
	if err != nil {
		return TrustedDeviceView{}, err
	}
	if _, exists := state.TrustedDevices[id]; !exists {
		return TrustedDeviceView{}, ErrTrustedDeviceNotFound
	}
	if _, _, err := authenticateTrustedDeviceState(state, id, hashToken(secret), s.now()); err != nil {
		if errors.Is(err, ErrTrustedDeviceExpired) {
			return state.TrustedDevices[id].Redacted(), err
		}
		if !errors.Is(err, ErrTrustedDeviceAutoSignInDisabled) {
			return TrustedDeviceView{}, err
		}
	}
	return state.TrustedDevices[id].Redacted(), nil
}

func (s *Service) AuthenticateTrustedDevice(ctx context.Context, id, secret, source string) (credentials Credentials, err error) {
	defer func() {
		if err != nil {
			s.RecordDenial(AuditTrustedDeviceDenied, source)
		}
	}()
	if err := s.check(); err != nil {
		return Credentials{}, err
	}
	if !validTrustedDeviceToken(id) || !validTrustedDeviceToken(secret) {
		return Credentials{}, ErrTrustedDeviceUntrusted
	}
	if ok, retry := s.allow(source, "trusted-device"); !ok {
		return Credentials{}, &RateLimitError{RetryAfter: retry}
	}
	state, err := s.repo.Load(ctx)
	if err != nil {
		return Credentials{}, err
	}
	verifier := hashToken(secret)
	_, user, err := authenticateTrustedDeviceState(state, id, verifier, s.now())
	if err != nil {
		return Credentials{}, err
	}
	credentials, session, err := s.newSession(user, MethodLocal, "")
	if err != nil {
		return Credentials{}, err
	}
	session.DeviceID = id
	session.Source = authenticationSource(source)
	session.ExpiresAt = session.IssuedAt.Add(trustedDeviceSessionTTL)
	credentials.Principal = principalFor(user, session)
	credentials.ExpiresAt = session.ExpiresAt
	err = s.apply(ctx, AuditTrustedDeviceLogin, user.ID, id, source, func(next *State) error {
		now := s.now().UTC()
		device, current, err := authenticateTrustedDeviceState(*next, id, verifier, now)
		if err != nil {
			return err
		}
		if current.ID != user.ID || current.Revision != user.Revision || now.Before(session.IssuedAt) || !session.ExpiresAt.After(now) {
			return ErrTrustedDeviceUntrusted
		}
		device.LastUsedAt = now
		next.TrustedDevices[id] = device
		pruneSessions(next, now)
		next.Sessions[session.TokenHash] = session
		return nil
	})
	if err != nil {
		return Credentials{}, err
	}
	return credentials, nil
}

func (s *Service) ListTrustedDevices(ctx context.Context, p Principal) ([]TrustedDeviceView, error) {
	state, _, err := s.trustedDevicePrincipal(ctx, p)
	if err != nil {
		return nil, err
	}
	devices := make([]TrustedDeviceView, 0, len(state.TrustedDevices))
	for _, device := range state.TrustedDevices {
		if p.UserID != BreakGlassID && device.UserID != p.UserID {
			continue
		}
		view := device.Redacted()
		view.Current = device.ID == p.DeviceID
		devices = append(devices, view)
	}
	sort.Slice(devices, func(i, j int) bool {
		if devices[i].CreatedAt.Equal(devices[j].CreatedAt) {
			return devices[i].ID < devices[j].ID
		}
		return devices[i].CreatedAt.After(devices[j].CreatedAt)
	})
	return devices, nil
}

func (s *Service) RevokeTrustedDevice(ctx context.Context, p Principal, currentPassword, id, source string) (err error) {
	defer func() {
		if err != nil {
			s.RecordDenial(AuditTrustedDeviceDenied, source)
		}
	}()
	state, user, err := s.trustedDevicePrincipal(ctx, p)
	if err != nil {
		return err
	}
	device, exists := state.TrustedDevices[id]
	if !validTrustedDeviceToken(id) || !exists || user.ID != BreakGlassID && device.UserID != user.ID {
		return ErrTrustedDeviceUntrusted
	}
	if err := s.verifyCurrentPassword(ctx, p, state, currentPassword, source); err != nil {
		return err
	}
	err = s.apply(ctx, AuditTrustedDeviceRevoke, user.ID, device.ID, source, func(next *State) error {
		if err := validatePrincipalState(*next, p, s.now()); err != nil {
			return err
		}
		current := next.Users[user.ID]
		device, exists := next.TrustedDevices[id]
		if current.Revision != user.Revision || current.PasswordPHC != user.PasswordPHC || !exists || user.ID != BreakGlassID && device.UserID != user.ID {
			return ErrTrustedDeviceUntrusted
		}
		revokeTrustedDevice(next, id)
		return nil
	})
	return err
}

func (s *Service) RevokeAllTrustedDevices(ctx context.Context, p Principal, currentPassword, source string) (err error) {
	defer func() {
		if err != nil {
			s.RecordDenial(AuditTrustedDeviceDenied, source)
		}
	}()
	state, user, err := s.trustedDevicePrincipal(ctx, p)
	if err != nil {
		return err
	}
	if err := s.verifyCurrentPassword(ctx, p, state, currentPassword, source); err != nil {
		return err
	}
	target := user.ID
	if user.ID == BreakGlassID {
		target = "all-trusted-devices"
	}
	err = s.apply(ctx, AuditTrustedDeviceRevokeAll, user.ID, target, source, func(next *State) error {
		if err := validatePrincipalState(*next, p, s.now()); err != nil {
			return err
		}
		current := next.Users[user.ID]
		if current.Revision != user.Revision || current.PasswordPHC != user.PasswordPHC {
			return ErrUnauthorized
		}
		if user.ID == BreakGlassID {
			revokeAllTrustedDevices(next)
		} else {
			revokeUserTrustedDevices(next, user.ID)
		}
		return nil
	})
	return err
}

func (s *Service) SetTrustedDeviceAutoSignIn(ctx context.Context, p Principal, currentPassword, id string, enabled bool, source string) (err error) {
	defer func() {
		if err != nil {
			s.RecordDenial(AuditTrustedDeviceDenied, source)
		}
	}()
	state, user, err := s.trustedDevicePrincipal(ctx, p)
	if err != nil {
		return err
	}
	device, exists := state.TrustedDevices[id]
	if !validTrustedDeviceToken(id) || !exists || device.UserID != user.ID {
		return ErrTrustedDeviceUntrusted
	}
	if enabled {
		if err := s.verifyCurrentPassword(ctx, p, state, currentPassword, source); err != nil {
			return err
		}
	}
	return s.apply(ctx, AuditTrustedDeviceAutoSignIn, user.ID, id, source, func(next *State) error {
		if err := validatePrincipalState(*next, p, s.now()); err != nil {
			return err
		}
		current := next.Users[user.ID]
		device, exists := next.TrustedDevices[id]
		if current.Revision != user.Revision || current.PasswordPHC != user.PasswordPHC || !exists || device.UserID != user.ID {
			return ErrTrustedDeviceUntrusted
		}
		device.AutomaticSignIn = enabled
		next.TrustedDevices[id] = device
		return nil
	})
}

func (s *Service) DiscardTrustedDevice(ctx context.Context, p Principal, id string) (err error) {
	defer func() {
		if err != nil {
			s.RecordDenial(AuditTrustedDeviceDenied, "")
		}
	}()
	if !validTrustedDeviceToken(id) {
		return ErrTrustedDeviceUntrusted
	}
	if p.DeviceID != id {
		return ErrUnauthorized
	}
	state, user, err := s.trustedDevicePrincipal(ctx, p)
	if err != nil {
		return err
	}
	device, exists := state.TrustedDevices[id]
	if !exists || device.UserID != user.ID {
		return ErrTrustedDeviceUntrusted
	}
	err = s.apply(ctx, AuditTrustedDeviceDiscard, user.ID, device.ID, "", func(next *State) error {
		if err := validatePrincipalState(*next, p, s.now()); err != nil {
			return err
		}
		device, exists := next.TrustedDevices[id]
		if !exists || device.UserID != user.ID {
			return ErrTrustedDeviceUntrusted
		}
		if device.LastUsedAt.IsZero() {
			session := next.Sessions[p.SessionHash]
			session.DeviceID = ""
			next.Sessions[p.SessionHash] = session
		}
		revokeTrustedDevice(next, id)
		return nil
	})
	return err
}

func revokeTrustedDevice(state *State, id string) {
	delete(state.TrustedDevices, id)
	for hash, session := range state.Sessions {
		if session.DeviceID == id {
			delete(state.Sessions, hash)
		}
	}
}

func revokeUserTrustedDevices(state *State, userID string) {
	for id, device := range state.TrustedDevices {
		if device.UserID == userID {
			revokeTrustedDevice(state, id)
		}
	}
}

func revokeAllTrustedDevices(state *State) {
	state.TrustedDevices = map[string]TrustedDevice{}
	for hash, session := range state.Sessions {
		if session.DeviceID != "" {
			delete(state.Sessions, hash)
		}
	}
}

func pruneTrustedDevices(state *State, now time.Time) {
	for id, device := range state.TrustedDevices {
		if !device.ExpiresAt.After(now) || device.ActivatedAt.IsZero() && !now.Before(device.CreatedAt.Add(trustedDeviceEnrollmentLifetime)) {
			revokeTrustedDevice(state, id)
		}
	}
}

func equalTrustedDevices(a, b map[string]TrustedDevice) bool {
	if len(a) != len(b) {
		return false
	}
	for id, device := range a {
		other, ok := b[id]
		if !ok || device.ID != other.ID || device.UserID != other.UserID || device.UserRevision != other.UserRevision || device.SecretHash != other.SecretHash || device.Metadata != other.Metadata || device.AutomaticSignIn != other.AutomaticSignIn || !device.CreatedAt.Equal(other.CreatedAt) || !device.ExpiresAt.Equal(other.ExpiresAt) || !device.LastUsedAt.Equal(other.LastUsedAt) || !device.ActivatedAt.Equal(other.ActivatedAt) {
			return false
		}
	}
	return true
}
