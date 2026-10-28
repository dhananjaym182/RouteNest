// Package modelcatalog holds model discovery types, free/paid classification,
// and access-class policy. It performs no HTTP and no routing.
package modelcatalog

import (
	"strings"
	"unicode"
)

// Tier signals (mirrored in Model.TierSignal, shown to administrators).
// Ordered strongest-first.
const (
	TierSignalPricingZero  = "pricing-zero"      // prompt/completion 0 with no per-request fee
	TierSignalFlagFree     = "flag-free"         // entry declares free: true or mode "free"
	TierSignalSuffixFree   = "suffix-free"       // id ends with :free / -free
	TierSignalNameToken    = "name-token-free"   // id has a standalone free segment
	TierSignalDisplayName  = "display-name-free" // display name carries a standalone free word
	TierSignalAllowlist    = "allowlist"         // tags say free, or the id hits the seed allowlist
	TierSignalProviderFree = "provider-free"     // provider-level free flag (metadata, not per-model)
	TierSignalPricingPaid  = "pricing-paid"      // explicit nonzero pricing outranks provider-free
	TierSignalPaidDefault  = "paid-default"      // no free signal: never guessed
)

// Entry is one parsed /models item (OpenAI list plus OpenRouter-style pricing).
type Entry struct {
	ID             string
	Name           string
	PricingPrompt  *float64
	PricingComplet *float64
	PricingRequest *float64
	Tags           []string
	FreeFlag       bool
	Mode           string
}

// FreeWord reports whether s carries a standalone "free" word. Any
// non-alphanumeric rune separates tokens, so "cline-free/x" matches while
// "freemap" does not.
func FreeWord(s string) bool {
	for _, tok := range strings.FieldsFunc(strings.ToLower(strings.TrimSpace(s)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if tok == "free" {
			return true
		}
	}
	return false
}

// Classify checks signals in order and returns on the first hit. Explicit free
// markers (free flag, free mode, ":free"/"-free" id suffix) outrank explicit
// non-zero pricing because gateway catalogs commonly list free variants with
// the reference list price. A parsed non-zero price outranks the weak fallbacks.
//
// Weak word/metadata signals (a standalone "free" word inside the id or display
// name, an allowlist tag, and the provider-level free flag) no longer grant a
// free tier: an aggregator namespace like "free/claude-opus" and a
// provider-scoped claim are metadata, not per-model entitlements, and they must
// not turn settled paid endpoints into "free" classifications. They fall
// through to the paid default unless stronger evidence exists. Access-class
// policy lives in domain.AccessClass and is applied separately.
//
// The returned tier keeps the legacy "free"/"pass" vocabulary used by the
// import flow. Access-class policy lives in domain.AccessClass and is applied
// separately so provider metadata is never mistaken for an entitlement.
func Classify(e Entry, providerFree bool, allowlist map[string]bool) (tier, signal string) {
	if e.PricingPrompt != nil && e.PricingComplet != nil && *e.PricingPrompt == 0 && *e.PricingComplet == 0 &&
		(e.PricingRequest == nil || *e.PricingRequest == 0) {
		return "free", TierSignalPricingZero
	}
	if e.FreeFlag || strings.EqualFold(strings.TrimSpace(e.Mode), "free") {
		return "free", TierSignalFlagFree
	}
	lower := strings.ToLower(e.ID)
	if strings.HasSuffix(lower, ":free") || strings.HasSuffix(lower, "-free") {
		return "free", TierSignalSuffixFree
	}
	// Strong pricing evidence: any parsed non-zero component wins over every
	// weak signal below. Request-only fees stay paid under this rule too.
	if (e.PricingPrompt != nil && *e.PricingPrompt != 0) || (e.PricingComplet != nil && *e.PricingComplet != 0) ||
		(e.PricingRequest != nil && *e.PricingRequest != 0) {
		return "pass", TierSignalPricingPaid
	}
	for _, tag := range e.Tags {
		if strings.EqualFold(strings.TrimSpace(tag), "free") {
			return "free", TierSignalAllowlist
		}
	}
	if allowlist[lower] {
		return "free", TierSignalAllowlist
	}
	if FreeWord(lower) {
		// 弱信号：id 里的独立 free 词段（如 "free/claude-opus" 目录命名）不是
		// 每模型权益，交给下方 provider-free 元数据处理，不再单独授予免费。
		return "pass", TierSignalNameToken
	}
	if FreeWord(e.Name) {
		return "pass", TierSignalDisplayName
	}
	if providerFree {
		// Provider 级 free 标志是元数据而非逐模型权益：不再授予免费，
		// 仅保留溯源信号（UI 显示「整源免费但无逐模型证据」）。
		return "pass", TierSignalProviderFree
	}
	return "pass", TierSignalPaidDefault
}
