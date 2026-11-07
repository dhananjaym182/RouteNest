# Phase 5 changed-files cleanup plan

## Scope

Only files changed by the admin authentication, RBAC, SSO, recovery, transport-security, integration-fixture, dependency-security, and account-race work in the active Ralph session.

## Behavior lock

- Real Chromium bootstrap/login/CSRF/viewer-RBAC flow passes with no JavaScript errors.
- `go test ./...` and `go test -race ./...` pass under Go 1.26.10.
- Focused admin auth, SSO, recovery, store, and parser-hostility tests pass.
- Vet, CLI/desktop builds, cross-builds, i18n checks, health smoke, and static guards pass.

## Cleanup inventory and order

1. Repair the incomplete account-state synchronization boundary and add deterministic failure/success race regressions.
2. Surface periodic SSO refresh failures instead of silently discarding them.
3. Remove avoidable immutable secret copies and preserve constructor cleanup errors.
4. Remove test-only or duplicate helpers where that does not widen the production contract.
5. Re-run the complete verification matrix after cleanup.

## Fallback classification

- SAML parser panic recovery: grounded fail-closed boundary; preserve with hostile signed fixtures.
- Windows file-secret rejection: grounded platform fail-closed behavior; preserve.
- Offline recovery ownership/TTY/store-lock checks: grounded break-glass boundary; preserve.
- Periodic SSO refresh error discard: masking fallback; replace with explicit operational evidence.

## Deferred boundary

The browser role-to-permission visibility map duplicates the canonical server map. Server RBAC remains authoritative, so this is not an access-control bypass. A later contract change should return derived permissions with the principal and remove the client mapping; do not widen this changed-files cleanup into that API migration.

## Stop condition

No unresolved critical/high review finding, no masking fallback in changed files, no avoidable cleanup regression, and all post-cleanup tests/builds/security checks green.
