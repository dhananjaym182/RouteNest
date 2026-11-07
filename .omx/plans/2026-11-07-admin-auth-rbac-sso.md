# RouteNest Phase 5 — Admin Identity, RBAC, SSO and Transport Security Plan

- Date: 2026-11-07
- Status: final consensus candidate after Planner, Architect and Critic iteration
- Baseline checkpoint: 441b044
- Context: .omx/context/admin-auth-rbac-sso-20261107T093110Z.md
- Readiness boundary: production-grade single-node admin identity; multi-replica identity/session/SSO remains unsupported until a shared transactional backend exists

## Outcome

RouteNest must never expose an anonymous administration plane because configuration is missing. Local break-glass login, OIDC and SAML must produce one durable principal/session model with explicit permissions, CSRF protection, trusted transport context, throttling and auditable security mutations.

SSO is complete only when protocol verification, replay prevention, secret rotation behavior, role mapping and hostile end-to-end fixtures pass. Interfaces, DTOs, configuration scaffolding or disabled buttons do not count.

## Existing failures being removed

- Password-not-set bypass: internal/adminapi/auth.go:83-102.
- In-memory anonymous sessions: internal/adminapi/auth.go:22-71.
- Fast SHA-256 password authority and RNG empty fallback: internal/adminapi/auth.go:130-161.
- Empty-password clearing and fixed-delay-only login rejection: internal/adminapi/auth_handlers.go:39-111.
- Binary auth wrapper without permissions: internal/admin/admin.go:111-260.
- Timestamp salt fallback: internal/admin/admin.go:263-325.
- Persist-before-rebind listener changes: internal/adminapi/config.go:329-367.
- Non-loopback warning instead of enforcement: internal/proxycore/server.go:44-72 and 248-285.
- Wildcard CORS on authenticated admin routes: internal/httpx/respond.go:47-61 and internal/admin/admin.go:111-139.
- No browser CSRF token: internal/admin/ui/js/03_shell.js:1-59.
- UI advertises anonymous-by-default and password removal: internal/admin/ui/html/pages/security.html:32-70.

## RALPLAN-DR

### Principles

1. Missing or corrupt security configuration fails closed.
2. Authentication, authorization and transport context have explicit owners.
3. No universal password, plaintext persisted secret or HTTP recovery backdoor.
4. Every route, secret reference and SSO provider has a verifiable readiness state.
5. Local audit is tamper-evident and is never described as tamper-proof.

### Decision drivers

1. Secure upgrade without locking out existing users or reopening anonymous downgrade.
2. Durable revocation, role changes and audit under crashes.
3. Protocol-correct OIDC/SAML with minimal dependency ambiguity.

### Viable options considered

#### Option A — Dedicated adminauth subsystem, selected

Create a dedicated security service and strict store, separate from gateway account credentials.

Pros:

- One owner for users, roles, sessions, bootstrap, SSO, CSRF and audit.
- Fail-closed corrupt-state handling.
- Clean migration and future shared-backend seam.
- No security mutation rewrites upstream refresh tokens.

Cons:

- Adds two security state files plus a lock.
- Requires one-way legacy password migration.
- Larger initial implementation surface.

#### Option B — Extend .routenest-accounts.json

Pros:

- Fewer files.
- Existing atomic-save helper.

Cons:

- Mixes upstream credentials with administrator identity.
- Inherits unsafe empty-state behavior and log-only save failures.
- High-frequency session/security writes rewrite unrelated credentials.
- Harder rollback and shared-backend evolution.

Rejected.

#### Option C — Stateless JWT or signed-cookie sessions

Pros:

- Minimal server-side session storage.
- Easier validation across replicas.

Cons:

- Immediate revocation and role revision are complex.
- Requires durable signing-key lifecycle.
- Weak fit for forced logout and bootstrap recovery.

Rejected for the primary implementation.

## ADR

### Decision

Use one dedicated adminauth.Service with a durable snapshot/hash-chained journal, explicit route registry, canonical request-security context and unified sessions for local, OIDC and SAML principals. Raise the Go baseline to 1.26 for the hardened SAML dependency.

