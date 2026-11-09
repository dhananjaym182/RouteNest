package adminauth

import "time"

type AuditKind string

const (
	AuditBootstrapPending     AuditKind = "bootstrap.pending"
	AuditBootstrapComplete    AuditKind = "bootstrap.complete"
	AuditPasswordRotate       AuditKind = "password.rotate"
	AuditLegacyMigrate        AuditKind = "password.legacy_migrate"
	AuditLoginSuccess         AuditKind = "login.success"
	AuditLoginDenied          AuditKind = "login.denied"
	AuditSessionRevoke        AuditKind = "session.revoke"
	AuditLogout               AuditKind = "session.logout"
	AuditUserChange           AuditKind = "user.change"
	AuditRoleChange           AuditKind = "role.change"
	AuditSSOChange            AuditKind = "sso.change"
	AuditSSOLogin             AuditKind = "sso.login"
	AuditListenerChange       AuditKind = "listener.change"
	AuditRecoveryComplete     AuditKind = "offline.recovery_complete"
	AuditOfflineRecovery      AuditKind = "offline.recovery"
	AuditRecoveryCodeIssue    AuditKind = "recovery_code.issue"
	AuditRecoveryCodeUse      AuditKind = "recovery_code.use"
	AuditRecoveryCodeDenied   AuditKind = "recovery_code.denied"
	AuditAccessDenied         AuditKind = "access.denied"
	AuditCSRFDenied           AuditKind = "csrf.denied"
	AuditClientKeyCreate      AuditKind = "client_key.create"
	AuditClientKeyDelete      AuditKind = "client_key.delete"
	AuditClientKeyDeleteFinal AuditKind = "client_key.delete_final"
)

// AuditEvent deliberately has no free-form metadata or supplied messages. Passwords,
// token material, claims, keys and callback query strings cannot enter its schema.
type AuditEvent struct {
	ID       string    `json:"id"`
	Kind     AuditKind `json:"kind"`
	ActorID  string    `json:"actorID,omitempty"`
	TargetID string    `json:"targetID,omitempty"`
	Source   string    `json:"source,omitempty"`
	Outcome  string    `json:"outcome"`
	At       time.Time `json:"at"`
}

func ValidAuditKind(k AuditKind) bool {
	switch k {
	case AuditBootstrapPending, AuditBootstrapComplete, AuditPasswordRotate, AuditLegacyMigrate, AuditLoginSuccess, AuditLoginDenied, AuditSessionRevoke, AuditLogout, AuditUserChange, AuditRoleChange, AuditSSOChange, AuditSSOLogin, AuditListenerChange, AuditOfflineRecovery, AuditRecoveryComplete, AuditRecoveryCodeIssue, AuditRecoveryCodeUse, AuditRecoveryCodeDenied, AuditAccessDenied, AuditCSRFDenied, AuditClientKeyCreate, AuditClientKeyDelete, AuditClientKeyDeleteFinal:
		return true
	}
	return false
}
