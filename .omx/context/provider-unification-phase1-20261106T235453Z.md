# Ralph context: provider unification phase 1

- Task: implement Phase 1 from `.omx/plans/2026-11-07-provider-unification-enterprise-hardening.md`.
- Desired outcome: custom-provider model state is keyed by `(providerID, modelID)` so pricing, tier signals, delisting, disabling, limits, and capabilities cannot leak between providers sharing a model ID.
- Stop condition: Phase 1 acceptance criteria pass with targeted tests, existing model-sync popup checks remain green, changed files receive architecture and cleanup review, and no unrelated dirty-worktree edits are reverted.

## Known facts and evidence

- `internal/models/provider.go` currently applies import decisions and delisting by model ID.
- `internal/models/offerings.go` projects a shared legacy model row into every custom provider claiming that ID.
- `.routenest-providers.json` owns provider declarations through `CustomProvider.ModelIDs`.
- `.routenest-providers-sync.json` already stores provider-keyed sync state and can accept additive optional fields without changing its top-level shape.
- The accepted ADR keeps existing JSON files as authoritative compatibility sources during behavioral migration.
- Existing admin UI/model-sync popup edits and tests are uncommitted and must be preserved.

## Constraints

- Implement Phase 1 only in this delivery slice.
- No destructive git operations and no unrelated worktree cleanup.
- No new dependency.
- Preserve legacy JSON readability and legacy seed behavior.
- Provider-specific mutations must require provider identity.
- Keep `AccountPool.Models` as a compatibility logical-model projection.

## Unknowns to resolve from code/tests

- The smallest API change that moves provider imports, delisting, and manual tier updates to provider-scoped state without breaking legacy admin callers.
- Whether optional provider offering state should live directly in `syncstate.State` or behind a separate in-package document wrapper.
- Which current tests assert ID-wide behavior and need provider-aware replacements.

## Likely touchpoints

- `internal/domain/model_offering.go`
- `internal/domain/model_offering_state.go` (new)
- `internal/store/syncstate/store.go`
- `internal/providers/offering_state.go` (new)
- `internal/providers/offering_projection.go`
- `internal/providers/model_import.go`
- `internal/providers/model_sync.go`
- `internal/models/offerings.go`
- `internal/models/provider.go`
- `internal/admin/model_import_api.go`
- provider/model/admin tests and existing popup verification tools