### Drivers

- Fail closed under missing config, crashes, downgrade, route omissions and audit failure.
- Preserve local offline recovery without a remote reset backdoor.
- Keep protocol authentication separate from authorization/session semantics.

### Alternatives rejected

- Account-pool extension.
- Stateless JWT sessions.
- Reverse-proxy-only identity.
- Cookie-authoritative SSO flow state.
- SAML v0.5.1.
- Permissive admin CORS.
- Claiming strict CSP while inline handlers remain.

### Consequences

- Security mutations synchronously journal locally.
- Local identity is explicitly single-node.
- Listener changes become validated asynchronous operations.
- Temporary CSP retains unsafe-inline but is explicitly labelled compatibility CSP.
- Offline recovery requires host ownership and downtime.
- External SIEM/WORM export is required for independent tamper-resistant retention.

### Follow-ups

- Shared transactional auth/session/flow/audit backend.
- MFA/WebAuthn.
- External WORM/SIEM sink.
- Hardware/keyring-backed secret references.
- Strict nonce-based CSP if not completed in this phase.

## Target layout

    internal/
    ├── adminauth/
    │   ├── types.go
    │   ├── permissions.go
    │   ├── service.go
    │   ├── password.go
    │   ├── session.go
    │   ├── csrf.go
    │   ├── throttle.go
    │   ├── audit.go
    │   ├── request_security.go
    │   ├── flow_store.go
    │   ├── oidc.go
    │   └── saml.go
    ├── store/adminauth/
    │   ├── store.go
    │   ├── journal.go
    │   ├── lock_unix.go
    │   └── lock_windows.go
    ├── app/admin_security.go
    ├── admin/routes.go
    ├── admin/security_routes.go
    ├── adminapi/security.go
    └── adminapi/session.go

## Durable snapshot and journal protocol

Security authority uses:

- .routenest-admin-auth.json
- .routenest-admin-auth.journal
- one exclusive data-directory process lock

Each durable mutation:

1. Acquire the in-process mutex and OS-exclusive lock.
2. Validate snapshot revision/hash and journal chain head.
3. Compute the complete next state.
4. Append a canonical prepare record containing event ID, previous/next revision, previous journal hash, redacted audit event, complete next state and next-state hash.
5. Fsync the journal.
6. Write the snapshot through a durable atomic helper:
   - create a 0600 temporary file;
   - write all bytes;
   - fsync;
   - close;
   - rename;
   - fsync the parent directory;
   - reopen and verify revision/hash.
7. Append and fsync the commit record.
8. Publish the in-memory state and acknowledge.

Recovery:

- Prepare plus matching snapshot but no commit: append the missing commit.
- Prepare plus old snapshot: replay its full next state, verify and commit.
- Invalid chain/revision, truncated committed record or unsafe permissions: preserve evidence and fail startup.
- Journal rotation only follows a verified snapshot checkpoint and directory sync.

The existing platform.WriteFileAtomic is not sufficient because it omits file and directory sync.

## Audit semantics

The journal is tamper-evident through revision and hash chaining. It is not tamper-proof against a filesystem administrator able to rewrite the snapshot, journal and executable.

For independent retention or non-repudiation, configure an external SIEM/WORM sink. Local and external audit health are reported separately.

| Event class | Local journal failure policy |
| --- | --- |
| Bootstrap, password, role, SSO, listener changes | Reject mutation and do not acknowledge |
| Successful login/session issue | Do not issue session |
| Logout/revoke/emergency disable | Clear client cookie/in-memory access; attempt durable revoke; enter degraded lockdown if persistence fails |
| Auth/RBAC/CSRF denial | Continue denying; coalesce/sampling queue prevents attacker-controlled fsync amplification |
| Read-only access | No per-request durable event |
| External sink failure | Local journal remains authoritative unless requireExternalAudit is enabled |

## Password and Argon2id rules

- PHC-encoded Argon2id.
- Initial target: 64 MiB, three iterations, parallelism one.
- Never below 19 MiB, two iterations, parallelism one.
- Parser ceilings before allocation:
  - memory at most 256 MiB;
  - iterations at most 10;
  - parallelism at most 8;
  - salt 16-64 bytes;
  - key 16-64 bytes.
