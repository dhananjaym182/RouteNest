# Phase 4A/4B Changed-Files Cleanup Plan

- Status: completed on 2026-11-07

## Scope

Only Phase 4 runtime-state work:

- `internal/budget/**`
- `internal/providerstate/**`
- `internal/store/syncstate/**`
- Phase 4 runtime composition, accounting, health, cooldown compatibility, and their tests under
  `internal/app`, `internal/providers`, `internal/dispatch`, `internal/requestlog`,
  `internal/httpapi`, `internal/admin`, `internal/adminapi`, `internal/config`,
  `internal/domain`, and `internal/proxycore`
- Phase 4 contract golden updates under `internal/integration/testdata`

Do not edit the unrelated dirty admin UI, provider setup, routing inspector, or earlier
offering/import implementation.

## Behavior lock

- Focused Phase 4 packages pass.
- Phase 4 race suite passes.
- Full `go test ./...`, `go vet ./...`, and `go build ./...` pass.
- The 20 delisting controller tests and 122 browser checks pass.
- Independent architecture verification approved the implementation.

## Fallback inventory and classification

- Acknowledged `fail_open`: grounded fail-safe policy; explicit config acknowledgement,
  degraded health, and outage tests are retained.
- Cached planner snapshots: grounded diagnostic fallback; they never authorize a hard-limited
  invocation and strict admission rechecks the backend.
- Legacy provider cooldown import/reset: grounded compatibility bridge; active entries import
  idempotently and resets persist normalized recovery.
- `RecordDispatch` and process-local `providerstate.NewStore`: grounded compatibility/test
  surfaces; production composition publishes durable stores.
- Legacy/shadow dispatch modes: grounded reversible rollout boundary from the approved plan;
  not broadened in this cleanup.
- Ignored errors in historical void/int compatibility methods: retained only where the public
  signature cannot return an error; store health/`LastError` preserves failure evidence.

No masking fallback is accepted.

## Pass order

1. Remove dead/debug leftovers in Phase 4-owned changes.
2. Remove local duplication where it improves the existing boundary without a new abstraction.
3. Tighten names/comments/error handling, especially startup failure classification.
4. Add or retain the narrow regressions required for any cleanup change.

## Expected edits

- Remove redundant static outage-policy plumbing when a live source is already configured.
- Consolidate repeated local-backend startup failure state changes.
- Remove stale comments or unused compatibility wording exposed by the durable-store cutover.

## Stop condition

No unrelated files changed; targeted tests, full tests, race, vet, build, static guards, and
admin model-sync checks all pass again after cleanup.

## Completion report

The changed-files-only cleanup removed redundant Phase 4 plumbing and tightened startup/error
handling without changing the approved runtime-state contracts. No unrelated admin UI,
provider setup, routing inspector, or earlier offering/import work was reverted.

Fresh post-cleanup evidence:

- `go test ./...` passed.
- `go test -race ./internal/providers ./internal/budget ./internal/providerstate ./internal/dispatch` passed.
- `go vet ./...` and `go build ./...` passed.
- Centralized-dispatch and legacy-budget static guards passed.
- All 20 model-delisting controller cases passed.
- The final browser verification passed 122 checks across 20 cases with zero forbidden writes and zero browser diagnostics.
- Independent architecture review approved the implementation with no critical or high findings.

Readiness remains deliberately bounded: the local compatibility adapter is restart-safe for a
single node and reports multi-replica enforcement as unsupported. Phase 4B proves the atomic
shared-backend contract with a deterministic shared adapter; it does not configure a production
multi-replica backend.
