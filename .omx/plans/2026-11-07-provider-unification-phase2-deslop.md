# Phase 2 Changed-Files Cleanup Plan

- Mode: `oh-my-codex:ai-slop-cleaner` standard pass inside the active Ralph workflow
- Scope: Phase 2 provider-unification files only; preserve unrelated Phase 1 and admin UI changes
- Behavior lock: focused Phase 2, full integration, full repository, race, vet, build, static call-site, and diff checks passed before cleanup

## Fallback classification

| Fallback-like path | Classification | Action |
| --- | --- | --- |
| `legacy` and `shadow` dispatch executor | Grounded compatibility fallback for reversible rollout | Preserve; enforce remains planner/executor-owned and fail-closed |
| Shadow planner-error tolerance | Grounded rollout compatibility | Preserve; tested separately from enforce behavior |
| Request-local unknown Cline offering projection | Grounded compatibility boundary | Preserve; it is non-persistent and excludes Zen/custom collisions |
| Pre-content semantic stream fallback | Grounded fail-safe behavior | Preserve; stop after semantic commit |
| `fallback-direct` egress transport inheritance | Grounded compatibility/test seam | Preserve; explicit `provider-direct` remains isolated |
| Provider-state observation write failures | Best-effort telemetry/state projection | Preserve for Phase 2; do not turn successful upstream responses into failures |

No masking fallback, swallowed request failure, bypass of provider eligibility, or duplicate upstream path remains in the Phase 2 production handlers.

## Cleanup passes

1. Dead code deletion
   - Remove unused request fields that never cross the executor/resolver boundary.
   - Remove planner/attempt metadata copied but never consumed or persisted.
   - Remove unused diagnostics fields.
2. Duplicate removal
   - Keep requested-model/combo/exclusion attribution in the single plan-metadata projection path.
3. Naming and error handling
   - Retain explicit raw dispatch outcomes alongside UI-compatible attempt outcomes.
   - Preserve concrete terminal stream-retry errors in request logs.
4. Test reinforcement
   - Update affected planner/snapshot tests and rerun all Phase 2 gates.

## Stop condition

No behavior changes beyond removal of unused metadata; all targeted, full, race, vet, build, static call-site, and diff checks pass after cleanup.

## Result

- Resolution gate: all fallback-like paths in scope are grounded compatibility or fail-safe behavior; none were removed or broadened.
- Pass 1: removed unused request, planner, candidate, and attempt metadata.
- Pass 2: removed duplicate requested-model attribution.
- Pass 3: retained structured planner exclusions, routing identity, raw dispatch outcomes, and concrete terminal retry errors.
- Pass 4: focused, full, race, vet, build, static call-site, and diff checks passed.
- Independent cleanup review: approved with no behavior or Phase 2 acceptance regression.
- Remaining risk: Zen still resolves its actual proxy through the legacy mutable transport, so a snapshotted egress identity can diverge from the endpoint used. Deferred outside Phase 2.
