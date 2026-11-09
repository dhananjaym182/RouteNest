package admin

import (
	"path/filepath"
	"testing"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/domain"
	"routenest/internal/platform"
	"routenest/internal/protocol/openaichat"
	"routenest/internal/providers"
	"routenest/internal/requestlog"
	onboardingstore "routenest/internal/store/onboarding"
	"routenest/internal/zen"
)

func isolateOnboardingStatus(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(platform.DataDirEnv, dir)
	accounts.PoolMu.Lock()
	oldPool := accounts.Pool
	accounts.Pool = &domain.AccountPool{Accounts: []*domain.Account{}, Keys: []string{}, Models: []domain.Model{}}
	accounts.PoolMu.Unlock()
	providers.Mu.Lock()
	oldProviders, oldLoaded := providers.CustomProviders, providers.Loaded
	providers.CustomProviders, providers.Loaded = nil, true
	providers.Mu.Unlock()
	requestlog.Mu.Lock()
	oldLogs, oldLogPath := requestlog.Logs, requestlog.Path
	requestlog.Logs, requestlog.Path = nil, filepath.Join(dir, ".routenest-request-logs.json")
	requestlog.Mu.Unlock()
	oldRequestRecorder := requestlog.RecordSuccessfulGatewayRequest
	zen.ConfigMu.Lock()
	oldZen := zen.Config
	zen.Config = zen.DefaultConfig()
	zen.ConfigMu.Unlock()
	oldResolver := onboardingPathResolver
	path := filepath.Join(dir, onboardingstore.FileName)
	onboardingPathResolver = func() string { return path }
	requestlog.RecordSuccessfulGatewayRequest = func(at time.Time) error {
		return onboardingstore.RecordSuccessfulGatewayRequest(path, at)
	}
	t.Cleanup(func() {
		accounts.PoolMu.Lock()
		accounts.Pool = oldPool
		accounts.PoolMu.Unlock()
		providers.Mu.Lock()
		providers.CustomProviders, providers.Loaded = oldProviders, oldLoaded
		providers.Mu.Unlock()
		requestlog.Mu.Lock()
		requestlog.Logs, requestlog.Path = oldLogs, oldLogPath
		requestlog.Mu.Unlock()
		requestlog.RecordSuccessfulGatewayRequest = oldRequestRecorder
		zen.ConfigMu.Lock()
		zen.Config = oldZen
		zen.ConfigMu.Unlock()
		onboardingPathResolver = oldResolver
	})
	return path
}

func TestOnboardingStatusTransitionsFromProviderSetupToDashboard(t *testing.T) {
	path := isolateOnboardingStatus(t)

	if got := currentOnboardingStatus(); got.Stage != "connect_provider" {
		t.Fatalf("fresh stage = %+v", got)
	}
	accounts.PoolMu.Lock()
	accounts.Pool.Accounts = []*domain.Account{{AccountID: "cline-1", Email: "cline@example.com", Status: "active"}}
	accounts.Pool.Models = []domain.Model{{ID: "cline-model", Status: "active"}}
	accounts.PoolMu.Unlock()
	if got := currentOnboardingStatus(); got.Stage != "verify_provider" || got.ProviderVerified || got.Verification.Kind != "cline_account" || got.Verification.ID != "cline-1" {
		t.Fatalf("unverified Cline stage = %+v", got)
	}
	markOnboardingProviderVerified(onboardingClineVerificationKey("cline-1"))
	if got := currentOnboardingStatus(); got.Stage != "first_request" || !got.ProviderVerified {
		t.Fatalf("verified Cline stage = %+v", got)
	}
	accounts.PoolMu.Lock()
	accounts.Pool.Accounts = []*domain.Account{}
	accounts.Pool.Models = []domain.Model{}
	accounts.PoolMu.Unlock()
	providers.Mu.Lock()
	providers.CustomProviders = []*providers.CustomProvider{{ID: "provider-1", Name: "Provider One", BaseURL: "https://provider.example/v1", APIKey: "secret", ModelIDs: []string{"model-1"}, Enabled: true}}
	providers.Mu.Unlock()
	accounts.PoolMu.Lock()
	accounts.Pool.Models = []domain.Model{{ID: "model-1", Status: "active"}}
	accounts.PoolMu.Unlock()
	if got := currentOnboardingStatus(); got.Stage != "verify_provider" || got.Verification.ID != "provider-1" {
		t.Fatalf("unverified stage = %+v", got)
	}
	markOnboardingProviderVerified("provider-1")
	if got := currentOnboardingStatus(); got.Stage != "first_request" || !got.ProviderVerified {
		t.Fatalf("verified stage = %+v", got)
	}
	requestlog.Mu.Lock()
	requestlog.Logs = []domain.RequestLog{{ID: "request-1", StartedAt: time.Now(), Completed: true}}
	requestlog.Mu.Unlock()
	if got := currentOnboardingStatus(); got.Stage != "dashboard" || !got.SuccessfulGatewayRequest {
		t.Fatalf("returning stage = %+v", got)
	}
	state, err := onboardingstore.Load(path)
	if err != nil || state.FirstSuccessfulGatewayRequestAt == nil || state.FirstSuccessfulGatewayRequestAt.IsZero() {
		t.Fatalf("migrated request completion = %+v, %v", state, err)
	}
	requestlog.Clear()
	if got := currentOnboardingStatus(); got.Stage != "dashboard" || !got.SuccessfulGatewayRequest {
		t.Fatalf("stage after clearing request history = %+v", got)
	}
}

