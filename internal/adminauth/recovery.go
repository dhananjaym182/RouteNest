package adminauth

import (
	"context"
	"fmt"
	"time"
)

// RecoverOffline is for the pre-server local recovery command only. The host must
// verify controlling TTY/native recovery, effective ownership and secure data-dir
// permissions, and open the exclusively locked repository before invoking it.
// There is deliberately no Service recovery method for HTTP handlers. The
// initialized bit remains permanent, so bootstrap environment values stay ignored.
func RecoverOffline(ctx context.Context, repo Repository, opts Options) error {
	if repo == nil || opts.DeliverBootstrap == nil {
		return fmt.Errorf("offline recovery requires exclusive local authority and a private delivery channel")
	}
	st, e := repo.Load(ctx)
	if e != nil {
		return e
	}
	if !st.Initialized {
		return ErrNotInitialized
	}
	if opts.PasswordParams.Memory == 0 {
		opts.PasswordParams = DefaultPasswordParams
	}
	h, e := NewPasswordHasher(opts.PasswordParams, opts.Random)
	if e != nil {
		return e
	}
	secret, e := randomToken(h.random)
	if e != nil {
		return e
	}
	phc, e := h.Hash(ctx, secret)
	if e != nil {
		return e
	}
	id, e := randomToken(h.random)
	if e != nil {
		return e
	}
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	event := AuditEvent{ID: id, Kind: AuditOfflineRecovery, ActorID: BreakGlassID, TargetID: BreakGlassID, Outcome: "success", At: now().UTC()}
	e = repo.Apply(ctx, event, func(next *State) error {
		if !next.Initialized {
			return ErrNotInitialized
		}
		u, ok := next.Users[BreakGlassID]
		if !ok {
			return ErrInvalidState
		}
		u.Revision++
		u.PasswordPHC = ""
		u.Legacy = nil
		u.Disabled = false
		u.Roles = []Role{RoleBreakGlass, RoleSecurityAdmin}
		next.Users[BreakGlassID] = u
		next.RecoveryPHC = phc
		next.RecoveryCodePHC = ""
		next.Sessions = map[string]Session{}
		return ValidateState(*next)
	})
	if e != nil {
		return e
	}
	return opts.DeliverBootstrap(secret)
}
