# RouteNest Phase 5 Final Changed-Files Cleanup

- Parent plan: `.omx/plans/2026-11-07-admin-auth-rbac-sso.md`
- Scope: only files already changed by the Phase 5 follow-up, plus narrowly targeted regression tests
- Behavior contract: preserve fail-closed administrator authentication, RBAC, SSO protocol validation, offline recovery, and provider/account behavior
- Dependency contract: no new dependencies

## Behavior lock

Before cleanup edits:

1. Keep the existing account refresh race regression green.
2. Add a focused race/serialization regression for Cline account failure and success state updates.
3. Keep SSO lifecycle, hostile signed SAML, secret-root, recovery, RBAC, and browser authentication tests unchanged and green.

## Findings and classification

1. `internal/routing/cline_call.go` and `internal/routing/account_health.go` still read or mutate shared account status, cooldown, and usage outside the account-pool synchronization boundary.
   - Classification: boundary violation and missing regression coverage.
   - Action: centralize the smallest semantic account-state snapshots/mutations in `internal/accounts/pool.go`; persist inside the same lock; update changed routing call sites.
2. `internal/adminsso/manager_lifecycle.go` discards periodic refresh errors.
   - Classification: masking fallback slop.
   - Action: after architect review, preserve cancellation as normal shutdown and surface operational failures through a bounded logger callback or explicit retained readiness signal without changing SSO authorization behavior.
3. `internal/adminsso/secret.go` trims file-secret bytes through an immutable string copy.
   - Classification: naming/error-handling and secret-memory hygiene.
   - Action: trim and copy bytes directly, then clear the original buffer.
4. SSO manager/secret-resolver constructors discard cleanup errors after a primary initialization failure.
   - Classification: swallowed cleanup evidence.
   - Action: join cleanup failures with the primary error where doing so remains local and deterministic.
5. The embedded UI duplicates part of the server role map.
   - Classification: deferred boundary duplication, not an authorization bypass because server RBAC is authoritative.
   - Action: do not widen this cleanup into a new session/API contract; retain as a documented follow-up.

## Grounded fail-safes to preserve

- SAML dependency panic recovery remains fail-closed and covered by signed hostile fixtures.
- Windows file-secret references remain rejected until descriptor-safe ACL validation exists.
- Offline recovery remains local-terminal-only, owner checked, securely permissioned, and lifetime-lock protected.
- Compatibility CSP remains explicitly labelled and must not be described as strict while `unsafe-inline` remains.

## Ordered passes

1. Resolve the account synchronization boundary and add its regression test.
2. Resolve the architect-approved periodic refresh error handling.
3. Remove the avoidable secret string copy and preserve cleanup errors.
4. Run `gofmt`, targeted unit/race tests, and changed-file review.
5. Run full test, race, vet, build, security, browser, and static verification from the parent plan.

## Stop condition

Cleanup is complete only when the changed-files review finds no remaining masking fallback in scope, all targeted and full regression gates pass, architecture/security reviewers approve, and no unrelated file is modified.
