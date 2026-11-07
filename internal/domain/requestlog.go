package domain

import "time"

// AttemptLog records one upstream attempt during ordered combo fallback.
type AttemptLog struct {
	Model           string `json:"model"`
	UpstreamModelID string `json:"upstreamModelId,omitempty"`
	Order           int    `json:"order,omitempty"`
	Group           int    `json:"group"`
	Upstream        string `json:"upstream,omitempty"`
	Status          int    `json:"status,omitempty"`
	Outcome         string `json:"outcome"`
	DispatchOutcome string `json:"dispatchOutcome,omitempty"`
	Reason          string `json:"reason,omitempty"`
	DurationMs      int64  `json:"durationMs,omitempty"`
	Source          string `json:"source,omitempty"`
	Provider        string `json:"provider,omitempty"`
	ProviderName    string `json:"providerName,omitempty"`
	CredentialID    string `json:"credentialId,omitempty"`
	EgressID        string `json:"egressId,omitempty"`
}

// PlannerExclusionLog records why one provider-scoped offering was omitted
// from the immutable request plan.
type PlannerExclusionLog struct {
	ProviderID string `json:"providerId"`
	Model      string `json:"model"`
	Reason     string `json:"reason"`
}

// RequestLog is the persisted attribution record for one proxied request.
type RequestLog struct {
	ID           string    `json:"id"`
	StartedAt    time.Time `json:"startedAt"`
	FinishedAt   time.Time `json:"finishedAt"`
	AccountID    string    `json:"accountId"`
	AccountEmail string    `json:"accountEmail"`
	Protocol     string    `json:"protocol"`
	// Upstream is "cline", "opencode", or "provider".
	Upstream     string `json:"upstream,omitempty"`
	ProviderName string `json:"providerName,omitempty"`
	// ProviderID 是实际服务方自定义 provider 的 ID（仅 Upstream=="provider" 时归因，
	// 供月度 token 配额按 provider 归集）。
	ProviderID string `json:"providerId,omitempty"`
	// BudgetAttemptID is the durable custom-provider admission receipt used to
	// make final token accounting idempotent. It contains no credential data.
	BudgetAttemptID string `json:"budgetAttemptId,omitempty"`
	// ConnectionID and ConnectionEmail identify the serving login connection.
	ConnectionID    string `json:"connectionId,omitempty"`
	ConnectionEmail string `json:"connectionEmail,omitempty"`
	// Model is the effective model; RequestedModel keeps the original combo ID.
	Model          string  `json:"model"`
	Stream         bool    `json:"stream"`
	InputTokens    int64   `json:"inputTokens"`
	OutputTokens   int64   `json:"outputTokens"`
	CachedTokens   int64   `json:"cachedTokens"`
	TotalTokens    int64   `json:"totalTokens"`
	UsageAvailable bool    `json:"usageAvailable"`
	DurationMs     int64   `json:"durationMs"`
	TTFTMs         int64   `json:"ttftMs"`
	OutputTPS      float64 `json:"outputTokensPerSecond"`
	Completed      bool    `json:"completed"`
	Error          string  `json:"error,omitempty"`
	// Combo attribution fields.
	RequestedModel    string                `json:"requestedModel,omitempty"`
	ComboID           string                `json:"comboId,omitempty"`
	ComboName         string                `json:"comboName,omitempty"`
	FallbackUsed      bool                  `json:"fallbackUsed,omitempty"`
	FallbackCount     int                   `json:"fallbackCount,omitempty"`
	Attempts          []AttemptLog          `json:"attempts,omitempty"`
	PlannerExclusions []PlannerExclusionLog `json:"plannerExclusions,omitempty"`
	// ToolNames maps shortened Cline tool names back to originals. It is
	// intentionally excluded from JSON, matching the former unexported field.
	ToolNames map[string]string `json:"-"`
}
