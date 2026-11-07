# Admin Authentication, RBAC, and SSO Context Snapshot

- Created: 2026-11-07T09:31:10.977Z
- Checkpoint: `441b044` pushed to `routenest/main`
- User request: continue the deferred login/password/RBAC/SSO work after checkpointing all current changes

## Desired outcome

RouteNest must fail securely: a fresh or externally exposed admin console cannot run anonymously; local/bootstrap credentials are one-time and require rotation; password/session state survives restart without plaintext secrets; state-changing browser requests are CSRF-protected; permissions are enforced server-side; login attempts and security changes are audited; and optional enterprise SSO can map trusted identity claims to RouteNest roles without bypassing local break-glass controls.

## Verified current behavior

- `internal/adminapi/auth.go` lets every admin request pass when no password is configured.
- Existing password storage is `SHA-256(salt + password)` in `.routenest-accounts.json`, with a timestamp fallback if secure salt generation fails.
- The password endpoint accepts an empty password to restore anonymous access; the four-character minimum exists only in browser JavaScript.
- Sessions are anonymous raw bearer tokens in process memory. They disappear on restart and carry no user, role, CSRF token, authentication method, or audit identity.
- The session cookie is HttpOnly and SameSite=Lax, but is not transport-aware and has no Secure flag on externally terminated HTTPS.
- Login uses only a fixed 500 ms delay. There is no bounded per-source/account rate limiter.
- All admin routes share one binary authenticated/unauthenticated middleware; there is no server-side RBAC.
- Browser mutations send no CSRF token.
- Non-loopback listeners are allowed with a warning, even when admin auth is absent and no trusted TLS/reverse-proxy contract is configured.
- The existing `/admin/api/sso/import` imports Cline/WorkOS account cookies and is not administrator identity SSO.
- A Security page and login overlay already exist in the embedded admin UI.
- `golang.org/x/crypto` is already present; no OIDC or SAML administrator identity dependency exists.

## Security constraints

- Never ship or commit a universal default password.
- Bootstrap secret comes from `ROUTENEST_ADMIN_BOOTSTRAP_PASSWORD` or is generated cryptographically once and printed only to the local console.
- A generated/environment bootstrap credential is one-time and must be rotated before normal administration.
- Cryptographic RNG failure is fatal for credential/session generation; never fall back to time or empty tokens.
- Migrate legacy salted-SHA256 passwords after successful verification; do not lock out existing installations.
- Persist only slow password hashes, hashed session identifiers, and required metadata. Never persist plaintext passwords or raw session tokens.
- Non-loopback administration requires a secure transport/trusted reverse-proxy policy and configured authentication.
- Break-glass local access remains explicit, rate-limited, auditable, and independently revocable.
- Authorization is enforced in Go handlers/middleware, never only in the UI.
- Preserve the single-binary embedded UI and compatibility data migration behavior.

## Likely codebase touchpoints

- `internal/adminapi/auth.go`, `auth_handlers.go`, and tests
- new deep auth/security module and atomic persistence store under `internal/adminauth` and/or `internal/store/adminauth`
- `internal/admin/admin.go` route registration and permission mapping
- `internal/domain/account.go` legacy hash migration only
- `internal/proxycore/server.go`, `main.go`, and `desktop_main.go` startup/listener safety
- `internal/platform/data_path.go` known state files
- embedded admin Security page, login dialog, shell API wrapper, i18n, UI contract tests
- state JSON golden/compatibility tests
- OIDC/SAML adapters and configuration after dependency review

## Open implementation decisions

1. Persist security state in a dedicated `.routenest-admin-auth.json` owner versus extending the already overloaded accounts file.
2. Role/permission vocabulary and route-to-permission mapping.
3. Session persistence/rotation model and CSRF token transport.
4. Trusted reverse-proxy/TLS contract for the shared admin/API listener.
5. OIDC and SAML package selection, metadata/config shape, group/claim role mapping, and callback paths.

## Stop condition

The phase is complete only when secure bootstrap, forced rotation, slow password hashing and legacy migration, durable revocable sessions, CSRF, throttling, audit events, server-side RBAC, optional OIDC/SAML login, break-glass behavior, non-loopback transport enforcement, UI flows, migration compatibility, focused/full/race tests, build/vet/static security guards, cleanup, and independent architecture/security review all pass.

