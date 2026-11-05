package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"routenest/internal/admin"
	"routenest/internal/egress"
	"routenest/internal/routing"
)

func TestCharacterizationCline402CoolsOfferingAndRotatesCredential(t *testing.T) {
	oldPool := getTestPool()
	oldConfig := admin.GetProxyConfig()
	oldTransport := egress.Client.Transport
	t.Cleanup(func() {
		setTestPool(oldPool)
		admin.SetProxyConfig(oldConfig)
		egress.Client.Transport = oldTransport
	})

	first := &Account{
		AccountID: "quota-one", Email: "one@example.com", AccessToken: "token-one",
		ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), Status: "active",
	}
	second := &Account{
		AccountID: "quota-two", Email: "two@example.com", AccessToken: "token-two",
		ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), Status: "active",
	}
	setTestPool(&AccountPool{Accounts: []*Account{first, second}})
	admin.SetProxyConfig(admin.DefaultProxyConfig())

	var attempts []string
	egress.Client.Transport = freeModelRoundTripper(func(req *http.Request) (*http.Response, error) {
		token := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
		attempts = append(attempts, token)
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		var upstream map[string]any
		if err := json.Unmarshal(body, &upstream); err != nil {
			return nil, err
		}
		if upstream["model"] != routing.FreeModelPrimary {
			t.Fatalf("upstream model = %v, want %q", upstream["model"], routing.FreeModelPrimary)
		}
		status := http.StatusOK
		responseBody := `{"id":"ok","choices":[]}`
		if token == "token-one" {
			status = http.StatusPaymentRequired
			responseBody = `{"error":"insufficient credits"}`
		}
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(responseBody)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})

	params := map[string]any{
		"model":    "free",
		"messages": []any{map[string]any{"role": "user", "content": "hello"}},
	}
	resp, selected, err := routing.CallClineAPI(params, false)
	if err != nil {
		t.Fatalf("routing.CallClineAPI returned error: %v", err)
	}
	if resp == nil {
		t.Fatal("routing.CallClineAPI returned nil response")
	}
	resp.Body.Close()

	if selected != second {
		t.Fatalf("selected account = %v, want second account", selected)
	}
	if got := strings.Join(attempts, ","); got != "token-one,token-two" {
		t.Fatalf("attempts = %q, want token-one,token-two", got)
	}
	if !routing.ModelCooldownActive(first, routing.FreeModelPrimary) {
		t.Fatal("402 must cool the exhausted credential's model offering")
	}
	until := first.ModelCooldowns[routing.FreeModelPrimary]
	remaining := time.Until(until)
	if remaining < 29*time.Minute || remaining > 31*time.Minute {
		t.Fatalf("402 cooldown remaining = %s, want about 30m", remaining)
	}
}
