// Package provideradapters defines the executable provider seam. Adapters own
// wire/auth transformations for one provider family; routing, cooldowns,
// budgets, persistence and HTTP responses remain with their callers.
package provideradapters

import (
	"context"
	"net/http"
	"time"

	"routenest/internal/domain"
	"routenest/internal/modelcatalog"
)

// Credential is the minimal redacted credential input needed by adapters.
// Global credential storage and lifecycle are introduced separately.
type Credential struct {
	ID       domain.CredentialID
	Token    string
	Email    string
	Name     string
	Headers  map[string]string
	Metadata map[string]string
}

// Invocation contains one prepared provider attempt. Adapters may use Params
// when their upstream protocol cannot consume an already encoded Body.
type Invocation struct {
	ProviderID domain.ProviderInstanceID
	HTTP       *http.Client
	BaseURL    string
	APIKey     string
	Headers    map[string]string
	Credential Credential
	Body       []byte
	Params     map[string]any
	Stream     bool
	Protocol   string
	ModelID    string
	SessionID  string
	SessionIDs []string
}

// Invoker is the one required adapter capability.
type Invoker interface {
	Invoke(context.Context, Invocation) (*http.Response, error)
}

// FailureInput is the bounded upstream result presented to an optional
// classifier. Body must already be size-limited by the caller.
type FailureInput struct {
	Scope      domain.AvailabilityScope
	StatusCode int
	Header     http.Header
	Body       []byte
}

type FailureClassifier interface {
	ClassifyFailure(FailureInput) []domain.AvailabilityObservation
}

type DiscoveryInput struct {
	HTTP    *http.Client
	BaseURL string
	APIKey  string
	Headers map[string]string
}

type ModelDiscoverer interface {
	DiscoverModels(context.Context, DiscoveryInput) ([]modelcatalog.Entry, error)
}

// QuotaSnapshot is an adapter-level report. Unknown values stay nil; callers
// must never convert absent provider data into a zero limit or reset time.
type QuotaSnapshot struct {
	Capability domain.CapabilityState
	Limit      *int64
	Used       *int64
	Remaining  *int64
	ResetAt    *time.Time
	Source     string
}

type QuotaReader interface {
	ReadQuota(context.Context, Credential) (QuotaSnapshot, error)
}

// Bundle groups one required invoker with independently optional capabilities.
type Bundle struct {
	ID                string
	Invoker           Invoker
	FailureClassifier FailureClassifier
	ModelDiscoverer   ModelDiscoverer
	QuotaReader       QuotaReader
}
