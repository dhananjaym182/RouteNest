# Provider Unification Phase 4A/4B Context Snapshot

## Task statement

Implement the approved provider-unification Phase 4A and Phase 4B plan: make provider
budgets and availability observations restart-safe on one node, define the shared-backend
contracts required for multi-replica enforcement, expose backend health, and preserve all
existing provider and protocol behavior.

## Desired outcome

- Daily request counts, token usage, RPM windows, and normalized availability observations
  persist in the existing provider-keyed `.routenest-providers-sync.json` file.
- State loads before the first request is accepted, expired entries are purged, and legacy
  provider cooldowns are projected into normalized observations during the compatibility
  window.
- One budget store owns check-and-record dispatch accounting and token accounting, including
  stable idempotency keys and UTC daily/monthly windows.
- Small budget and availability backend interfaces support a local atomic-file adapter and a
  deterministic shared adapter used by two-store/concurrency tests.
- The local file adapter reports single-node readiness but explicitly reports multi-replica
  enforcement as unsupported.
- Backend outages follow an explicit secure policy: fail closed after stale last-good state by
  default; fail open is allowed only through acknowledged configuration.

## Known facts and evidence

- Phases 1-3 are complete and independently approved.
- `internal/providers/budget.go` currently owns process-local RPM, daily request, and token
  maps protected by one mutex.
- `internal/providerstate/store.go` currently owns an in-memory observation cache.
- `internal/store/syncstate/store.go` already atomically persists provider-keyed sync and
  offering state and is the approved additive compatibility file.
- `internal/providers/cooldown.go` persists legacy provider/model cooldowns separately and
  already projects new cooldown changes into providerstate.
- The repository currently has no Redis, Postgres, etcd, or SQLite dependency. The approved
  plan explicitly defers production shared-backend technology selection.
- The Phase 4 acceptance criteria require a deterministic fake shared backend and explicit
  unsupported multi-replica reporting while the local file adapter is configured; they do not
  authorize adding an unselected production database dependency.

## Constraints

- Preserve the heavily dirty worktree and all Phase 1-3/admin/UI changes.
- Do not add a production database dependency without a separate dependency decision.
- Preserve the top-level `providerID -> state` JSON shape and make all new fields optional.
- Keep one persistence owner and atomic writes; do not create a second runtime-state file.
- Count actual upstream attempts, not planner candidates.
- Never double-count replayed dispatch or token-accounting events.
- Do not classify the local file adapter as multi-replica enterprise-ready.
- Do not expose secrets, raw credentials, or proxy URLs in health responses.

## Unknowns to resolve

- The smallest backend interfaces that preserve atomic check-and-record semantics without
  leaking file layout to callers.
- The stable request/attempt and token-accounting idempotency keys available across Chat,
  Messages, Responses, retries, streams, and legacy execution.
- The exact composition order needed to load durable state before routing, credentials, and
  dispatch are published.
- How corrupt-file backup/recovery should affect readiness and startup.
- Where backend health belongs in the existing health/admin diagnostics without duplicating
  provider-health ownership.

## Likely codebase touchpoints

- `internal/budget/backend.go`
- `internal/budget/store.go`
- `internal/providers/budget.go`
- `internal/providerstate/backend.go`
- `internal/providerstate/store.go`
- `internal/store/syncstate/store.go`
- `internal/app/runtime_state_compat.go`
- `internal/app/provider_state_compat.go`
- `internal/app/wiring.go`
- `internal/providers/client.go`
- `internal/app/dispatch_compat.go`
- `internal/dispatch/types.go`
- `internal/httpapi/auth.go`
- focused budget, providerstate, syncstate, app, health, and integration tests

## Verification and stop condition

Stop only when restart, rollover, expiration, recovery, idempotency, atomic-limit concurrency,
two-store shared-backend, outage-policy, legacy-cooldown migration, startup-order, health, and
unsupported-multi-replica behavior are covered; focused/full/race/vet/build/static/diff checks
pass; independent architecture review approves; the changed-files cleanup pass and post-cleanup
verification pass; and the enterprise-hardening plan records Phase 4A/4B completion without
claiming a concrete shared production backend.
