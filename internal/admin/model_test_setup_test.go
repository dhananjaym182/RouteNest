package admin_test

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/admin"
	_ "cline-go-proxy/internal/app"
	"cline-go-proxy/internal/domain"
	"cline-go-proxy/internal/egress"
	"cline-go-proxy/internal/platform"
	"cline-go-proxy/internal/providers"
)

type Account = domain.Account
type AccountPool = domain.AccountPool
type ComboEntry = domain.ComboEntry
type CustomProvider = domain.CustomProvider
type Model = domain.Model
type ModelCombo = domain.ModelCombo

type freeModelRoundTripper func(*http.Request) (*http.Response, error)

func (f freeModelRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func comboTestAccount(id string) *Account {
	return &Account{
		AccountID: id, Email: id + "@example.com", AccessToken: id + "-token",
		ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), Status: "active",
	}
}

func installPool(t *testing.T, models []Model, accs ...*Account) {
	t.Helper()
	old := accounts.Pool
	t.Cleanup(func() { accounts.Pool = old })
	if len(accs) == 0 {
		accs = []*Account{comboTestAccount("acc1")}
	}
	accounts.Pool = &AccountPool{Accounts: accs, Models: models}
}

func installConfig(t *testing.T, cfg *admin.ProxyConfig) {
	t.Helper()
	t.Setenv(platform.DataDirEnv, t.TempDir())
	old := admin.GetProxyConfig()
	t.Cleanup(func() { admin.SetProxyConfig(old) })
	admin.SetProxyConfig(cfg)
}

func installTransport(t *testing.T, fn func(req *http.Request) *http.Response) {
	t.Helper()
	old := egress.Client.Transport
	t.Cleanup(func() { egress.Client.Transport = old })
	egress.Client.Transport = freeModelRoundTripper(func(req *http.Request) (*http.Response, error) {
		return fn(req), nil
	})
}

func installProviders(t *testing.T, provs ...*CustomProvider) {
	t.Helper()
	oldPath, oldCool := providers.FilePath, providers.CooldownsPath
	oldProvs, oldLoaded := providers.CustomProviders, providers.Loaded
	providers.FilePath = filepath.Join(t.TempDir(), ".cline-providers.json")
	providers.CooldownsPath = filepath.Join(t.TempDir(), ".cline-providers-cooldowns.json")
	providers.CustomProviders = nil
	providers.Loaded = true
	t.Cleanup(func() {
		providers.FilePath, providers.CooldownsPath = oldPath, oldCool
		providers.CustomProviders, providers.Loaded = oldProvs, oldLoaded
		providers.ResetRoutingState()
	})
	for _, p := range provs {
		providers.Upsert(p)
	}
}

func stubResp(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
		Request:    req,
	}
}

func readReqModel(req *http.Request) string {
	if req == nil || req.Body == nil {
		return ""
	}
	b, _ := io.ReadAll(req.Body)
	var p map[string]any
	_ = json.Unmarshal(b, &p)
	m, _ := p["model"].(string)
	return m
}

const okCompletion = `{"id":"ok","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
