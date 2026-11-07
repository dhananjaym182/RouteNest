# Provider Unification Phase 3 Cleanup Plan

## Scope

Only the Phase 3 files changed during the active Ralph iteration:

- `internal/integration/provider_only_ingress_test.go`
- `internal/integration/free_model_test.go`
- `internal/proxycore/unified_dispatch_test.go`
- `internal/proxycore/proxy.go`
- `internal/proxycore/chat_handler.go`
- `internal/proxycore/server.go`
- `internal/httpapi/auth.go`
- `internal/routing/dispatch.go`

Unrelated dirty admin, UI, provider-catalogue, and earlier phase changes are out of scope.

## Behavior lock

The focused Phase 3 suite covers zero-Cline-account custom-provider ingress for Chat,
Messages, and Responses in streaming and non-streaming modes; Zen-only ingress; strict
Cline pins; no-eligible-provider availability; health readiness; and wrapped availability
status mapping. Before cleanup, the focused suite, `go test ./...`, the selected race suite,
`go vet ./...`, `go build ./...`, static migration guards, and `git diff --check` pass.

## Cleanup order

1. Resolve fallback-like findings.
2. Delete dead code only if the changed delta introduced any.
3. Remove duplication only where it improves the changed delta without widening scope.
4. Clean names and error handling without changing public envelopes.
5. Reinforce the wrapped availability test across both availability error classes and
   Zen/non-Zen model families.
6. Re-run the focused and full Phase 3 verification commands.

## Fallback classification

- `providerUnavailable` is not a fallback path; it is the provider-neutral classification
  boundary for `routing.FreeModelUnavailableError` and `dispatch.ErrExhausted`.
- The legacy dispatch exits for an unavailable pinned provider and an empty Cline pool now
  return the same typed availability error instead of an unclassified plain error. This is
  boundary repair, not a new fallback path.
- Existing model, stream, and compatibility fallbacks elsewhere in the scoped files predate
  Phase 3, retain failure evidence, and are covered by existing tests. They are grounded
  compatibility/fail-safe behavior and will not be refactored in this bounded pass.
- No masking fallback, swallowed error, silent default, temporary bypass, or Phase 4 shim was
  introduced by the Phase 3 delta.

## Stop condition

Stop when the scoped smell passes are complete, the independent architect approves the
Phase 3 behavior, post-cleanup focused/full/race/vet/build/static/diff verification is green,
and no file outside the listed scope was modified by the cleanup.

## Result

- Resolution gate: no masking fallback, swallowed request failure, silent default, or
  duplicate production path remains in the Phase 3 delta.
- Pass 1, dead code: no Phase 3 dead code remained after removing the handler account-count
  dependency.
- Pass 2, duplication: wrapped availability checks are table-driven across both error types,
  Zen/non-Zen models, and both HTTP status helpers.
- Pass 3, naming and error handling: legacy no-provider and empty-account exits reuse the
  existing typed availability error; upstream auth, request, policy, and provider failures
  retain their original classification.
- Pass 4, test reinforcement: provider-only, Zen-only, strict-pin, and health cases exercise
  the real default `shadow` configuration; no-provider status is covered in `shadow`,
  `legacy`, and `enforce` across all three ingress protocols.
- Independent post-fix architecture review: approved with no blocking findings.
- Quality gates: focused tests, full tests, selected race suite, vet, build, static ingress
  guards, and diff hygiene all pass after cleanup.
- Remaining risk: Phase 4A restart-safe state and Phase 4B multi-replica backend work remain
  required before enterprise-readiness claims.
