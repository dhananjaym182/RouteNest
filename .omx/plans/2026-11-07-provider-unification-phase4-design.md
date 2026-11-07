# Provider Unification Phase 4A/4B Implementation Design

- Status: implemented, independently approved, cleaned, and regression-verified on 2026-11-07
- Readiness boundary: restart-safe single-node operation is supported; production multi-replica enforcement is not configured

## Selected module shape

Use two deep caller modules over one persistence owner:

1. `budget.Store`
   - planner-facing exhaustion snapshots;
   - atomic per-invocation check-and-record admission;
   - idempotent final token accounting;
   - explicit outage policy and redacted health.
2. `providerstate.Store`
   - durable normalized observations;
   - exact-scope revision guards for recovery/reset;
   - persistent expiry/defer behavior;
   - redacted health.

Both modules use facets of one local adapter that serializes every mutation of
`.routenest-providers-sync.json` under the existing provider runtime-state owner. Catalogue,
offering, budget, and observation fields remain in the existing top-level
`providerID -> state` document.

## Why this design

- It matches the approved Phase 4 file matrix and existing caller vocabulary.
- Common callers use business operations instead of storage primitives.
- Atomic checks, UTC windows, idempotency receipts, revisions, expiry, JSON layout, and
  persistence ordering stay hidden behind the interfaces.
- The deterministic shared adapter is a real second adapter for contract/concurrency tests.
- No Redis/Postgres/SQLite dependency is added; production shared-backend selection remains a
  separate dependency decision.

Rejected alternatives:

- One combined runtime interface was smaller, but it collapsed the approved budget and
  availability seams and made compatibility migration harder.
- A broad typed transaction framework was flexible, but exposed more storage vocabulary than
  Phase 4 callers need.

## Invocation and accounting order

1. Resolve provider, credential, transport, and request body.
2. Acquire provider concurrency capacity.
3. Re-check cancellation.
4. Generate a unique server-owned invocation event ID.
5. Atomically check limits and durably record the attempt.
6. Invoke the upstream.
7. Persist normalized observations and final reported token usage with stable event IDs.

Planner snapshots never consume capacity. Resolver/acquire/cancellation failures consume no
capacity. Each credential refresh, connection retry, provider fallback, model fallback,
context retry, and empty-stream retry receives a distinct invocation ID.

Durable admission may conservatively count an invocation when the process crashes after the
commit but before network transmission. RouteNest must not automatically refund that uncertain
attempt. Final token accounting blocks later attempts after observed usage reaches the limit;
it does not promise predictive reservation against concurrent in-flight token overshoot.

## Secure outage behavior

- Empty/unknown policy normalizes to `fail_closed`.
- A provider with configured hard limits is not invoked when durable atomic admission fails.
- `fail_open` requires an explicit acknowledgement field and reports degraded best-effort
  enforcement.
- Read-only planner snapshots may remain available as stale diagnostics, but cannot authorize
  strict dispatch.
- Local file health reports single-node readiness and `multiReplicaSupported: false`.
- Configuring multi-replica topology with only the local adapter never reports enterprise
  readiness.

## Compatibility and migration

- New sync-state fields are optional and deeply cloned.
- Missing sync-state files initialize a new installation; permission/I/O/decode errors do not
  become empty enforcement state.
- Corrupt state follows the existing backup policy and fails durable startup.
- Active legacy provider cooldowns import idempotently as normalized fallback-confidence
  observations before the stores are published.
- Existing provider deletion removes that provider's complete runtime partition.
- Recheck replacement writes preserve runtime fields.
- Existing public helpers remain temporary facades over the new stores; legacy maps stop being
  authorities.

## Implementation lanes

| Lane | Owned files | Result |
| --- | --- | --- |
| Budget module | `internal/budget/*` | Semantic store/backend interfaces, atomic/idempotent tests, shared fake |
| Availability module | `internal/providerstate/*` | Durable backend seam, guarded recovery, two-store/expiry tests |
| Compatibility document | `internal/store/syncstate/*` | Optional runtime DTOs, strict load, deep clone, round-trip tests |
| Local composition | `internal/providers/*`, `internal/app/*` | One local adapter, migration, compatibility facades, startup order |
| Invocation identity | `internal/dispatch/*`, `internal/proxycore/*`, `internal/requestlog/*` | Per-request/per-invocation IDs and exact accounting placement |
| Health/config | `internal/config/*`, `internal/adminapi/*`, `internal/httpapi/*`, admin diagnostics | Secure policy validation and redacted status |

## Stop condition

Phase 4A/4B is complete only when restart, UTC rollover, expiration, recovery races,
idempotency, atomic limits, two-store shared limits/observations, outage policy, legacy cooldown
migration, failed-write rollback, startup order, public/admin health, and local multi-replica
unsupported behavior are covered and every Ralph verification/architecture/cleanup gate passes.