func TestOnboardingStatusKeylessProviderRequiresPersistedVerification(t *testing.T) {
	path := isolateOnboardingStatus(t)
	providers.Mu.Lock()
	providers.CustomProviders = []*providers.CustomProvider{{ID: "local", Name: "Local", BaseURL: "http://127.0.0.1:11434/v1", ModelIDs: []string{"local-model"}, Enabled: true}}
	providers.Mu.Unlock()
	if got := currentOnboardingStatus(); got.Stage != "verify_provider" || !got.ProviderReady || got.ProviderVerified || got.Verification.Kind != "custom_provider" || got.Verification.ID != "local" {
		t.Fatalf("unverified keyless provider = %+v", got)
	}
	// Merely having a verification map entry is not successful test evidence.
	state := onboardingstore.Empty()
	state.VerifiedProviders["local"] = time.Time{}
	if err := onboardingstore.Save(path, state); err != nil {
		t.Fatal(err)
	}
	if got := currentOnboardingStatus(); got.Stage != "verify_provider" || got.ProviderVerified {
		t.Fatalf("zero-time verification = %+v", got)
	}
	markOnboardingProviderVerified("local")
	if got := currentOnboardingStatus(); got.Stage != "first_request" || !got.ProviderReady || !got.ProviderVerified {
		t.Fatalf("verified keyless provider = %+v", got)
	}
	clearOnboardingProviderVerification("local")
	if got := currentOnboardingStatus(); got.Stage != "verify_provider" || got.ProviderVerified {
		t.Fatalf("cleared verification = %+v", got)
	}
}

func TestOnboardingStatusPublicZenRequiresPersistedVerification(t *testing.T) {
	isolateOnboardingStatus(t)
	if got := currentOnboardingStatus(); got.Stage != "connect_provider" || got.ProviderReady || got.ProviderVerified {
		t.Fatalf("built-in public defaults = %+v", got)
	}
	markOnboardingProviderVerified(onboardingZenVerificationKey)
	if got := currentOnboardingStatus(); got.Stage != "first_request" || !got.ProviderConfigured || !got.ProviderReady || !got.ProviderVerified {
		t.Fatalf("verified public Zen = %+v", got)
	}
	zen.ConfigMu.Lock()
	zen.Config.Enabled = false
	zen.ConfigMu.Unlock()
	if got := currentOnboardingStatus(); got.Stage != "verify_provider" || got.ProviderReady || got.ProviderVerified {
		t.Fatalf("disabled verified public Zen = %+v", got)
	}
}

func TestOnboardingStatusConfiguredPublicZenRemainsUnverified(t *testing.T) {
	isolateOnboardingStatus(t)
	zen.ConfigMu.Lock()
	zen.Config.SessionIDs = []string{"registered-session"}
	zen.ConfigMu.Unlock()
	if got := currentOnboardingStatus(); got.Stage != "verify_provider" || !got.ProviderConfigured || !got.ProviderReady || got.ProviderVerified || got.Verification.Kind != "opencode_zen" {
		t.Fatalf("unverified public Zen with a session = %+v", got)
	}
}

