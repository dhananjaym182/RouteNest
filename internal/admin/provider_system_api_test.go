package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/domain"
	"routenest/internal/httpx"
	"routenest/internal/providers"
	"routenest/internal/providerstate"
)

func installProviderSystemFixtures(t *testing.T) {
	t.Helper()
	providers.Load()
	providers.Mu.Lock()
	oldProviders := providers.CustomProviders
	providers.CustomProviders = append([]*providers.CustomProvider{{
		ID: "prov_system_test", Name: "System Test",
		BaseURL: "https://user:pass@system.example/v1?token=query-secret",
		APIKey:  "sk-super-secret", ProxyURL: "http://proxy-user:proxy-secret@proxy.example:8080",
		ModelIDs: []string{"system-model"}, Enabled: true,
	}}, oldProviders...)
	providers.Mu.Unlock()

	accounts.PoolMu.Lock()
	oldPool, oldPath := accounts.Pool, accounts.PoolPath
	accounts.PoolPath = t.TempDir() + "/.routenest-accounts.json"
	accounts.Pool = &domain.AccountPool{Accounts: []*domain.Account{{
		AccountID: "acc_system", Email: "system@example.com",
		AccessToken: "access-token-secret", RefreshToken: "refresh-token-secret",
		Status: "active",
	}}}
	accounts.PoolMu.Unlock()

	t.Cleanup(func() {
		providers.Mu.Lock()
		providers.CustomProviders = oldProviders
		providers.Mu.Unlock()
		accounts.PoolMu.Lock()
		accounts.Pool, accounts.PoolPath = oldPool, oldPath
		accounts.PoolMu.Unlock()
	})
}

func callProviderSystemHandler(t *testing.T, handler http.HandlerFunc, method string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(method, "/admin/api/x", nil))
	return rec
}

func decodeProviderSystemData(t *testing.T, rec *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	var envelope struct {
		Success bool                       `json:"success"`
		Data    map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v (%s)", err, rec.Body.String())
	}
	if !envelope.Success {
		t.Fatalf("envelope not successful: %s", rec.Body.String())
	}
	return envelope.Data
}

func TestProviderSystemHandlersExposeRedactedGlobalViews(t *testing.T) {
	installProviderSystemFixtures(t)

	instancesRec := callProviderSystemHandler(t, handleProviderInstances, http.MethodGet)
	if instancesRec.Code != http.StatusOK {
		t.Fatalf("instances status = %d: %s", instancesRec.Code, instancesRec.Body.String())
	}
	if body := instancesRec.Body.String(); strings.Contains(body, "sk-super-secret") ||
		strings.Contains(body, "proxy-secret") || strings.Contains(body, "query-secret") {
		t.Fatalf("provider instance projection leaked a secret: %s", body)
	}
	var instances []domain.ProviderInstance
	if err := json.Unmarshal(decodeProviderSystemData(t, instancesRec)["instances"], &instances); err != nil {
		t.Fatalf("decode instances: %v", err)
	}
	found := false
	for _, instance := range instances {
		if instance.ID != "prov_system_test" {
			continue
		}
		found = true
		if instance.Enabled != true || instance.AdapterID != "openaicompat" {
			t.Fatalf("instance = %+v", instance)
		}
		for capability, state := range instance.Capabilities {
			if !state.Valid() {
				t.Fatalf("capability %q has invalid state %q", capability, state)
			}
		}
	}
	if !found {
		t.Fatalf("fixture provider missing from instances: %+v", instances)
	}

	credentialsRec := callProviderSystemHandler(t, handleProviderCredentials, http.MethodGet)
	if credentialsRec.Code != http.StatusOK {
		t.Fatalf("credentials status = %d: %s", credentialsRec.Code, credentialsRec.Body.String())
	}
	if body := credentialsRec.Body.String(); strings.Contains(body, "access-token-secret") ||
		strings.Contains(body, "refresh-token-secret") {
		t.Fatalf("credential projection leaked a token: %s", body)
	}

	offeringsRec := callProviderSystemHandler(t, handleModelOfferings, http.MethodGet)
	if offeringsRec.Code != http.StatusOK {
		t.Fatalf("offerings status = %d: %s", offeringsRec.Code, offeringsRec.Body.String())
	}
	if _, ok := decodeProviderSystemData(t, offeringsRec)["offerings"]; !ok {
		t.Fatal("offerings envelope has no offerings key")
	}

	egressRec := callProviderSystemHandler(t, handleEgressProfiles, http.MethodGet)
	if egressRec.Code != http.StatusOK {
		t.Fatalf("egress status = %d: %s", egressRec.Code, egressRec.Body.String())
	}
	egressData := decodeProviderSystemData(t, egressRec)
	if _, ok := egressData["profiles"]; !ok {
		t.Fatal("egress envelope has no profiles key")
	}
	if _, ok := egressData["globalProfileId"]; !ok {
		t.Fatal("egress envelope has no globalProfileId key")
	}
}

