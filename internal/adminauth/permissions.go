package adminauth

import (
	"fmt"
	"strings"
	"time"
)

const (
	RoleViewer                 Role       = "viewer"
	RoleProviderAdmin          Role       = "provider_admin"
	RoleCredentialAdmin        Role       = "credential_admin"
	RoleSecurityAdmin          Role       = "security_admin"
	RoleBreakGlass             Role       = "break_glass"
	PermissionView             Permission = "admin.view"
	PermissionProviderWrite    Permission = "provider.write"
	PermissionCredentialWrite  Permission = "credential.write"
	PermissionCredentialExport Permission = "credential.export"
	PermissionSecurityWrite    Permission = "security.write"
	PermissionDangerousExecute Permission = "dangerous.execute"
)

var rolePermissions = map[Role][]Permission{
	RoleViewer:          {PermissionView},
	RoleProviderAdmin:   {PermissionView, PermissionProviderWrite, PermissionDangerousExecute},
	RoleCredentialAdmin: {PermissionView, PermissionCredentialWrite, PermissionCredentialExport, PermissionDangerousExecute},
	RoleSecurityAdmin:   {PermissionView, PermissionSecurityWrite},
	RoleBreakGlass:      {PermissionView, PermissionProviderWrite, PermissionCredentialWrite, PermissionCredentialExport, PermissionSecurityWrite, PermissionDangerousExecute},
}

func ValidPermission(p Permission) bool {
	for _, ps := range rolePermissions {
		for _, v := range ps {
			if p == v {
				return true
			}
		}
	}
	return false
}
func ValidRole(r Role) bool { _, ok := rolePermissions[r]; return ok }
func (p Principal) HasPermission(want Permission) bool {
	if !ValidPermission(want) {
		return false
	}
	for _, role := range p.Roles {
		if role == RoleBreakGlass && p.Method != MethodLocal {
			continue
		}
		for _, v := range rolePermissions[role] {
			if v == want {
				return true
			}
		}
	}
	return false
}
func (p Principal) Authorize(want Permission) error {
	if !p.HasPermission(want) {
		return ErrForbidden
	}
	return nil
}
func NormalizeUsername(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
func ValidateState(s State) error {
	if s.Version != StateVersion || s.Users == nil || s.Sessions == nil || len(s.Users) > 1024 || len(s.Sessions) > 4096 || len(s.Providers) > 128 {
		return ErrInvalidState
	}
	seen := map[string]bool{}
	securityAdmins := 0
	for id, u := range s.Users {
		if id == "" || len(id) > 128 || id != u.ID || u.Username == "" || len(u.Username) > 128 || NormalizeUsername(u.Username) != u.Username || seen[u.Username] || u.Revision == 0 {
			return fmt.Errorf("%w: user", ErrInvalidState)
		}
		seen[u.Username] = true
		if len(u.Roles) == 0 {
			return fmt.Errorf("%w: role missing", ErrInvalidState)
		}
		roles := map[Role]bool{}
		for _, r := range u.Roles {
			if !ValidRole(r) || roles[r] {
				return fmt.Errorf("%w: role", ErrInvalidState)
			}
			roles[r] = true
		}
		if id != BreakGlassID && roles[RoleBreakGlass] {
			return fmt.Errorf("%w: reserved role", ErrInvalidState)
		}
		if u.Provider != "" && (u.PasswordPHC != "" || u.Legacy != nil || id == BreakGlassID) {
			return fmt.Errorf("%w: federated local credential", ErrInvalidState)
		}
		if u.PasswordPHC != "" {
			if _, e := ParsePHC(u.PasswordPHC); e != nil {
				return fmt.Errorf("%w: password hash", ErrInvalidState)
			}
		}
		if u.Legacy != nil && !ValidLegacy(*u.Legacy) {
			return fmt.Errorf("%w: legacy credential", ErrInvalidState)
		}
		if !u.Disabled && (roles[RoleSecurityAdmin] || roles[RoleBreakGlass]) {
			securityAdmins++
		}
	}
	if s.Initialized {
		u, ok := s.Users[BreakGlassID]
		if !ok || u.Username != BreakGlassUsername || u.Disabled || u.Provider != "" || (!hasRole(u.Roles, RoleBreakGlass)) || (!hasRole(u.Roles, RoleSecurityAdmin)) || securityAdmins == 0 || s.BootstrapPHC != "" || (u.PasswordPHC == "" && u.Legacy == nil && s.RecoveryPHC == "") {
			return fmt.Errorf("%w: break-glass invariant", ErrInvalidState)
		}
	} else {
		if s.RecoveryPHC != "" || s.RecoveryCodePHC != "" {
			return ErrInvalidState
		}
		if len(s.Sessions) > 0 {
			return fmt.Errorf("%w: sessions before bootstrap", ErrInvalidState)
		}
		if s.BootstrapPHC != "" {
			if _, e := ParsePHC(s.BootstrapPHC); e != nil {
				return fmt.Errorf("%w: bootstrap hash", ErrInvalidState)
			}
		}
	}
	if s.RecoveryPHC != "" {
		if !s.Initialized || len(s.Sessions) > 0 {
			return ErrInvalidState
		}
		if _, e := ParsePHC(s.RecoveryPHC); e != nil {
			return ErrInvalidState
		}
	}
	if s.RecoveryCodePHC != "" {
		if !s.Initialized || s.RecoveryPHC != "" {
			return ErrInvalidState
		}
		if _, e := ParsePHC(s.RecoveryCodePHC); e != nil {
			return ErrInvalidState
		}
	}
	for id, p := range s.Providers {
		if id == "" || id != p.ID || len(id) > 128 || p.Revision == 0 || (p.Type != MethodOIDC && p.Type != MethodSAML) {
			return ErrInvalidState
		}
	}
	for h, v := range s.Sessions {
		u, ok := s.Users[v.UserID]
		if !ok || h != v.TokenHash || !validDigest(h) || !validDigest(v.CSRFHash) || v.UserRevision == 0 || !v.ExpiresAt.After(v.IssuedAt) || v.ExpiresAt.Sub(v.IssuedAt) > 24*time.Hour || v.UserRevision > u.Revision || !validMethod(v.Method) || (v.UserID == BreakGlassID && v.Method != MethodLocal) {
			return fmt.Errorf("%w: session", ErrInvalidState)
		}
		if v.Method == MethodLocal && (v.Provider != "" || u.Provider != "") {
			return ErrInvalidState
		}
		if v.Method != MethodLocal && (v.Provider == "" || v.Provider != u.Provider) {
			return ErrInvalidState
		}
	}
	return nil
}
func hasRole(rs []Role, r Role) bool {
	for _, v := range rs {
		if v == r {
			return true
		}
	}
	return false
}
func validMethod(m AuthMethod) bool { return m == MethodLocal || m == MethodOIDC || m == MethodSAML }
