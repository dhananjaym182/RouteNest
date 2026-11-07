package admin

import (
	"context"

	"routenest/internal/adminauth"
)

// hasAdminPassword describes the current durable break-glass credential. The
// legacy account-pool hash is only a migration input, never current authority.
func hasAdminPassword() bool {
	runtime, err := configuredAdminSecurity()
	if err != nil {
		return false
	}
	state, err := runtime.service.State(context.Background())
	if err != nil || !state.Initialized || state.RecoveryPHC != "" {
		return false
	}
	user, ok := state.Users[adminauth.BreakGlassID]
	return ok && !user.Disabled && (user.PasswordPHC != "" || user.Legacy != nil)
}
