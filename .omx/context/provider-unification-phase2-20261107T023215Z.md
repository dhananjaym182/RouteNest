# Provider Unification Phase 2 Context Snapshot

## Task statement

Continue the approved provider-unification program with Phase 2 only: put one shared routing planner and one shared executor on the production path for Chat Completions, Anthropic Messages, Responses, named combos, and fallback chains.

## Desired outcome

- `routing.PlanCandidates` is the only automatic provider-ordering policy.
- `routing.PlanFallbackChain` is the only logical-model fallback-chain expansion policy.
- One provider-neutral executor owns attempt ordering, retry/error classification, tracing, credential/provider failover, and the stream-commit fallback boundary.
- Chat, Messages, Responses, and combos use the same candidate order for identical policy inputs.
- Named combos preserve configured entry order and explicit provider pins.
- A temporary `legacy`, `shadow`, and `enforce` dispatch mode supports reversible rollout without duplicate upstream requests in shadow mode.
- Public protocol envelopes, streaming event order, request logging, compaction hooks, aliases, free-only behavior, provider pins, and current persistence formats remain compatible.

## Known facts and evidence

- Phase 1 is complete and verified; provider-scoped offering state is keyed by `(providerID, modelID)` and supports isolated pricing/delisting, legacy migration, restart persistence, scoped deletion, and provider-specific popup behavior.
- The approved implementation plan is `.omx/plans/2026-11-07-provider-unification-enterprise-hardening.md`, Phase 2.
- `routing.PlanCandidates` and `routing.PlanFallbackChain` currently have test callers but no production callers.
- Production HTTP handlers still directly call provider-specific paths:
  - `internal/proxycore/chat_handler.go`: `combos.RunCombo`, `zen.Call`, `routing.CallClineAPI`
  - `internal/proxycore/anthropic_handler.go`: the same provider-specific branches
  - `internal/proxycore/responses.go`: the same provider-specific branches
- `internal/combos/combos_execution.go` directly invokes Zen, custom-provider, and Cline paths.
- Existing provider adapters and conformance tests are present under `internal/provideradapters`.
- The worktree is heavily dirty with Phase 1 and unrelated UI/provider changes; those changes must be preserved.

## Constraints

- Do not reset, revert, overwrite, or broadly reformat unrelated dirty-worktree changes.
- Use small `apply_patch` edits and no new dependencies.
- Add characterization/regression tests before production cutover.
- Preserve public protocol response/error envelopes and streaming event order.
- Preserve combo order, explicit provider pins, automatic free-first routing, compaction behavior, and existing JSON stores.
- Never retry a different provider after any client-visible stream bytes are committed.
- Shadow mode compares plans/diagnostics only and must not duplicate upstream requests.
- Keep Phase 3 account-gate removal out of scope except compatibility seams explicitly required by Phase 2.
- Do not claim the full system is unified or enterprise/multi-replica ready; later phases remain required.

## Unknowns and open questions to resolve from the code

- Exact normalized request/result boundary that can serve all three ingress protocols without moving protocol formatting into dispatch.
- Which existing adapter request fields already carry upstream model ID, protocol, credential, egress, and session identity, and which minimal extensions are required.
- Existing error types/status handling that can be reused for retry classification without behavior drift.
- Current configuration ownership and safest additive location for the dispatch mode.
- How combo entries and configured fallback chains are represented at runtime and how to encode their order/group metadata in `CandidatePlan`.
- How current handlers detect first committed stream bytes and how that signal should cross the unified executor boundary.
- Which legacy credential/account and provider-resolution helpers can be composed in `internal/app/dispatch_compat.go` without import cycles.

## Likely codebase touchpoints

- `internal/routing/candidates.go`
- `internal/routing/snapshot.go` (new)
- `internal/dispatch/types.go` (new)
- `internal/dispatch/executor.go` (new)
- `internal/app/dispatch_compat.go` (new)
- `internal/app/wiring.go`
- `internal/provideradapters/adapter.go` and adapter implementations
- `internal/proxycore/unified_dispatch.go` (new)
- `internal/proxycore/chat_handler.go`
- `internal/proxycore/anthropic_handler.go`
- `internal/proxycore/responses.go`
- `internal/combos/combos_execution.go`
- Compatibility helpers in `internal/routing`, `internal/providers`, and `internal/zen`
- Characterization and integration tests under `internal/routing`, `internal/dispatch`, `internal/proxycore`, and `internal/integration`

## Verification and stop condition

Phase 2 stops only when targeted planner/dispatch/adapter/proxy/integration tests, race tests, full Go tests, vet, build, static production-path checks, post-deslop regression, and an architect review all pass; the Phase 2 plan/status and Ralph completion audit must then be updated. Phase 3 remains pending.
