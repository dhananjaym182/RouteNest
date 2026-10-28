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
// the reference list price.
//
// A standalone "free" word anywhere in the id (free/… , …:free , …-free) or the
// display name is treated as a free marker: gateways advertise free variants by
// embedding "free" in the model id/name, and administrators expect those to be
// classified free. The provider-level free flag also grants free (a source
// marked whole-free is trusted). Only a parsed non-zero price — a real per-model
// price — outranks these name/provider signals, since an explicit price is
// stronger evidence than a "free" word. Access-class policy lives in
// domain.AccessClass and is applied separately.
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
	// Strong pricing evidence: any parsed non-zero component wins over the
	// word/name/provider signals below (a real price beats a "free" name).
	// Request-only fees stay paid under this rule too.
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
	// "free" appearing anywhere in the id (free/… , …:free , …-free , or a
	// standalone token) or the display name marks the model free — these
	// naming conventions are how gateways advertise free variants.
	if FreeWord(lower) {
		return "free", TierSignalNameToken
	}
	if FreeWord(e.Name) {
		return "free", TierSignalDisplayName
	}
	if providerFree {
		// Provider 整源标记免费：视为该源的模型免费（管理端显式声明）。
		return "free", TierSignalProviderFree
	}
	return "pass", TierSignalPaidDefault
}
