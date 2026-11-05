# RouteNest Domain Glossary

## Provider Definition

Immutable knowledge about a provider offering: its stable identity, supported
protocol and authentication methods, discoverable capabilities, and default
public metadata. A Provider Definition contains no user credentials or live
health state.

## Provider Instance

One configured upstream that RouteNest can route to. Multiple Provider
Instances may use the same Provider Definition with different credentials,
endpoints, limits, or network policy. Built-in upstreams are Provider Instances
too.

## Credential

Authentication material and identity state used by one or more Provider
Instances. Credentials include API keys, OAuth accounts, refresh tokens,
imported bearer tokens, and provider session identities. RouteNest client keys
are inbound access controls and are not Provider Credentials.

## Logical Model

The model identity requested by a RouteNest client. A Logical Model may be
available from multiple providers.

## Model Offering

One Provider Instance's ability to serve a Logical Model. Access class,
upstream model identity, limits, availability, quota, cooldown, and catalogue
status belong to the Model Offering rather than globally to the model name.

## Availability Observation

A fact learned from an upstream response, catalogue, configured budget, or
health probe. Examples include rate limiting, quota exhaustion, a known reset,
expired authentication, model removal, transient failure, and recovery.

## Cooldown

A temporary period in which a Provider, Credential, Model Offering, or Egress
Endpoint is ineligible. A CooldownUntil value does not by itself prove an exact
quota reset time.

## Quota Reset

A confirmed or inferred time when a quota window becomes available again. A
Quota Reset has evidence and confidence; unknown reset information remains
unknown.

## Egress Profile

A named outbound-network policy containing proxy endpoints and their selection
behavior. Provider Instances can inherit the global profile, choose another
profile, or explicitly connect directly.

## Free-first Routing

Candidate selection that prefers eligible free Model Offerings across all
providers. Unknown or unverified access never qualifies as free.

## Compatibility Stores

During the behavioral migration the existing JSON files remain the sources of
truth: `.routenest-accounts.json` (Cline accounts, models, and admin state),
`.routenest-connections.json`, `.routenest-providers.json` with
`.routenest-providers-cooldowns.json`, `.routenest-zen.json`,
`.routenest-proxy.json`, and `.routenest-providers-sync.json`.

Global Provider Instances, Credentials, Model Offerings, Availability
Observations, and Egress Profiles are projections composed from those stores by
`internal/app`; they do not yet own a separate schema. The files are
compatibility stores, not deprecated leftovers: routing and administration keep
reading and writing them, and every legacy endpoint remains a supported adapter
over the same data. Consolidating them is deferred to a separately approved
migration so behavior changes and data migration cannot regress together.
