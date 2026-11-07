package routing

import (
	"encoding/json"
	"io"
	"log"
	"regexp"
	"strconv"
	"time"

	"routenest/internal/accounts"
	"routenest/internal/domain"
	"routenest/internal/platform"
	"routenest/internal/protocol/openaichat"
	"routenest/internal/providerstate"
)

type accountTestResult struct {
	AccountID    string `json:"accountId"`
	Email        string `json:"email"`
	OK           bool   `json:"ok"`
	DurationMs   int64  `json:"durationMs"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
	Error        string `json:"error,omitempty"`
}

// parseCooldownUntil 从 429 响应体中解析 "Try again in 1h 1m" 格式的等待时长，
// 返回预计恢复时间；解析失败则回退到 1 小时后。
var cooldownRe = regexp.MustCompile(`(?i)try\s+again\s+in\s+(\d+)\s*h?(?:\s*(\d+))?\s*m?`)

func parseCooldownUntil(body string) time.Time {
	matches := cooldownRe.FindStringSubmatch(body)
	if len(matches) >= 2 {
		hours, _ := strconv.Atoi(matches[1])
		minutes := 0
		if len(matches) >= 3 && matches[2] != "" {
			minutes, _ = strconv.Atoi(matches[2])
		}
		if hours > 0 || minutes > 0 {
			return time.Now().Add(time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute)
		}
	}

	return time.Now().Add(1 * time.Hour)
}

// StartCooldownRecovery 启动后台 goroutine，每 30 秒检查一次 cooldown 账号，
// 对 CooldownUntil 已过期的账号执行探活，成功则自动激活。
func StartCooldownRecovery() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			p := accounts.LoadPool()
			accounts.PoolMu.Lock()
			var toRecover []*domain.Account
			for _, acc := range p.Accounts {
				if acc.Status != "cooldown" {
					continue
				}

				if acc.CooldownUntil.IsZero() || time.Now().After(acc.CooldownUntil) {
					toRecover = append(toRecover, acc)
				}
			}
			accounts.PoolMu.Unlock()

			for _, acc := range toRecover {
				log.Printf("cooldown recovery: testing %s", acc.Email)
				result := TestAccount(acc)
				if result.OK {
					log.Printf("cooldown recovery: %s reactivated", acc.Email)
				} else {
					log.Printf("cooldown recovery: %s still unavailable: %s", acc.Email, result.Error)
				}
			}
		}
	}()
}

// TestAccount sends a minimal "hi" request through a specific account to verify
// it can complete an upstream call. It does not update aggregate token counters
// or request logs; it is a diagnostic-only probe.
func TestAccount(acc *domain.Account) accountTestResult {
	result := accountTestResult{AccountID: acc.AccountID, Email: acc.Email}
	started := time.Now()

	params := map[string]any{
		"model":      DefaultModel(),
		"max_tokens": 1024,
		"stream":     false,
		"messages": []any{
			map[string]any{"role": "user", "content": "hi"},
		},
	}

	resp, _, err := CallClineAPIWithAccount(acc, params, false)
	if err != nil {
		result.DurationMs = time.Since(started).Milliseconds()
		result.Error = platform.Truncate(err.Error(), 200)
		return result
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		result.DurationMs = time.Since(started).Milliseconds()
		result.Error = "read response: " + platform.Truncate(err.Error(), 200)
		return result
	}

	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		result.DurationMs = time.Since(started).Milliseconds()
		result.Error = "decode response: " + platform.Truncate(err.Error(), 200)
		return result
	}
	if data, ok := obj["data"]; ok {
		if d, ok := data.(map[string]any); ok {
			obj = d
		}
	}
	obj = openaichat.NormalizeResponse(obj)
	usage := openaichat.ParseUsage(obj["usage"])

	result.OK = true
	result.DurationMs = time.Since(started).Milliseconds()
	if usage.Valid {
		result.InputTokens = usage.Prompt
		result.OutputTokens = usage.Completion
	}

	accounts.ActivateAccount(acc)
	return result
}

// ModelCooldownActive 判断某账号下该模型是否处于模型级冷却中。
func ModelCooldownActive(acc *domain.Account, model string) bool {
	if acc == nil || model == "" {
		return false
	}
	accounts.PoolMu.Lock()
	defer accounts.PoolMu.Unlock()
	until, ok := acc.ModelCooldowns[model]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(acc.ModelCooldowns, model)
		accounts.SavePoolLocked()
		return false
	}
	return true
}

// HasAlternativeModel 判断降级链上是否还有未冷却的候选模型。
// 用于避免「所有模型都被冷却」导致整条链不可用。
func HasAlternativeModel(acc *domain.Account, current string) bool {
	for _, m := range ModelFallbackChain(current) {
		if m == "" || m == current {
			continue
		}
		if !ModelCooldownActive(acc, m) {
			return true
		}
	}
	return false
}

// SetModelCooldown 记录模型级冷却（429 或首 token 前失败时调用）：只暂停该模型，账号保持可用。
// fallback 为解析失败时的恢复时长（默认 1 小时）。
func SetModelCooldown(acc *domain.Account, model string, until time.Time) {
	if acc == nil || model == "" {
		return
	}
	accounts.PoolMu.Lock()
	if acc.ModelCooldowns == nil {
		acc.ModelCooldowns = make(map[string]time.Time)
	}
	acc.ModelCooldowns[model] = until
	accounts.SavePoolLocked()
	accounts.PoolMu.Unlock()
	log.Printf("model cooldown: account=%s model=%s until=%s", TruncateEmail(acc.Email), model, until.Format("15:04:05"))
	if providerstate.HasDefault() && acc.AccountID != "" {
		scope := domain.AvailabilityScope{
			ProviderID: "cline", CredentialID: domain.CredentialID("cline:" + acc.AccountID), ModelID: model,
		}
		if until.IsZero() || !time.Now().Before(until) {
			_ = providerstate.Default().Recover(scope)
		} else {
			_ = providerstate.Default().Apply(domain.AvailabilityObservation{
				Kind: domain.ObservationRateLimited, Scope: scope, Until: until,
				Source: "legacy_cline_model_cooldown", Confidence: domain.ConfidenceFallback,
			})
		}
	}
}

func TruncateEmail(email string) string {
	if len(email) <= 12 {
		return email
	}
	parts := splitEmail(email)
	if len(parts) == 2 && len(parts[0]) > 3 {
		return parts[0][:3] + "***@" + parts[1]
	}
	if len(email) > 12 {
		return email[:8] + "..."
	}
	return email
}

func splitEmail(email string) []string {
	for i := 0; i < len(email); i++ {
		if email[i] == '@' {
			return []string{email[:i], email[i+1:]}
		}
	}
	return []string{email}
}
