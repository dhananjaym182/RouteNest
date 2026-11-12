package providers

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"routenest/internal/budget"
	"routenest/internal/config"
)

// A pinned provider (a combo entry with source "provider", or a model@Provider
// request) is resolved strictly: when it yields no candidate, nothing else may
// serve that entry. The caller then needs a reason the operator can act on,
// because "raise the limit", "wait for the reset" and "fix the configuration"
// are different remedies.

// pinBlockLocked reports why p cannot serve model right now, or "" when it can.
// Conditions are checked in the order the resolver has always applied them, and
// the budget check, which reads the budget store, comes last. The caller holds
// Mu, as ResolveNamedForModel always has.
func pinBlockLocked(p *CustomProvider, model string, now time.Time, legacyOwners map[string]int) string {
	name := p.Name
	if strings.TrimSpace(name) == "" {
		name = p.ID
	}
	if !p.Enabled {
		return fmt.Sprintf("provider %q is disabled", name)
	}
	listed := false
	for _, id := range p.ModelIDs {
		if id == model {
			listed = true
			break
		}
	}
	if !listed {
		return fmt.Sprintf("provider %q does not offer model %q", name, model)
	}
	if !providerOfferingEligibleScoped(*p, model, providerMayInheritLegacy(*p, model, legacyOwners)) {
		return fmt.Sprintf("model %q is delisted or disabled on provider %q", model, name)
	}
	if until, cool := Cooldowns[p.ID+":"+model]; cool && now.Before(until) {
		return fmt.Sprintf("model %q is cooling down on provider %q until %s", model, name, formatResetUTC(until, now))
	}
	exhaustion, unavailable := providerBudgetBlock(p.ID, model, providerBudgetPolicy(*p))
	if exhaustion != nil {
		return fmt.Sprintf("provider %q: %s", name, describeExhaustion(exhaustion, now))
	}
	if unavailable {
		return fmt.Sprintf("provider %q: usage limits cannot be checked right now", name)
	}
	return ""
}

// describeExhaustion renders one configured limit, for example
// "daily token limit reached (3,034,885 of 3,000,000), resets 2026-11-13 00:00 UTC".
func describeExhaustion(e *budget.ExhaustionError, now time.Time) string {
	return fmt.Sprintf("%s reached (%s of %s), resets %s",
		ExhaustionKindLabel(e.Kind), groupThousands(e.Usage), groupThousands(e.Limit), formatResetUTC(e.ResetAt, now))
}

// ExhaustionKindLabel names a budget limit for people. Unknown kinds fall back
// to the raw kind so a new limit type is never shown as something else.
func ExhaustionKindLabel(kind string) string {
	switch kind {
	case "rpm":
		return "per-minute request limit"
	case "daily_requests":
		return "daily request limit"
	case "daily_tokens":
		return "daily token limit"
	case "monthly_tokens":
		return "monthly token limit"
	}
	return kind
}

// formatResetUTC prints a moment in UTC: just the time when it is today in
// UTC, with the date otherwise.
func formatResetUTC(at, now time.Time) string {
	at = at.UTC()
	if at.Format("2006-01-02") == now.UTC().Format("2006-01-02") {
		return at.Format("15:04:05") + " UTC"
	}
	return at.Format("2006-01-02 15:04") + " UTC"
}

func groupThousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

// PinUnavailableReason explains, in one sentence, why the provider selected by
// pin (a provider ID or a display name) cannot serve model right now. It
// reports the real blocker: disabled, not offering the model, delisted,
// cooling down, or a configured limit with its usage and reset time. When more
// than one provider shares a display name, every blocker is listed.
//
// It never returns an empty string. If the provider became usable between the
// failed resolution and this call, the sentence says so.
func PinUnavailableReason(model, pin string) string {
	pin = strings.TrimSpace(pin)
	if pin == "" {
		return fmt.Sprintf("no provider was selected for model %q", model)
	}
	Load()
	Mu.Lock()
	defer Mu.Unlock()
	idPin := GeneratedProviderIDPin(pin) || config.ProviderModelIDReserved(pin)
	for _, p := range CustomProviders {
		if p.ID == pin {
			idPin = true
			break
		}
	}
	now := time.Now()
	legacyOwners := providerLegacyOwnerCountsLocked()
	var reasons []string
	matched := 0
	for _, p := range CustomProviders {
		if !providerPinMatches(*p, pin, idPin) {
			continue
		}
		matched++
		reason := pinBlockLocked(p, model, now, legacyOwners)
		if reason == "" {
			return fmt.Sprintf("provider %q became available for model %q; retry the request", p.Name, model)
		}
		reasons = append(reasons, reason)
	}
	if matched == 0 {
		return fmt.Sprintf("no provider matches %q", pin)
	}
	return strings.Join(reasons, "; ")
}
