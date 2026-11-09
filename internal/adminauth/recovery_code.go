package adminauth

import (
	"context"
	"errors"
)

// RecoveryCodeStatus exposes only whether a durable recovery credential exists.
// The verifier and credential are never returned by status APIs.
func (s *Service) RecoveryCodeStatus(ctx context.Context) (bool, error) {
	if err := s.check(); err != nil {
		return false, err
	}
	state, err := s.repo.Load(ctx)
	if err != nil {
		return false, err
	}
	return state.Initialized && state.RecoveryPHC == "" && state.RecoveryCodePHC != "", nil
}

// IssueRecoveryCode rotates the recovery credential for the reserved local
// administrator. It requires the current password even for an authenticated
// break-glass session so a stolen session alone cannot replace recovery proof.
func (s *Service) IssueRecoveryCode(ctx context.Context, principal Principal, currentPassword, source string) (code string, err error) {
	defer func() {
		if err != nil && (errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrForbidden) || errors.Is(err, ErrRateLimited)) {
			s.RecordDenial(AuditRecoveryCodeDenied, source)
		}
	}()
	if principal.UserID != BreakGlassID || principal.Method != MethodLocal {
		return "", ErrForbidden
	}
	if err := s.validatePrincipal(ctx, principal); err != nil {
		return "", err
	}
	if err := principal.Authorize(PermissionSecurityWrite); err != nil {
		return "", err
	}
	state, err := s.repo.Load(ctx)
	if err != nil {
		return "", err
	}
	user, ok := state.Users[BreakGlassID]
	if !ok || user.PasswordPHC == "" || user.Legacy != nil || state.RecoveryPHC != "" {
		return "", ErrForbidden
	}
	verified, err := s.hasher.Verify(ctx, user.PasswordPHC, currentPassword)
	if err != nil || !verified {
		return "", ErrUnauthorized
	}
	code, err = randomToken(s.random)
	if err != nil {
		return "", err
	}
	verifier, err := s.hasher.hash(ctx, code)
	if err != nil {
		return "", err
	}
	err = s.apply(ctx, AuditRecoveryCodeIssue, principal.UserID, BreakGlassID, source, func(next *State) error {
		if err := validatePrincipalState(*next, principal, s.now()); err != nil {
			return err
		}
		current, ok := next.Users[BreakGlassID]
		if !ok || current.Revision != user.Revision || current.PasswordPHC != user.PasswordPHC || next.RecoveryPHC != "" {
			return ErrConflict
		}
		next.RecoveryCodePHC = verifier
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrMutationPrepared) {
			return code, err
		}
		return "", err
	}
	return code, nil
}

// RecoverWithCode consumes the current recovery credential, changes the local
// break-glass password, rotates recovery proof, and revokes every administrator
// session in one durable mutation. Concurrent or replayed credentials fail.
func (s *Service) RecoverWithCode(ctx context.Context, code, newPassword, source string) (replacementCode string, err error) {
	defer func() {
		if err != nil && (errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrRateLimited)) {
			s.RecordDenial(AuditRecoveryCodeDenied, source)
		}
	}()
	if ok, retry := s.allow(source, "recovery"); !ok {
		return "", &RateLimitError{RetryAfter: retry}
	}
	if err = s.check(); err != nil {
		return "", err
	}
	state, err := s.repo.Load(ctx)
	if err != nil {
		return "", err
	}
	if !state.Initialized || state.RecoveryPHC != "" || state.RecoveryCodePHC == "" {
		return "", ErrUnauthorized
	}
	verified, verifyErr := s.hasher.Verify(ctx, state.RecoveryCodePHC, code)
	if verifyErr != nil || !verified {
		return "", ErrUnauthorized
	}
	passwordPHC, err := s.hasher.Hash(ctx, newPassword)
	if err != nil {
		return "", err
	}
	replacementCode, err = randomToken(s.random)
	if err != nil {
		return "", err
	}
	replacementPHC, err := s.hasher.hash(ctx, replacementCode)
	if err != nil {
		return "", err
	}
	expectedVerifier := state.RecoveryCodePHC
	expectedUser := state.Users[BreakGlassID]
	err = s.apply(ctx, AuditRecoveryCodeUse, BreakGlassID, BreakGlassID, source, func(next *State) error {
		user, ok := next.Users[BreakGlassID]
		if !ok || !next.Initialized || next.RecoveryPHC != "" || next.RecoveryCodePHC != expectedVerifier || user.Revision != expectedUser.Revision || user.PasswordPHC != expectedUser.PasswordPHC {
			return ErrUnauthorized
		}
		user.PasswordPHC = passwordPHC
		user.Legacy = nil
		user.Disabled = false
		user.Revision++
		next.Users[BreakGlassID] = user
		next.RecoveryCodePHC = replacementPHC
		next.Sessions = map[string]Session{}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrMutationPrepared) {
			return replacementCode, err
		}
		return "", err
	}
	s.flows.Clear()
	return replacementCode, nil
}
