# ADR 0001: Global provider capability system

- Status: Accepted
- Date: 2026-11-04

## Context

RouteNest grew from a Cline-focused gateway. Cline accounts, OpenCode Zen,
OAuth/token connections, custom providers, model catalogues, quotas, cooldowns,
and proxy settings consequently have different runtime and administration
paths. The product goal is a provider-neutral, free-first gateway without
losing the strongest existing Cline and Zen behavior.

The persisted JSON files and public/admin contracts are already used in
production. Replacing their schemas while moving behavior would combine two
high-risk changes and make regressions difficult to diagnose.

## Decision

RouteNest will introduce global Provider Definitions, Provider Instances,
Credentials, Model Offerings, Availability Observations, and Egress Profiles.

Provider-specific wire, authentication, discovery, quota, and session behavior
will live behind explicitly composed adapters. Invocation is the small required
interface; optional capabilities use separate interfaces rather than one large
Provider interface.

Existing stores remain compatibility adapters during the behavioral migration.
Generic/custom providers migrate first, OpenCode Zen second, and Cline last.
Old and new routing outcomes remain comparable until each lane reaches parity.

Free detection and catalogue reconciliation operate per Model Offering.
Quota-reset evidence remains distinct from fallback cooldown duration.
Unsupported and unknown capabilities are represented explicitly.

Core modules point inward. `providerstate`, `egress`, and `credentials` do not
import orchestration owners such as `routing`, `providers`, or `zen`; legacy
adapters are composed by `internal/app`.

## Consequences

- Cline and OpenCode Zen become built-in Provider Instances instead of special
  top-level systems.
- Provider fixes become local to adapters and their conformance fixtures.
- Global routing can compare offerings across all providers.
- Existing persistence and endpoints remain available during migration.
- Temporary compatibility adapters increase code until parity is proven.
- A later storage consolidation remains possible but is not required by this
  refactor.

## Rejected alternatives

### UI-only unification

Rejected because it would preserve separate runtime systems behind a common
screen and continue duplicating routing, quota, and cooldown behavior.

### One giant Provider interface

Rejected because most providers cannot implement every authentication, quota,
catalogue, session, and compaction capability meaningfully.

### Big-bang persisted-schema migration

Rejected because behavior ownership and data migration would fail together and
remove the ability to compare old and new execution paths.

### One mutable runtime file per provider

Rejected because it complicates atomic routing changes, secret management,
backup, locking, and schema migration. Immutable definitions may remain one
file per provider; mutable state stays organized by concern.