func TestOnboardingStatusUnavailableProviderCannotAdvanceWithSavedVerification(t *testing.T) {
	isolateOnboardingStatus(t)
	markOnboardingProviderVerified("local")
	for _, tc := range []struct {
		name     string
		baseURL  string
		enabled  bool
		modelIDs []string
	}{
		{name: "disabled", baseURL: "http://localhost:11434/v1", modelIDs: []string{"m"}},
		{name: "no models", baseURL: "http://localhost:11434/v1", enabled: true},
		{name: "no endpoint", enabled: true, modelIDs: []string{"m"}},
		{name: "unsupported endpoint", baseURL: "ftp://localhost/v1", enabled: true, modelIDs: []string{"m"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			providers.Mu.Lock()
			providers.CustomProviders = []*providers.CustomProvider{{ID: "local", Name: "Local", BaseURL: tc.baseURL, Enabled: tc.enabled, ModelIDs: tc.modelIDs}}
			providers.Mu.Unlock()
			if got := currentOnboardingStatus(); got.Stage != "verify_provider" || got.ProviderReady || got.ProviderVerified {
				t.Fatalf("unavailable provider advanced = %+v", got)
			}
		})
	}
}

func TestOnboardingStatusSuccessfulCompletionSurvivesClearBeforeStatusRead(t *testing.T) {
	path := isolateOnboardingStatus(t)
	providers.Mu.Lock()
	providers.CustomProviders = []*providers.CustomProvider{{ID: "local", Name: "Local", BaseURL: "http://127.0.0.1:11434/v1", ModelIDs: []string{"local-model"}, Enabled: true}}
	providers.Mu.Unlock()
	markOnboardingProviderVerified("local")
	started := time.Now().Add(-time.Second)
	entry := domain.RequestLog{ID: "real-request", StartedAt: started}
	requestlog.Finalize(&entry, openaichat.Usage{}, time.Time{}, started, true, "")
	requestlog.Clear()
	if got := currentOnboardingStatus(); got.Stage != "dashboard" || !got.SuccessfulGatewayRequest {
		t.Fatalf("completion after request log clear before status read = %+v", got)
	}
	state, err := onboardingstore.Load(path)
	if err != nil || state.FirstSuccessfulGatewayRequestAt == nil || !state.FirstSuccessfulGatewayRequestAt.Equal(entry.FinishedAt) {
		t.Fatalf("persisted real completion = %+v, %v", state, err)
	}
}

func TestOnboardingStatusFailedAndIncompleteRequestsDoNotComplete(t *testing.T) {
	path := isolateOnboardingStatus(t)
	started := time.Now().Add(-time.Second)
	failed := domain.RequestLog{ID: "failed", StartedAt: started}
	requestlog.Finalize(&failed, openaichat.Usage{}, time.Time{}, started, true, "upstream failed")
	incomplete := domain.RequestLog{ID: "incomplete", StartedAt: started}
	requestlog.Finalize(&incomplete, openaichat.Usage{}, time.Time{}, started, false, "")
	if got := currentOnboardingStatus(); got.SuccessfulGatewayRequest {
		t.Fatalf("failed/incomplete requests counted as success = %+v", got)
	}
	state, err := onboardingstore.Load(path)
	if err != nil || state.FirstSuccessfulGatewayRequestAt != nil {
		t.Fatalf("failed/incomplete requests persisted as success = %+v, %v", state, err)
	}
}

func TestOnboardingStatusVerificationChangesPreserveCompletion(t *testing.T) {
	path := isolateOnboardingStatus(t)
	completed := time.Now().UTC()
	if err := onboardingstore.RecordSuccessfulGatewayRequest(path, completed); err != nil {
		t.Fatal(err)
	}
	markOnboardingProviderVerified("local")
	clearOnboardingProviderVerification("local")
	state, err := onboardingstore.Load(path)
	if err != nil || state.FirstSuccessfulGatewayRequestAt == nil || !state.FirstSuccessfulGatewayRequestAt.Equal(completed) || len(state.VerifiedProviders) != 0 {
		t.Fatalf("verification mutation lost completion = %+v, %v", state, err)
	}
}
