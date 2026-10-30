package providers

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"cline-go-proxy/internal/config"
	"cline-go-proxy/internal/domain"
	"cline-go-proxy/internal/upstream/openaicompat"
)

// Domain aliases keep the moved registry code readable while preserving the
// exact types used by root call sites during the phased extraction.
type CustomProvider = domain.CustomProvider
type Connection = domain.Connection
type Account = domain.Account
type AccountPool = domain.AccountPool
type Model = domain.Model
type ModelStat = domain.ModelStat
type RequestLog = domain.RequestLog

// ProviderIDParam carries the provider ID through the request map for request
// log attribution. It was previously owned by package main.
const ProviderIDParam = "__served_by_id"

// Root-owned dependencies are injected once from http.go. Defaults keep this
// internal package independently testable without importing package main.
var (
	TransportProvider = func() http.RoundTripper { return http.DefaultTransport }

	BodyOptionsProvider = func() openaicompat.BodyOptions {
		return openaicompat.BodyOptions{}
	}
	ConfigProvider = config.DefaultProxy

	APIErrorFactory = func(status int, message string) error {
		return fmt.Errorf("API %d: %s", status, message)
	}
	ErrorRuleDecision = func(error) (stop, cooldown, matched bool) {
		return false, false, false
	}
	ErrorStatus       = func(error) int { return 0 }
	RuleCooldownUntil = func(int) time.Time {
		return time.Now().Add(2 * time.Minute)
	}
	StopError  = func(err error) error { return err }
	LogRuleHit = func(string, int, string) {}

	RequestLogsSnapshot = func() []RequestLog { return nil }
)

var freeIDs = func() *atomic.Value {
	v := &atomic.Value{}
	v.Store(map[string]bool{})
	return v
}()

// SetFreeModelIDs installs the root Zen seed IDs and aliases used by provider
// model classification. The copy makes later root mutations irrelevant.
func SetFreeModelIDs(ids []string) {
	values := make(map[string]bool, len(ids))
	for _, id := range ids {
		values[strings.ToLower(id)] = true
	}
	freeIDs.Store(values)
}

func freeModelIDAllowlist() map[string]bool {
	return freeIDs.Load().(map[string]bool)
}
