package domain

import "time"

// CustomProvider is a user-configured OpenAI-compatible upstream.
type CustomProvider struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	BaseURL    string            `json:"baseURL"`
	APIKey     string            `json:"apiKey"`
	ModelIDs   []string          `json:"modelIds"`
	Headers    map[string]string `json:"headers,omitempty"`
	Enabled    bool              `json:"enabled"`
	Priority   int               `json:"priority"`
	TimeoutSec int               `json:"timeoutSec,omitempty"`
	// ProxyURL overrides the egress for this provider: empty inherits the
	// global transport, "direct"/"none" bypasses every proxy, and any other
	// value must be a supported proxy URL.
	ProxyURL string `json:"proxyUrl,omitempty"`
	// Weight is used by the weighted_round_robin strategy. 0 means 1;
	// negative values exclude the provider from that strategy.
	Weight int `json:"weight,omitempty"`
	// Free is a provider-level flag used for statistics and fallback hints.
	// It is not a per-model access entitlement.
	Free bool `json:"free"`
	// ConnectionID binds one logged-in provider connection.
	ConnectionID string `json:"connectionId,omitempty"`
	// ConnectionIDs pools several connections for round-robin and failover.
	ConnectionIDs []string `json:"connectionIds,omitempty"`
	RpmLimit      int      `json:"rpmLimit,omitempty"`
	DailyLimit    int      `json:"dailyRequestLimit,omitempty"`
	// TokenLimit token 预算（如 10000000 = 1000 万免费额度 token），0 = 不启用。
	TokenLimit int64 `json:"tokenLimit,omitempty"`
	// TokenWindow 配额窗口："monthly"（默认，空值视同 monthly）或 "daily"。
	// 多数上游按日限额，少数按月发放免费 token 池，故窗口随 provider 配置。
	TokenWindow string    `json:"tokenWindow,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}
