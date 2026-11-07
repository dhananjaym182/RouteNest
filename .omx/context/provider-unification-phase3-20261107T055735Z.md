# Provider Unification Phase 3 Context Snapshot

## Task statement

Continue the approved provider-unification program with Phase 3 only: remove HTTP-ingress assumptions that the whole gateway is unavailable when Cline has no usable accounts.

## Desired outcome

- Chat Completions, Anthropic Messages, and Responses reach the unified candidate planner without a Cline account-pool prerequisite.
- Missing Cline credentials exclude only the Cline candidate with `credential_missing`.
- Custom-provider-only and anonymous/free-Zen deployments can serve requests with zero Cline accounts.
- A strict Cline pin with no credential returns provider-neutral availability, not inbound authentication failure.
- Health may show zero active Cline accounts while remaining ready when another provider is eligible.

## Known facts and evidence

- Phases 1 and 2 are complete and independently approved.
- Phase 2 handlers now enter `startUnifiedDispatch`; direct provider-specific calls are absent from production HTTP handlers.
- `routing.BuildCandidateSnapshot` already marks Cline as credential-required, so the planner can emit `credential_missing` without rejecting other providers.
- The worktree is heavily dirty with completed Phase 1/2 and unrelated admin UI work; all existing changes must be preserved.
- The approved plan is `.omx/plans/2026-11-07-provider-unification-enterprise-hardening.md`, Phase 3.

## Constraints

- Do not begin Phase 4 persistence/backend work.
- Preserve public OpenAI, Anthropic, and Responses error envelopes.
- Preserve `legacy` and `shadow` rollback behavior where required, but do not reintroduce a permanent provider-neutral Cline prerequisite.
- Keep startup pre-warm and account counts as Cline-specific diagnostics only.
- Use small `apply_patch` edits, no new dependencies, and do not revert unrelated dirty files.

## Unknowns to resolve from current code

- Whether any handler constructor or server registration still receives or checks `activeAccounts`.
- Whether no-candidate errors consistently map to availability status for every ingress protocol.
- Whether provider-only and Zen-only fixtures already exist or need a new integration file.
- Whether health readiness currently derives from Cline account count rather than composed provider availability.
- Whether legacy/shadow modes need an explicit compatibility exception or should be restricted for provider-only deployments.

## Likely codebase touchpoints

- `internal/proxycore/chat_handler.go`
- `internal/proxycore/anthropic_handler.go`
- `internal/proxycore/responses.go`
- `internal/proxycore/server.go`
- `internal/proxycore/unified_dispatch.go`
- `internal/routing/snapshot.go`
- integration/provider-only ingress tests
- health/readiness tests and current health owner

## Verification and stop condition

Phase 3 stops only when provider-only Chat, Messages, Responses, streaming, Zen-only, strict-Cline-pin, no-provider availability, and zero-account readiness behavior are covered; focused and full tests, race tests, vet, build, static ingress-gate checks, post-deslop regression, and independent architecture review all pass. Phase 4 remains pending.
