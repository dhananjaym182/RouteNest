package adminauth

import (
	"context"
	"net"
)

func authenticationSource(source string) string {
	if ip := net.ParseIP(source); ip != nil {
		return ip.String()
	}
	return ""
}

func (s *Service) verifyCurrentPassword(ctx context.Context, p Principal, state State, password, source string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.Method != MethodLocal || p.Provider != "" {
		return ErrForbidden
	}
	if err := validatePrincipalState(state, p, s.now()); err != nil {
		return err
	}
	user := state.Users[p.UserID]
	if user.Provider != "" || user.PasswordPHC == "" || user.Legacy != nil {
		return ErrForbidden
	}
	keys := []string{"reauthentication-session:" + p.SessionHash}
	storedSource := state.Sessions[p.SessionHash].Source
	if storedSource != "" {
		keys = append(keys, storedSource)
	}
	if currentSource := authenticationSource(source); currentSource != "" {
		keys = append(keys, currentSource)
	}
	if ok, retry := s.throttle.allow(keys...); !ok {
		return &RateLimitError{RetryAfter: retry}
	}
	verified, err := s.hasher.Verify(ctx, user.PasswordPHC, password)
	if err != nil {
		return err
	}
	if !verified {
		return ErrUnauthorized
	}
	return nil
}