- Constant-time derived-key comparison.
- Unknown users verify against a fixed dummy PHC.
- Request body at most 4 KiB; username at most 128 bytes; password at most 1024 bytes.
- Global and per-source token buckets run before KDF.
- KDF concurrency semaphore starts at min(2, GOMAXPROCS).
- Verification uses a bounded deadline.
- Benchmark target: p95 under 750 ms and two concurrent checks under 256 MiB on the lowest supported packaged platform.

## Bootstrap and legacy migration

### New installation

ROUTENEST_ADMIN_BOOTSTRAP_PASSWORD is read only while completed security state does not exist.

- It is one-time and never the permanent password.
- Completed bootstrap permanently ignores the environment value.
- Changing/reintroducing the variable cannot recreate bootstrap mode.

Without the variable:

- CLI writes a generated secret directly to the controlling TTY.
- Headless startup without a TTY fails with instructions.
- Desktop uses a one-shot native dialog/in-memory channel.
- No HTTP endpoint, application log or browser storage reveals it.
- Pending generated bootstrap rotates on restart.

Bootstrap completion submits the bootstrap secret and permanent password in one operation. No general session exists before completion.

### Legacy password migration

1. Valid new state is the sole authority.
2. If new state is absent and the legacy hash exists, create a break-glass identity with a legacy verifier.
3. Keep legacy fields untouched until successful Argon2 rotation.
4. After durable rotation, replace legacy fields with an unmatchable migration sentinel, not empty strings.

The sentinel makes old binaries see password configured while verification always fails, preventing anonymous downgrade.

## Break-glass and offline recovery

The reserved local break-glass identity:

- cannot be deleted;
- cannot lose security_admin;
- cannot be disabled online;
- remains present when SSO is enabled;
- is independently throttled and audited.

Every user/role/SSO mutation rejects a result with no enabled security administrator.

Offline command:

    routenest auth recover

It must:

- run only with a local controlling TTY or pre-server native desktop recovery context;
- verify effective UID/file ownership and secure data-directory permissions;
- acquire the exclusive store lock and refuse while the server owns it;
- rotate the break-glass bootstrap secret;
- revoke sessions and SSO transactions;
- preserve journal history;
- deliver the secret only through TTY/native dialog.

There is no HTTP or environment-variable recovery/reset path after initialization.

## Roles and permissions

| Role | Permissions |
| --- | --- |
| viewer | Redacted configuration, health, dashboard, logs and provider/model views |
| provider_admin | Viewer plus provider/model/combo/routing/quota/upstream changes |
| credential_admin | Viewer plus accounts, connections, API keys and credential import/export/delete |
| security_admin | Viewer plus users, roles, SSO, sessions, audit and listener security |
| break_glass | All permissions; local authentication only |

Sensitive exceptions:

- Credential export requires explicit credential-export permission.
- Bulk delete requires domain permission plus dangerous.execute.
- Listener/trusted-proxy/password/session/SSO changes require security administration.
- Missing route permission denies access and fails startup.

## Fail-closed route registry

