package admin

import (
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/httpx"
	"routenest/internal/platform"
	"routenest/internal/providers"
	"routenest/internal/requestlog"
	onboardingstore "routenest/internal/store/onboarding"
	"routenest/internal/zen"
)

type onboardingVerification struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
}

type onboardingStatus struct {
	Stage                    string                 `json:"stage"`
	ProviderConfigured       bool                   `json:"providerConfigured"`
	ProviderReady            bool                   `json:"providerReady"`
	ProviderVerified         bool                   `json:"providerVerified"`
	ModelsAvailable          bool                   `json:"modelsAvailable"`
	ClientAPIKeyConfigured   bool                   `json:"clientAPIKeyConfigured"`
	SuccessfulGatewayRequest bool                   `json:"successfulGatewayRequest"`
	Verification             onboardingVerification `json:"verification,omitempty"`
}

var onboardingStateMu sync.Mutex
var onboardingPathResolver = func() string { return platform.ResolveDataPath(onboardingstore.FileName, nil) }

const onboardingZenVerificationKey = "opencode-zen"

func onboardingClineVerificationKey(accountID string) string {
	return "cline-account:" + strings.TrimSpace(accountID)
}

func onboardingStatePath() string {
	return onboardingPathResolver()
}

func loadOnboardingState() onboardingstore.State {
	onboardingStateMu.Lock()
	defer onboardingStateMu.Unlock()
	state, err := onboardingstore.Load(onboardingStatePath())
	if err != nil {
		platform.BackupCorruptFile(onboardingStatePath(), err)
		return onboardingstore.Empty()
	}
	return state
}

func markOnboardingProviderVerified(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	onboardingStateMu.Lock()
	defer onboardingStateMu.Unlock()
	path := onboardingStatePath()
	_, err := onboardingstore.Update(path, func(state *onboardingstore.State) bool {
		state.VerifiedProviders[id] = time.Now().UTC()
		return true
	})
	if err != nil {
		log.Printf("onboarding provider verification could not be persisted: %v", err)
	}
}

func clearOnboardingProviderVerification(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	onboardingStateMu.Lock()
	defer onboardingStateMu.Unlock()
	path := onboardingStatePath()
	_, err := onboardingstore.Update(path, func(state *onboardingstore.State) bool {
		if _, ok := state.VerifiedProviders[id]; !ok {
			return false
		}
		delete(state.VerifiedProviders, id)
		return true
	})
	if err != nil {
		log.Printf("onboarding provider verification could not be cleared: %v", err)
	}
}

func onboardingEndpointConfigured(raw string) bool {
	endpoint, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && (endpoint.Scheme == "http" || endpoint.Scheme == "https") && endpoint.Host != ""
}

func onboardingProviderVerified(state onboardingstore.State, id string) bool {
	verifiedAt, verified := state.VerifiedProviders[id]
	return verified && !verifiedAt.IsZero()
}

func currentOnboardingStatus() onboardingStatus {
	state := loadOnboardingState()
	status := onboardingStatus{Stage: "connect_provider"}

	pool := accounts.LoadPool()
	accounts.PoolMu.Lock()
	for _, account := range pool.Accounts {
		if account == nil {
			continue
		}
		status.ProviderConfigured = true
		if account.Status == "active" {
			status.ProviderReady = true
			key := onboardingClineVerificationKey(account.AccountID)
			if onboardingProviderVerified(state, key) {
				status.ProviderVerified = true
			} else if status.Verification.Kind == "" {
				status.Verification = onboardingVerification{Kind: "cline_account", ID: account.AccountID}
			}
		}
	}
	status.ClientAPIKeyConfigured = len(pool.Keys) > 0
	for _, model := range pool.Models {
		if !model.Disabled && !model.Delisted {
			status.ModelsAvailable = true
			break
		}
	}
	accounts.PoolMu.Unlock()

	for _, provider := range providers.List() {
		status.ProviderConfigured = true
		// Custom/local OpenAI-compatible providers may intentionally omit an API
		// key. The existing invocation and verification paths support that shape.
		ready := provider.Enabled && onboardingEndpointConfigured(provider.BaseURL) && len(provider.ModelIDs) > 0
		if ready {
			status.ProviderReady = true
			if onboardingProviderVerified(state, provider.ID) {
				status.ProviderVerified = true
			} else if status.Verification.Kind == "" {
				status.Verification = onboardingVerification{Kind: "custom_provider", ID: provider.ID}
			}
		}
	}

	zenConfig := zen.GetConfig()
	zenVerified := onboardingProviderVerified(state, onboardingZenVerificationKey)
	zenConfigured := zenVerified
	if zenConfig != nil {
		key := strings.TrimSpace(zenConfig.Key)
		zenConfigured = zenConfigured || len(zenConfig.SessionIDs) > 0 || (key != "" && key != "public")
	}
	if zenConfig != nil && zenConfigured {
		status.ProviderConfigured = true
		if zenConfig.Enabled && onboardingEndpointConfigured(zenConfig.BaseURL) {
			status.ProviderReady = true
			if zenVerified {
				status.ProviderVerified = true
			} else if status.Verification.Kind == "" {
				status.Verification = onboardingVerification{Kind: "opencode_zen"}
			}
		}
	}

	status.SuccessfulGatewayRequest = state.FirstSuccessfulGatewayRequestAt != nil && !state.FirstSuccessfulGatewayRequestAt.IsZero()
	if !status.SuccessfulGatewayRequest {
		// Migrate existing installations that already completed a real request.
		// New completions are persisted directly by the request completion owner.
		for _, entry := range requestlog.Snapshot() {
			if entry.Completed && entry.Error == "" {
				completedAt := entry.FinishedAt
				if completedAt.IsZero() {
					completedAt = entry.StartedAt
				}
				if completedAt.IsZero() {
					completedAt = time.Now()
				}
				if err := onboardingstore.RecordSuccessfulGatewayRequest(onboardingStatePath(), completedAt); err != nil {
					log.Printf("onboarding gateway request completion could not be persisted: %v", err)
				} else {
					status.SuccessfulGatewayRequest = true
				}
				break
			}
		}
	}

	switch {
	case !status.ProviderConfigured:
		status.Stage = "connect_provider"
	case !status.ProviderReady || !status.ProviderVerified:
		status.Stage = "verify_provider"
	case !status.SuccessfulGatewayRequest:
		status.Stage = "first_request"
	default:
		status.Stage = "dashboard"
	}
	return status
}

func handleOnboardingStatus(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteAPI(w, http.StatusOK, httpx.Response{Success: true, Data: currentOnboardingStatus()})
}
