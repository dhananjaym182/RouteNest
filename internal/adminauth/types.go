// Package adminauth owns administrator identity. Local repositories are single-node
// authorities; a missing, corrupt, or degraded authority never grants access.
package adminauth

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrUnauthorized       = errors.New("administrator authentication failed")
	ErrForbidden          = errors.New("administrator permission denied")
	ErrNotInitialized     = errors.New("administrator bootstrap required")
	ErrAlreadyInitialized = errors.New("administrator authentication already initialized")
	ErrDegraded           = errors.New("administrator authentication is locked down")
	ErrInvalidState       = errors.New("invalid administrator security state")
	ErrRateLimited        = errors.New("administrator authentication rate limited")
	ErrReplay             = errors.New("authentication flow missing, expired, or consumed")
	ErrConflict           = errors.New("administrator security state changed concurrently")
	ErrMutationPrepared   = errors.New("administrator mutation is durably prepared and requires restart")
)

const BreakGlassID = "break-glass"
const BreakGlassUsername = "admin"
const StateVersion = 1
const LegacyMigrationSentinel = "!routenest-admin-auth-migrated!"

type AuthMethod string

const (
	MethodLocal AuthMethod = "local"
	MethodOIDC  AuthMethod = "oidc"
	MethodSAML  AuthMethod = "saml"
)

type Permission string
type Role string

type LegacyCredential struct {
	Salt string `json:"salt"`
	Hash string `json:"hash"`
}
type User struct {
	ID          string            `json:"id"`
	Username    string            `json:"username"`
	PasswordPHC string            `json:"passwordPHC,omitempty"`
	Legacy      *LegacyCredential `json:"legacy,omitempty"`
	Roles       []Role            `json:"roles"`
	Disabled    bool              `json:"disabled"`
	Revision    uint64            `json:"revision"`
	Provider    string            `json:"provider,omitempty"`
	Subject     string            `json:"subject,omitempty"`
	CreatedAt   time.Time         `json:"createdAt"`
}
type Session struct {
	TokenHash    string     `json:"tokenHash"`
	CSRFHash     string     `json:"csrfHash"`
	UserID       string     `json:"userID"`
	UserRevision uint64     `json:"userRevision"`
	Method       AuthMethod `json:"method"`
	Provider     string     `json:"provider,omitempty"`
	DeviceID     string     `json:"deviceID,omitempty"`
	Source       string     `json:"source,omitempty"`
	IssuedAt     time.Time  `json:"issuedAt"`
	ExpiresAt    time.Time  `json:"expiresAt"`
}

// ProviderState contains public metadata and references, never resolved secrets.
type ProviderState struct {
	ID            string          `json:"id"`
	Type          AuthMethod      `json:"type"`
	Enabled       bool            `json:"enabled"`
	Revision      uint64          `json:"revision"`
	Configuration json.RawMessage `json:"configuration,omitempty"`
}
type State struct {
	Version         int                      `json:"version"`
	Revision        uint64                   `json:"revision"`
	Initialized     bool                     `json:"initialized"`
	RecoveryPHC     string                   `json:"recoveryPHC,omitempty"`
	RecoveryCodePHC string                   `json:"recoveryCodePHC,omitempty"`
	BootstrapPHC    string                   `json:"bootstrapPHC,omitempty"`
	Users           map[string]User          `json:"users"`
	Sessions        map[string]Session       `json:"sessions"`
	Providers       map[string]ProviderState `json:"providers,omitempty"`
	TrustedDevices  map[string]TrustedDevice `json:"trustedDevices,omitempty"`
}

func EmptyState() State {
	return State{Version: StateVersion, Users: map[string]User{}, Sessions: map[string]Session{}, Providers: map[string]ProviderState{}, TrustedDevices: map[string]TrustedDevice{}}
}
func CloneState(s State) State {
	b, _ := json.Marshal(s)
	var v State
	_ = json.Unmarshal(b, &v)
	if v.Providers == nil {
		v.Providers = map[string]ProviderState{}
	}
	if v.TrustedDevices == nil {
		v.TrustedDevices = map[string]TrustedDevice{}
	}
	return v
}

// Repository.Apply must atomically validate, journal, and persist the mutation and
// its audit event before returning success. Callbacks must have no external effects.
type Repository interface {
	Load(context.Context) (State, error)
	Apply(context.Context, AuditEvent, func(*State) error) error
	Close() error
}
type Principal struct {
	UserID      string     `json:"userID"`
	Username    string     `json:"username"`
	Roles       []Role     `json:"roles"`
	Method      AuthMethod `json:"method"`
	Provider    string     `json:"provider,omitempty"`
	DeviceID    string     `json:"deviceID,omitempty"`
	SessionHash string     `json:"-"`
	CSRFHash    string     `json:"-"`
	ExpiresAt   time.Time  `json:"expiresAt"`
}
type Credentials struct {
	Token     string    `json:"-"`
	CSRFToken string    `json:"csrfToken"`
	Principal Principal `json:"principal"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (Credentials) String() string      { return "[redacted administrator credentials]" }
func (LegacyCredential) String() string { return "[redacted legacy credential]" }
func (User) String() string             { return "[administrator user]" }
func (Session) String() string          { return "[redacted administrator session]" }

type Readiness struct {
	AuthStoreReady        bool                         `json:"authStoreReady"`
	AuditJournalReady     bool                         `json:"auditJournalReady"`
	BreakGlassReady       bool                         `json:"breakGlassReady"`
	TransportReady        bool                         `json:"transportReady"`
	RoutePolicyReady      bool                         `json:"routePolicyReady"`
	CSRFReady             bool                         `json:"csrfReady"`
	OIDCProviders         map[string]ProviderReadiness `json:"oidcProviders"`
	SAMLProviders         map[string]ProviderReadiness `json:"samlProviders"`
	ExternalAuditReady    bool                         `json:"externalAuditReady"`
	MultiReplicaSupported bool                         `json:"multiReplicaSupported"`
	AdminReady            bool                         `json:"adminReady"`
	Backend               string                       `json:"backend"`
	FlowBackend           string                       `json:"flowBackend"`
}
type ProviderReadiness struct {
	Ready  bool   `json:"ready"`
	Reason string `json:"reason,omitempty"`
}

func (r *Readiness) Recompute() {
	r.MultiReplicaSupported = false
	r.Backend = "local_file"
	r.FlowBackend = "memory"
	r.AdminReady = r.AuthStoreReady && r.AuditJournalReady && r.BreakGlassReady && r.TransportReady && r.RoutePolicyReady && r.CSRFReady
}