func TestProviderStateHandlerSeparatesResetFromCooldown(t *testing.T) {
	if err := providerstate.Publish(providerstate.NewStore(time.Now)); err != nil {
		t.Fatal(err)
	}
	resetAt := time.Now().Add(90 * time.Minute).UTC().Truncate(time.Second)
	scope := domain.AvailabilityScope{ProviderID: "prov_system_state", ModelID: "system-model"}
	if err := providerstate.Default().Apply(domain.AvailabilityObservation{
		Kind: domain.ObservationQuotaExhausted, Scope: scope,
		Until: resetAt.Add(15 * time.Minute), ResetAt: resetAt,
		Source: "test-explicit", Confidence: domain.ConfidenceExplicit,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = providerstate.Default().Recover(scope) })

	rec := callProviderSystemHandler(t, handleProviderState, http.MethodGet)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var observations []domain.AvailabilityObservation
	if err := json.Unmarshal(decodeProviderSystemData(t, rec)["observations"], &observations); err != nil {
		t.Fatalf("decode observations: %v", err)
	}
	found := false
	for _, observation := range observations {
		if observation.Scope != scope {
			continue
		}
		found = true
		if observation.Kind != domain.ObservationQuotaExhausted ||
			observation.Confidence != domain.ConfidenceExplicit ||
			observation.Source != "test-explicit" {
			t.Fatalf("observation evidence = %+v", observation)
		}
		if observation.ResetAt.IsZero() || !observation.ResetAt.Equal(resetAt) {
			t.Fatalf("resetAt = %s, want %s", observation.ResetAt, resetAt)
		}
		if observation.Until.IsZero() || observation.Until.Equal(observation.ResetAt) {
			t.Fatalf("until must remain separate from resetAt: %+v", observation)
		}
	}
	if !found {
		t.Fatalf("fixture observation missing: %+v", observations)
	}
}

func TestProviderSystemHandlersRejectNonGET(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"instances":   handleProviderInstances,
		"credentials": handleProviderCredentials,
		"offerings":   handleModelOfferings,
		"state":       handleProviderState,
		"egress":      handleEgressProfiles,
	} {
		rec := callProviderSystemHandler(t, handler, http.MethodPost)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status = %d, want 405", name, rec.Code)
		}
	}
}

func TestProviderSystemRoutesRequireAdminSession(t *testing.T) {
	accounts.PoolMu.Lock()
	oldPool, oldPath := accounts.Pool, accounts.PoolPath
	accounts.PoolPath = t.TempDir() + "/.routenest-accounts.json"
	accounts.Pool = &domain.AccountPool{}
	accounts.PoolMu.Unlock()
	t.Cleanup(func() {
		setAdminPassword("")
		accounts.PoolMu.Lock()
		accounts.Pool, accounts.PoolPath = oldPool, oldPath
		accounts.PoolMu.Unlock()
	})

	setAdminPassword("secret")
	mux := http.NewServeMux()
	registerProviderSystemRoutes(mux, func(handler http.HandlerFunc) http.HandlerFunc {
		return requireAdminAuth(httpx.CORS(handler))
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/api/provider-instances", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", rec.Code)
	}

	loginRec := httptest.NewRecorder()
	handleAdminLogin(loginRec, httptest.NewRequest(http.MethodPost, "/admin/api/login",
		strings.NewReader(`{"password":"secret"}`)))
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d: %s", loginRec.Code, loginRec.Body.String())
	}
	cookies := loginRec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies = %+v", cookies)
	}
	req := httptest.NewRequest(http.MethodGet, "/admin/api/provider-instances", nil)
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated status = %d: %s", rec.Code, rec.Body.String())
	}
}
