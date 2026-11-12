package domain

import "strings"

// AccessClass is the access basis of one model on one route. It is evaluated
// per provider/account route, never as a global property of a model ID.
type AccessClass string

const (
	AccessFree         AccessClass = "free"
	AccessFreeTier     AccessClass = "free_tier"
	AccessSubscription AccessClass = "subscription"
	AccessPaidAPI      AccessClass = "paid_api"
	AccessTrial        AccessClass = "trial"
	AccessUnknown      AccessClass = "unknown"
)

// Valid reports whether c is a defined access class.
func (c AccessClass) Valid() bool {
	switch c {
	case AccessFree, AccessFreeTier, AccessSubscription, AccessPaidAPI, AccessTrial, AccessUnknown:
		return true
	}
	return false
}

// EligibleForFreeOnly reports whether a route may be used by Free-only routing.
// A free_tier route qualifies only when the selected account/route has a
// verified active allowance. Unknown and unverified classes never qualify.
func (c AccessClass) EligibleForFreeOnly(allowanceVerified bool) bool {
	switch c {
	case AccessFree:
		return true
	case AccessFreeTier:
		return allowanceVerified
	default:
		return false
	}
}

// AccessClassForSignal maps an import classifier signal to an access class.
// Provider-level free flags are metadata, so they map to unknown rather than
// free: the route must be verified before Free-only routing may use it.
func AccessClassForSignal(signal string) AccessClass {
	switch signal {
	case "pricing-zero", "flag-free", "suffix-free", "name-token-free", "display-name-free", "allowlist":
		return AccessFree
	case "pricing-paid":
		return AccessPaidAPI
	default:
		return AccessUnknown
	}
}

// ProviderAccessPolicy is the administrator's explicit statement about how
// Free-only routing treats the models of one provider. The zero value means
// "detect", which is the behaviour before the setting existed.
//
// Nothing but the administrator sets it. Presets, model syncs and imports never
// do, because the provider-level free flag they do set is only metadata.
type ProviderAccessPolicy string

const (
	// AccessPolicyDetect lets each model's own access class decide.
	AccessPolicyDetect ProviderAccessPolicy = "detect"
	// AccessPolicyAllFree declares every model of the provider free.
	AccessPolicyAllFree ProviderAccessPolicy = "all_free"
	// AccessPolicyAllowAll is for a provider that mixes free and paid models:
	// Free-only routing does not filter its models. Detected classes are still
	// recorded and shown, so paid models stay visible as paid.
	AccessPolicyAllowAll ProviderAccessPolicy = "allow_all"
)

// NormalizeProviderAccessPolicy parses a stored or submitted value. An empty
// value is "detect". Anything unrecognised is rejected rather than guessed,
// because a wrong guess here decides whether paid models are allowed.
func NormalizeProviderAccessPolicy(raw string) (ProviderAccessPolicy, bool) {
	switch ProviderAccessPolicy(strings.ToLower(strings.TrimSpace(raw))) {
	case "", AccessPolicyDetect:
		return AccessPolicyDetect, true
	case AccessPolicyAllFree:
		return AccessPolicyAllFree, true
	case AccessPolicyAllowAll:
		return AccessPolicyAllowAll, true
	}
	return AccessPolicyDetect, false
}

// Effective keeps only a policy that changes routing. The default, and any
// value that is not a recognised grant, becomes empty, so records read exactly
// as they did before the setting existed and an unreadable stored value can
// never grant anything.
func (p ProviderAccessPolicy) Effective() ProviderAccessPolicy {
	normalized, ok := NormalizeProviderAccessPolicy(string(p))
	if !ok || !normalized.Grants() {
		return ""
	}
	return normalized
}

// Grants reports whether the policy lets models through Free-only routing
// regardless of their own access class.
func (p ProviderAccessPolicy) Grants() bool {
	return p == AccessPolicyAllFree || p == AccessPolicyAllowAll
}

// FreeOnlyBasis names why a route is, or is not, allowed under Free-only
// routing, so that explanations can say what to change.
type FreeOnlyBasis string

const (
	// BasisClass: the model's own access class decided.
	BasisClass FreeOnlyBasis = "class"
	// BasisManual: the administrator set this model's tier by hand, which
	// outranks the provider setting.
	BasisManual FreeOnlyBasis = "manual"
	// BasisProviderAllFree: the provider is declared free.
	BasisProviderAllFree FreeOnlyBasis = "provider_all_free"
	// BasisProviderAllowAll: the provider is set to allow every model.
	BasisProviderAllowAll FreeOnlyBasis = "provider_allow_all"
)

// GrantsFreeOnly reports whether the basis is a provider-level grant, as opposed
// to a decision made by a class or by a tier the administrator set by hand.
func (b FreeOnlyBasis) GrantsFreeOnly() bool {
	return b == BasisProviderAllFree || b == BasisProviderAllowAll
}

// FreeOnlyDecision is the outcome of the Free-only check for one route.
type FreeOnlyDecision struct {
	Eligible bool
	Basis    FreeOnlyBasis
}

// DecideFreeOnly applies the rule, most specific administrator choice first:
//
//  1. A hand-set model tier decides, and the provider setting is ignored. A
//     model marked paid stays blocked even on an "allow all" provider.
//  2. Otherwise a provider set to "all_free" or "allow_all" allows the model.
//  3. Otherwise the model's own access class decides, as it always has.
func DecideFreeOnly(class AccessClass, allowanceVerified, handSetTier bool, policy ProviderAccessPolicy) FreeOnlyDecision {
	byClass := class.Valid() && class.EligibleForFreeOnly(allowanceVerified)
	if handSetTier {
		return FreeOnlyDecision{Eligible: byClass, Basis: BasisManual}
	}
	switch policy {
	case AccessPolicyAllFree:
		return FreeOnlyDecision{Eligible: true, Basis: BasisProviderAllFree}
	case AccessPolicyAllowAll:
		return FreeOnlyDecision{Eligible: true, Basis: BasisProviderAllowAll}
	}
	return FreeOnlyDecision{Eligible: byClass, Basis: BasisClass}
}