Only the admin registry may register /admin/api/*.

Each definition declares method, path, permission or Public=true, CSRF policy and handler.

Startup fails on:

- empty permission without Public=true;
- duplicate/conflicting method/path;
- mutation without CSRF policy;
- invalid SSO exemption;
- direct /admin/api/ registration outside the registry.

Unknown methods return 405 and cannot fall into another permission entry.

## Canonical request-security context

One middleware derives peer IP, client IP, external scheme/host, secure status and trusted-proxy status.

Rules:

- Direct TLS uses r.TLS and validated Host.
- Forwarded headers are ignored unless the immediate peer belongs to configured trusted CIDRs.
- Trusted chains are walked right-to-left, removing trusted proxy hops.
- Conflicting/multiple forwarded scheme or host values are rejected.
- Effective scheme/host must equal the configured public URL.
- Direct untrusted access to /admin/* is rejected in proxy mode.
- Cookies, CSRF, Origin, throttling, SSO callbacks and audit identity all consume this context.

## Exact same-origin wrapper rules

Pipeline:

1. Build request-security context.
2. Apply security headers.
3. Resolve method/path policy.
4. Handle validated preflight.
5. Authenticate.
6. Apply same-origin or protocol-specific exemption.
7. Validate CSRF.
8. Authorize permission.
9. Invoke handler.
10. Record required audit outcome.

Host rules:

- Effective host/port must exactly match the canonical configured origin after default-port normalization.
- Reject userinfo, fragments, unexpected path, malformed IPv6 and arbitrary hosts.
- Loopback allowed hosts are explicit configured values.

Origin rules:

- Present Origin must exactly match canonical scheme/host/port.
- Reject null, multiple/malformed origins and Sec-Fetch-Site: cross-site.
- Safe GET/HEAD without Origin is allowed only with valid Host/auth and no side effect.
- State-changing requests without Origin are rejected except the exact desktop rule.
- Sensitive exports become POST.

Desktop WebView missing-Origin exception requires all:

- direct loopback peer;
- no accepted forwarded headers;
- canonical loopback Host;
- valid session and CSRF;
- Sec-Fetch-Site absent or same-origin;
- not an SSO callback.

Preflight:

- Cross-origin preflight is rejected.
- Valid same-origin preflight returns 204.
- Requested method must match route policy.
- Requested headers are limited to Content-Type and X-RouteNest-CSRF.
- Echo the exact allowed origin and Vary: Origin.
- Never emit wildcard origin.

Generic httpx.CORS remains only for public proxy endpoints.

## Precise CSRF rules

Normal POST/PUT/PATCH/DELETE and side-effecting operations require:

- valid session;
- X-RouteNest-CSRF;
- token hash bound to the session;
- constant-time comparison;
- same-origin validation;
- declared JSON content type where applicable.

No token is accepted in query parameters.

Local login/bootstrap:

- POST only.
- Exact Origin when present.
- Missing Origin only for the direct-loopback/native rule.
- Exact Host, rate limiting and body limits.
- No cross-origin CORS.

OIDC:

- Start is same-origin POST.
- Callback is a top-level cross-site GET exempt from normal Origin/CSRF.
- Exemption still requires exact canonical callback plus one-time state, nonce and PKCE.
- Browser return URLs are never authoritative.

SAML:

- Start is same-origin POST.
- ACS is cross-site POST exempt from normal Origin/CSRF.
- ACS requires one-time RelayState, InResponseTo, signature, issuer, audience, recipient, destination and time validation.
- Body is size-limited form data.
- IdP-initiated SSO is disabled by default.

## Security headers and CSP

Apply to /admin/ and admin APIs:

- X-Content-Type-Options: nosniff
- Referrer-Policy: no-referrer
- X-Frame-Options: DENY
- Cross-Origin-Opener-Policy: same-origin
- Cross-Origin-Resource-Policy: same-origin
- restrictive Permissions-Policy
- Cache-Control: no-store
- HSTS only on explicitly validated HTTPS
- CSP base-uri none, object-src none and frame-ancestors none

Temporary compatibility CSP:

    default-src 'self';
    base-uri 'none';
    object-src 'none';
    frame-ancestors 'none';
    img-src 'self' data:;
    font-src 'self' data:;
    connect-src 'self';
    script-src 'self' 'unsafe-inline';
    style-src 'self' 'unsafe-inline';
    form-action 'self' <exact enabled SAML IdP origins>;

No unsafe-eval, wildcard source or remote scripts. This is explicitly compatibility CSP, not strict CSP.

A later strict-CSP gate removes inline handlers/styles, uses delegated listeners/nonces and removes unsafe-inline.

## Listener preflight and rollback

Replace persist-before-restart with a validated asynchronous operation:

1. Validate auth initialization, host exposure, TLS certificate/key match, trusted CIDRs, public URL and SSO callbacks.
2. Return 202 plus an operation ID.
3. Preserve old configuration.
4. Gracefully stop old listener after the response completes.
5. Bind/wrap the new listener and run a local readiness probe.
6. Persist new configuration only after readiness succeeds.
7. Restore the old listener/config on failure.
8. Terminate if rollback cannot restore the known-safe listener.

Non-loopback requires direct TLS or validated trusted proxy configuration.

## Secret references and rotation

Persist only secret references and public certificate fingerprints. Never persist inline client secrets or private keys.

Environment references:

- Name must match an approved ROUTENEST_* pattern.
- Missing value makes the provider not ready.
- API exposes only a redacted reference.

File references:

- Resolve symlinks and enforce approved roots.
- Require service ownership.
- Unix mode must be 0600 or explicitly allowed owner/group 0640.
- Windows ACL must allow only expected service principals.
- Private key must parse and match its certificate.

Fingerprints:

- Secret fingerprint uses keyed HMAC-SHA-256, not a raw hash.
- Certificate fingerprint uses public SHA-256.
- Only shortened fingerprints/status appear in APIs/audit.

Rotation:

- A fingerprint change atomically rebuilds the provider client.
- Pending flows for that provider are revoked.
- Existing sessions follow configured revalidation policy.
- Missing/unreadable/permission-invalid secrets fail closed and never silently disable/enable a provider.

## Go baseline and exact SSO dependencies

Raise the project baseline to Go 1.26.

Pin:

- github.com/coreos/go-oidc/v3 v3.21.0
- github.com/crewjam/saml v0.5.2
- golang.org/x/oauth2 v0.37.0
- github.com/russellhaering/goxmldsig v1.6.1 or newer transitively

crewjam/saml is BSD-3-Clause. Version v0.5.2 is required for XML namespace/signature hardening and the CVE-2026-33487 goxmldsig fix. Version v0.5.1 is forbidden.

Update:

- go.mod and go.sum
- desktop/icon-gen/go.mod
- .github/workflows/build.yml
- Dockerfile builder pin
- README.md and README.zh-CN.md
- About UI and Go-version fixtures
- THIRD_PARTY_NOTICES.md

Run exact module graph guards, govulncheck and license inventory.

## Server-side SSO transient state

AuthFlowStore holds an opaque handle hash, provider/type, OIDC nonce, PKCE verifier, SAML request ID, expected issuer/entity, canonical callback, timestamps and return location.

Properties:

- TTL at most 10 minutes.
- Bounded per-source/provider counts.
- Atomic one-time consume.
- Browser cookies never own the nonce, verifier or request ID.
- OIDC state and SAML RelayState expose only opaque handles.
- Restart invalidates local outstanding flows safely.
- Replay fails.

## Independent readiness flags

Expose:

- authStoreReady
- auditJournalReady
- breakGlassReady
- transportReady
- routePolicyReady
- csrfReady
- oidcProviders[id].ready
- samlProviders[id].ready
- externalAuditReady
- multiReplicaSupported
- adminReady

adminReady requires the six core local flags. One broken SSO provider does not disable local break-glass login.

Local status reports backend local_file, SSO flow backend memory and multiReplicaSupported=false.

## Phase enablement gates

### Gate 0 — Baseline and characterization

- Go 1.26/dependency migration.
- Current-behavior characterization.
- Crash/fault harness.
- No production auth behavior change.

### Gate 1 — Security store/bootstrap

Enable only after snapshot/journal recovery, TTY/native bootstrap and legacy migration tests pass. Anonymous bypass is permanently removed.

### Gate 2 — Session/CSRF/same-origin

Enable only after the browser wrapper, desktop exception and mutation policies pass. CSRF initialization failure blocks admin route publication.

### Gate 3 — RBAC

Initially migrate break-glass with all permissions. Role-specific UI enables only after every route has a policy and the role matrix passes.

### Gate 4 — Transport enforcement

Non-loopback is rejected until listener preflight/rollback and request-security tests pass.

### Gate 5 — OIDC per provider

Enable only after discovery, secret resolution, canonical callback, role mapping and hostile signed fixture tests pass.

### Gate 6 — SAML per provider

Enable only after metadata/certificate resolution, canonical ACS, signed hostile fixture tests and replay protection pass.

### Gate 7 — Strict CSP

Do not claim strict CSP while unsafe-inline remains.

## Implementation phases

1. Go 1.26 and dependency/security-notice migration.
2. Characterization, route inventory and fault-injection harness.
3. Durable snapshot/journal/lock.
4. Bootstrap, Argon2id and legacy sentinel migration.
5. Durable sessions, throttling, CSRF and audit semantics.
6. Fail-closed route registry and RBAC matrix.
7. Request-security context, same-origin wrapper and security headers.
8. Listener preflight/rebind/rollback and TLS/proxy enforcement.
9. Embedded login/bootstrap/session/RBAC UI.
10. OIDC adapter and local signed issuer tests.
11. SAML adapter and hostile signed IdP fixtures.
12. Secret-reference rotation and readiness diagnostics.
13. Strict-CSP migration or explicit compatibility-CSP release boundary.
14. Cleanup, architecture/security review and full regression.

## Required crash and downgrade tests

Inject failure:

- before/after prepare append;
- before/after journal sync;
- during temporary snapshot write;
- after snapshot sync;
- after rename before directory sync;
- after directory sync;
- before/after commit append;
- before commit sync.

Each restart must recover to exactly one committed state and audit event.

Old-binary sentinel test:

1. Build checkpoint 441b044.
2. Create migrated accounts state with sentinel.
3. Run the old binary against that state.
4. Confirm password is considered configured, verification always fails and no admin API passes anonymously.

## Pre-mortem

1. Upgrade locks out every administrator.
   - Cause: legacy authority cleared before new state is durable.
   - Mitigation: two-phase migration, backup, sentinel and integration fixture.
2. Trusted proxy headers are spoofed.
   - Cause: forwarded values accepted from an untrusted peer.
   - Mitigation: CIDR trust, canonical public URL, direct-access rejection and spoof tests.
3. SSO grants excessive permissions.
   - Cause: permissive mapping or claim drift.
   - Mitigation: deny-by-default exact mappings, preview, audit and hostile missing/extra claim fixtures.

## Expanded test plan

### Unit

- Argon2 encode/verify/parser limits/rehash.
- Legacy verifier and sentinel migration.
- Bootstrap completion and permanent environment ignore.
- Session hash/expiry/revision/revoke.
- CSRF binding and origin validation.
- Throttle cleanup/Retry-After/KDF semaphore.
- Role and permission resolution.
- Journal hash/revision/recovery.
- OIDC claim mapping and flow consume.
- SAML attribute mapping and replay store.
- Secret-reference permission and fingerprint behavior.

### Integration

- Fresh install bootstrap, rotate, restart, login.
- Legacy account file, rotate, sentinel, restart.
- Crash injection at every persistence boundary.
- Route matrix for every role.
- CSRF same-origin/desktop/callback cases.
- Direct TLS and trusted proxy classification.
- Listener apply/rollback.
- Local signed OIDC issuer.
- Local signed SAML IdP fixture.
- Audit persistence/redaction/readiness.

### E2E/browser

- Bootstrap and forced rotation.
- Local login/logout/session revoke.
- Viewer/provider/credential/security UI.
- CSRF-protected mutations.
- OIDC and SAML buttons/callbacks.
- Mobile/light/dark login/security layouts.
- Security headers/CSP.
- No secrets in console or browser storage.

### Observability/security

- Login outcomes, throttles, active sessions, denials and SSO failures.
- Redacted readiness flags.
- Static guards for anonymous bypass, SHA-256 password authority, empty RNG fallback, unclassified routes, untrusted forwarded-header use and plaintext secrets.
- Exact dependency guards.
- Secret scan and govulncheck.

## Acceptance criteria

- Fresh, missing or corrupt auth state never exposes admin APIs anonymously.
- Bootstrap secret never enters HTTP, logs or persisted plaintext.
- Completed bootstrap ignores environment bootstrap values forever.
- Legacy downgrade fails locked, not open.
- No online path removes the final security administrator.
- Offline recovery is local-only, owner-checked and lock-protected.
- Every admin method/path is public or permission-declared.
- Every normal mutation requires session-bound CSRF and same-origin validation.
- OIDC callback and SAML ACS exemptions require protocol-specific one-time state.
- Missing Origin is accepted only by the exact direct-loopback desktop rule.
- Untrusted forwarded headers never affect identity, cookie security, Origin or callbacks.
- Failed listener reconfiguration preserves the old active and persisted configuration.
- Secret-reference failure is visible and fail-closed.
- Local audit is described as tamper-evident, not tamper-proof.
- Readiness components are independently observable.
- Local backend reports multi-replica unsupported.
- Compatibility CSP contains no unsafe-eval or wildcard and is not called strict.
- Exact dependency versions and goxmldsig minimum are enforced.
- Hostile OIDC/SAML, crash, CSRF, proxy-spoof, role and migration tests pass.

## Verification commands

    go mod tidy
    go mod verify
    go list -m all
    govulncheck ./...
    go test ./internal/adminauth ./internal/store/adminauth ./internal/admin ./internal/adminapi
    go test -race ./internal/adminauth ./internal/store/adminauth ./internal/admin ./internal/adminapi
    go test ./internal/integration
    go test ./...
    go vet ./...
    go build ./...

Also run:

- desktop builds and selfcheck;
- Docker build/smoke;
- license inventory and THIRD_PARTY_NOTICES verification;
- static route-registration and insecure-default guards;
- secret scan;
- browser bootstrap/login/RBAC/SSO/CSRF suite;
- CSP/header assertions;
- independent architecture review;
- independent security review;
- changed-files-only cleanup and full post-cleanup regression.

## Available agent roster and staffing

Available roles:

- explore
- dependency-expert
- architect
- executor
- debugger
- test-engineer
- designer
- verifier
- code-reviewer
- critic
- code-simplifier

Ralph staffing:

- primary executor: store/service/bootstrap/RBAC;
- dependency expert: versions/licenses/SSO;
- test engineer: crash/migration/protocol/browser;
- designer: login/security/role UI;
- architect and verifier: final security boundary;
- code simplifier: changed-files-only cleanup.

Team lanes:

1. Storage/crypto/bootstrap: internal/adminauth and internal/store/adminauth.
2. HTTP/RBAC/transport: internal/admin, internal/adminapi and internal/proxycore.
3. OIDC/SAML/dependencies: adapters, modules and notices.
4. UI: Security page, login dialog, shell wrapper, i18n and contracts.
5. Verification: crash, old-binary, browser and static guards.

## Launch hints

Ralph:

    $ralph implement .omx/plans/2026-11-07-admin-auth-rbac-sso.md

Team:

    omx team 5:executor "Implement .omx/plans/2026-11-07-admin-auth-rbac-sso.md using the ownership lanes and verification path"

Team proves focused lane tests, dependency/license checks, route inventory, crash/migration fixtures and browser security flows before shutdown.

Ralph performs full reconciliation, race/test/vet/build, browser/header checks, architecture/security review, cleanup and post-cleanup regression.

## Goal-mode follow-up suggestions

- $ultragoal is the default durable implementation tracker.
- $autoresearch-goal is appropriate only for additional IdP/security research.
- $performance-goal is appropriate for Argon2/session/audit latency work.

## Changelog from review iterations

- Replaced vague atomic JSON with prepare/commit journal and fsync protocol.
- Added old-binary fail-closed sentinel migration.
- Added Argon2 allocation/KDF DoS controls.
- Defined one-time bootstrap and native-only recovery.
- Added last-security-admin invariant.
- Added runtime fail-closed route registry.
- Added listener rollback and canonical proxy context.
- Raised Go baseline and selected hardened SSO versions.
- Added exact Origin/Host/preflight/desktop/SSO CSRF rules.
- Added compatibility CSP and strict-CSP follow-up.
- Corrected crewjam/saml licensing.
- Added secret-reference permissions, fingerprint and rotation rules.
- Clarified tamper-evident audit and external SIEM boundary.
- Split readiness into independent flags.
- Added phase gates, crash injection and downgrade tests.

