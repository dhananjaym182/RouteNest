# Administrator security and SSO

Status: **current operational documentation**
Reviewed: **2026-11-10**

RouteNest uses one durable administrator identity plane for local passwords, RBAC,
OIDC and SAML. Corrupt or partially missing committed authority fails closed. If both
authority snapshot and journal are deleted, the store can appear fresh and return to
secret-protected bootstrap; deleting authority files is not a recovery procedure.
Client API keys used by model/inference routes never grant administrator access, and an
empty client-key list does not disable administrator authentication.

## First startup

On an interactive CLI startup, RouteNest generates a one-time bootstrap secret and
writes it only to the controlling terminal. A headless first start without a controlling
terminal must provide `ROUTENEST_ADMIN_BOOTSTRAP_PASSWORD`; the value must contain 12 to
1024 bytes, and the operator is responsible for generating it randomly and delivering it
privately. Open `/admin/`, enter that secret, and set a permanent password for the
reserved local administrator `admin`.

The desktop build instead performs guarded native first-run setup and asks for the new
administrator password directly; no terminal, environment variable, or displayed
bootstrap secret is required. While durable initialized authority is retained,
subsequent startup ignores the bootstrap environment variable.
New permanent passwords require at least 8 Unicode characters and may occupy
at most 1024 UTF-8 bytes. The same policy applies to bootstrap completion,
password changes, local account creation, resets and offline recovery completion.
Passwords use bounded Argon2id; raw session tokens and CSRF tokens are never
persisted. An installation with a valid legacy administrator password enters a
mandatory password-upgrade flow. Successful upgrade persists the Argon2id credential
and an unmatchable legacy sentinel so an older binary cannot restore anonymous
administration.

## Public entry surfaces

The static `/admin` and `/admin/` shell is public so it can render bootstrap or login;
the shell contains no server-rendered authenticated data. The page starts with a standalone
loading surface and hides the dashboard until the ordinary administrator session has been
verified. Unauthenticated browsers see a plain full-page password login or first-run
bootstrap/legacy-upgrade form. `#login` and `#recover` are distinct page views under
`/admin/`, with one main landmark, a shared language/theme header, normal document
scrolling, and no modal focus trap or decorative floating card. Recovery can be opened,
refreshed, and navigated with browser history without submitting a reset. Recovery
submission and its one-time replacement-code result cannot be abandoned until the code
is saved and Finish is selected. Passwords, codes, sessions, and CSRF never enter the
page route or history state. An expired or revoked session clears the displayed identity
and returns to login. Successful sign-in restores only an approved console destination.
For direct local GET/HEAD navigation only, `/admin/` at `localhost`, `127.0.0.1`, or `::1`
redirects to the configured canonical loopback address when the scheme and port match.
The redirect creates no session and copies no query values. Remote clients, arbitrary
hosts, trusted-proxy mode, alternate ports/schemes, mutations, and API requests do not
receive this exception. Administrator APIs retain exact canonical-origin validation.
The presentation state is not authentication proof; server-side session, RBAC, and CSRF
checks remain authoritative. Its browser scripts request protected data only after session
establishment. The public administrator API routes are exactly:

- `GET /admin/api/auth/status`;
- `POST /admin/api/auth/bootstrap`, `POST /admin/api/login`,
  `POST /admin/api/auth/legacy/rotate`, and `POST /admin/api/auth/recovery`;
- `POST /admin/api/auth/oidc/start` and `GET /admin/api/auth/oidc/callback`;
- `GET /admin/api/auth/saml/metadata`, `POST /admin/api/auth/saml/start`, and
  `POST /admin/api/auth/saml/acs`.

The registered `/admin/api/auth/desktop-handoff` path rejects ordinary HTTP/browser
requests, even from localhost; only the private embedded desktop transport can redeem
an issued handoff. There is no HTTP device-enrollment or device-secret sign-in endpoint.

These are bootstrap, login, recovery, readiness, metadata, or protocol-entry routes,
not anonymous administrative authority. Every other
registered administrator API route requires a
durable session plus all route permissions. Same-origin and protocol-specific callback
checks still apply to the public routes.

## Durable sessions

Administrator sessions are independent of client API keys. The default session
lifetime is 12 hours; the internal service rejects a non-default configured lifetime
outside `(0, 24 hours]`. The browser receives a SameSite=Lax `HttpOnly` session cookie
and a separate, script-readable SameSite=Strict CSRF cookie under `/admin`; both are
`Secure` when the effective external scheme is HTTPS, and only hashes are stored in the
server authority. Sessions survive restart until expiry or revocation.
Logout revokes the current session. Password, role, account-status and identity changes
revoke the affected user's sessions; changing or deleting an SSO provider revokes its
sessions and pending flows. A `security_admin` can inspect and revoke active sessions.

## Trusted desktop sign-in

The desktop app can remember an explicitly trusted device without storing or replaying
the administrator password. Enrollment follows valid local-password authentication and
stores a 256-bit random device credential in Windows Credential Manager, the macOS login
Keychain, or Linux Secret Service through the platform keyring adapter. The administrator
authority stores only its SHA-256 verifier, owning user revision, activation/expiry and
last-use times, and descriptive device metadata. Enrollment becomes usable only after the
OS keyring write is read back successfully and the registration is durably activated.

A valid enabled device credential issues a fresh ordinary 12-hour local administrator
session, then passes that session to the existing console through a two-minute, single-use
in-process handoff. The first setup handoff is created only after recovery-code saving
is acknowledged. The 90-day device credential is not a browser cookie, recovery code,
password, or proof that a process is RouteNest. Another process with sufficient access
to the local user can still compromise it.

The desktop console stays at `/admin/` on the embedded Wails origin. Its private Go
transport uses the existing route registry, session validation, RBAC, and session-bound
CSRF without relying on custom-scheme browser cookies. It never sends native session or
device secrets to the gateway socket. Wails **v2.17.1** fixes the binding-origin advisory
GHSA-47hv-j4px-h3c9 and the Linux child-frame IPC advisory GHSA-r5j2-ghh9-2jqw.
Additional binding origins are empty; ordinary localhost pages and external development
assets are not authorized native entry points.

Ordinary browsers never receive or read the OS-stored credential and always authenticate
independently. Missing, disabled, revoked, expired, corrupt, locked, or unavailable
credential storage falls back to password login. RouteNest never falls back to plaintext
credential files or anonymous administrator access and never silently replaces corrupted
material.

**Administrator Security → Desktop Sign-In** lists registered devices and last successful
use, where available. Trusting, resuming, revoking, or forgetting a registered device
requires the current local administrator password. Removing only an unusable local
credential requires explicit deletion confirmation, never silently re-enrolls it, and
cannot enable trust. Lock ends only the current session. Sign out needs no password
merely to end its own session and pause automatic sign-in. Sign out ends
the current session and pauses automatic sign-in. Forget this device also revokes the
server registration and deletes the local OS credential. Password change, online reset,
offline emergency recovery, and affected local-user changes revoke device registrations.

## Roles and permissions

Server route registration is authoritative. The administrator UI may hide or disable
controls for clarity, but client-side visibility never grants access.

| Role | Server permissions |
| --- | --- |
| `viewer` | `admin.view` |
| `provider_admin` | `admin.view`, `provider.write`, `dangerous.execute` |
| `credential_admin` | `admin.view`, `credential.write`, `credential.export`, `dangerous.execute` |
| `security_admin` | `admin.view`, `security.write` |
| `break_glass` | All permissions above; valid only for the immutable local emergency administrator |

Examples of the resulting ownership:

- `provider.write` manages Provider Instances, models, routing, combos and egress
  configuration. Current legacy provider/config/OpenCode endpoints also disclose and
  replace raw provider keys, headers and proxy values, and can bind existing Provider
  Connections; the global projections remain redacted.
- `credential.write` manages Cline accounts, Provider Connections and client API
  keys. The account-export route requires `credential.export`; current production roles
  grant it together with `credential.write`, but the server route does not require both.
- `security.write` manages users, roles, SSO, sessions and listener security.
- Routes marked `dangerous.execute` require that permission in addition to their domain
  permission. Delete All Accounts requires `credential.write` plus `dangerous.execute`;
  clearing request logs requires `security.write` plus `dangerous.execute`, so
  `security_admin` alone cannot clear them. Client-key deletion requires
  `credential.write` plus `dangerous.execute`. Deleting the final effective key also
  requires the exact `disable-client-authentication` request confirmation.

The current UI combines data with different permissions on some pages. A section that
is unavailable to the signed-in role must not be interpreted as empty or unknown; this
is a proposed UI requirement and some current composite pages still fail or show zero.
See
the role/state contract in [CONTRACTS.md](../CONTRACTS.md).

Role, password and other identity changes revoke affected sessions according to the
server lifecycle. Every authenticated browser mutation is same-origin checked and
requires the CSRF token bound to its durable session. Public bootstrap/login, legacy
rotation, and SSO-start entry points require same-origin validation but not an existing
session CSRF token. OIDC callback and SAML ACS routes use their protocol state, nonce/InResponseTo,
signature and replay protections. Flow handles are not additionally bound to the
initiating browser. These public entry routes do not grant anonymous administration.
The missing-Origin loopback exception applies only to local-entry and SSO-start policies,
not authenticated session mutations or protocol callbacks such as SAML ACS. Qualifying
local-entry requests require loopback peer, client and authority, compatible fetch
metadata, and no trusted proxy.

## Hosted administration

Local installations need none of the hosted-administration variables. A production
non-loopback listener requires the first three settings below. RouteNest accepts an
`http` canonical origin when HSTS is disabled, so HTTPS remains an operator deployment
requirement rather than a universal server invariant; use HTTPS for every hosted
administrator deployment.

| Variable | Requirement and purpose |
| --- | --- |
| `ROUTENEST_ADMIN_CANONICAL_URL` | **Required:** exact public origin, for example `https://routenest.example.com` |
| `ROUTENEST_ADMIN_PROXY_MODE=true` | **Required:** reject direct administrator access and trust only configured proxy peers |
| `ROUTENEST_ADMIN_TRUSTED_PROXY_CIDRS` | **Required:** comma-separated CIDRs containing only the actual reverse-proxy peers |
| `ROUTENEST_ADMIN_HSTS=true` | Optional hardening: emit HSTS after the canonical public URL is HTTPS; enabling it with an HTTP canonical URL is rejected |

The canonical URL must be an `http` or `https` origin without credentials, query,
fragment, or a non-root path. The configured host is exact: `localhost`, `127.0.0.1`
and `::1` are not interchangeable canonical authorities. RouteNest does not provide TLS
termination; use an HTTPS reverse proxy for hosted administration. Forwarded headers
are ignored unless the immediate peer is trusted.
Conflicting `Forwarded` versus X-forwarded header families, multiple elements or
repeated parameters inside the parsed `Forwarded` value, repeated X-forwarded scheme/
host values, direct access in proxy mode, and any request whose effective origin differs
from the canonical URL are rejected. The proxy must still emit only one physical
`Forwarded` field; multiple physical fields are not a documented accepted input.
Do not use `0.0.0.0/0` or an entire shared network as a trusted proxy range. The
production configuration path validates HSTS against an HTTPS canonical URL; custom
embedding code should apply the same validation rather than enabling HSTS independently.

### Docker and reverse proxy

The sample compose file binds RouteNest only to `127.0.0.1:3457` on the host.
Create `.env` before the first start:

```dotenv
ROUTENEST_ADMIN_BOOTSTRAP_PASSWORD=replace-with-a-long-random-one-time-secret
ROUTENEST_INITIALIZE_EMPTY_ACCOUNT_STORE=true
ROUTENEST_ADMIN_CANONICAL_URL=https://routenest.example.com
ROUTENEST_ADMIN_PROXY_MODE=true
ROUTENEST_ADMIN_TRUSTED_PROXY_CIDRS=172.17.0.1/32
ROUTENEST_ADMIN_HSTS=true
```

The CIDR above is only an example. Resolve the actual peer address observed from
your host reverse proxy/container network and trust the narrowest possible
range. The proxy must overwrite, not append untrusted client values to,
`X-Forwarded-Proto`, `X-Forwarded-Host` and `X-Forwarded-For` (or emit one valid
single-element `Forwarded` header value). Do not emit both header families. Repeated
X-forwarded scheme/host values are rejected; do not rely on accepting multiple physical
forwarding fields. Remove the bootstrap password from `.env` after the permanent
password is set, and remove `ROUTENEST_INITIALIZE_EMPTY_ACCOUNT_STORE` immediately
after the account store is initialized. Leaving either first-run control in the service
environment is unsafe operational practice.

## OIDC and SAML

SSO providers are managed under **Administrator Security** by a
`security_admin`. RouteNest persists only provider metadata and secret references.

- OIDC callback: `<canonical-url>/admin/api/auth/oidc/callback`
- SAML ACS: `<canonical-url>/admin/api/auth/saml/acs`
- SAML metadata: `<canonical-url>/admin/api/auth/saml/metadata?provider=<id>`

Environment secret names must match `^ROUTENEST_[A-Z0-9_]+$`. File references are
stored as references, not secret contents. They must resolve inside the RouteNest data
directory's `admin-auth/sso-secrets/` root, be a regular file owned by the service user,
and use exactly mode `0600` on Unix. Windows file secret references currently fail
closed; use protected `ROUTENEST_*` environment values there.

OIDC uses discovery, state, nonce and PKCE and verifies issuer, audience,
authorized party, signature and time claims. SAML implements service-provider-initiated
HTTP-POST ACS only; artifact binding and single logout are not advertised. It requires
a signed response or assertion, one-time RelayState/InResponseTo,
issuer/audience/recipient/destination/time checks, and replay prevention. Optional SP
request signing uses the configured certificate/private-key references.

Provider readiness failures do not disable local break-glass login. Only enabled and
ready providers are offered on the login screen; readiness is refreshed independently
for each configured provider. Each provider must configure at least one default role or
group-mapped role; matching mapped roles are unioned with defaults. Federated identities
cannot receive the reserved break-glass role.

## Authentication CLI

`routenest auth --help`, `routenest auth status`, `routenest auth recover`, and
`routenest auth recover --help` are available in CLI and desktop binaries. Help commands
never open or mutate security storage. Status validates the existing snapshot and
hash-chained journal read-only while a server may retain its lifetime lock. It does not
create files or locks, change permissions, or finalize prepared mutations. Missing,
corrupt, or changing authority is reported explicitly; prepared mutations require a
restart. Active-session and enabled-device counts exclude expired entries. The
`-initialize-empty-account-store` option authorizes only guarded first-run client storage;
it is not a password reset mechanism.

## Offline recovery

### Recovery codes

Normal desktop setup generates a 256-bit recovery credential from `crypto/rand` and
shows it once. RouteNest stores only an Argon2id verifier. Existing local break-glass
administrators can issue or rotate a code under **Administrator Security → Recovery Code**
after current-password reauthentication; a stolen administrator session alone cannot
mint one. Rotating only the recovery code does not revoke otherwise valid trusted devices;
using a code to reset the password does.

The public recovery protocol still requires the recovery credential. It is protected by
the same canonical-host and same-origin request policy as local login and does not trust
localhost, a browser window, or Wails presence as proof of ownership. A successful reset
atomically changes the break-glass password, rotates the recovery verifier, revokes all
administrator sessions and pending SSO flows, and audits the operation without secret
material. Used or concurrently replayed credentials fail generically. Rate limiting and
bounded redacted denial auditing apply to failed attempts.

If the administrator journal is durably prepared but a later persistence step reports
failure, the exact prepared recovery code is returned once with a restart-required
result. Operators must save it and restart; retrying does not create another credential.
Older RouteNest binaries that strictly decode administrator state fail closed after a
recovery verifier has been written, so downgrading requires restoring a compatible
backup rather than deleting authority fields.

### Emergency owner-terminal recovery

If every online administrator is unavailable, stop all RouteNest processes and
run as the owner of the existing data directory from a local terminal:

```bash
./routenest auth recover
```

Recovery requires a controlling local terminal, the owner of the existing data
directory, intact authority files, secure ownership/permissions, and no running process
holding the store lock. It is an access-recovery path, not repair for corrupt or missing
identity state. It preserves the audit journal, revokes all sessions and SSO flows,
rotates the break-glass credential, and prints the one-time recovery secret only to the
controlling terminal. Restart RouteNest and complete recovery by entering that
terminal-issued recovery secret in a trusted local browser and setting a permanent
password. There is no HTTP endpoint that initiates offline recovery, and the bootstrap environment
variable is not a reset path while initialized authority is retained.

## Desktop first run

Normal Wails launches use Wails v2.17.1, a dedicated embedded origin for setup and the
console, and a native-only bridge with no additional binding origins. For a data
directory with no RouteNest, legacy, migration, administrator-authority,
corrupt-backup, account-pool, or initialization-witness evidence, the bridge may create
the verified empty client-authentication store and complete administrator setup. It
generates the bootstrap capability in memory, rechecks freshness immediately before the
mutation, uses the existing administrator service and journal, and becomes inert after
completion. A single-instance lock prevents competing desktop setup windows.

The localhost administrator server does not expose a public administrator-creation or
device-enrollment shortcut for desktop. After setup or verified device/password sign-in,
a 256-bit, two-minute, single-use handoff capability may transfer the already-authorized
session to embedded `/admin/`; it is accepted only through the private in-process
transport, never by a localhost browser or HTTP client. The gateway listener is
synchronously reserved before credential issuance; an occupied port cannot impersonate
readiness or receive a native session. Existing installations skip setup and enter
automatic sign-in only when both the OS
credential and authoritative registration remain valid. Missing, unreadable, corrupt,
partially restored, or otherwise uncertain security state produces a recovery diagnostic
and no storage recreation.

CLI, headless, and Docker deployments remain password-based and never use native desktop
automatic sign-in. `ROUTENEST_INITIALIZE_EMPTY_ACCOUNT_STORE` and
`-initialize-empty-account-store` remain process-local advanced controls, not desktop
user requirements and never reset established data.

## Client API keys are separate

Client API keys protect model and inference routes only after at least one key is
loaded, including the key-wrapped `/v1/*` routes and their compatibility aliases.
For compatibility, a valid intentionally empty key list leaves those routes open;
`/health` and `/v1/health` remain public liveness endpoints. `/ready` and `/v1/ready`
remain public but return HTTP 503 when client-authentication storage is unavailable, so
deployment health checks can distinguish a running process from an operational gateway.
A confirmed fresh CLI/headless installation must set
`ROUTENEST_INITIALIZE_EMPTY_ACCOUNT_STORE=true` for one startup, then remove it, or use
`-initialize-empty-account-store` for that one startup. Normal desktop setup authorizes
the same guarded initialization through its native first-run capability.
The variable is ignored as an initialization shortcut when independent RouteNest, legacy,
administrator-authority, migration, or corrupt-backup evidence shows that the directory
is established. Corrupt, unreadable, invalid-marker, partially restored, or uncertain
missing account storage remains unavailable, and key-wrapped routes return a redacted
HTTP 503 instead of accepting anonymous requests.

Back up `.routenest-accounts.json.initialized` together with the account pool. A valid
restored pool without a witness is validated before a new witness is written. A witness
without its pool, or loss of both files in an established directory, requires restoring
or deliberately creating a valid pool document and restarting RouteNest; the server
does not silently recreate security-sensitive storage after detecting that state.
The command-line flag is process-local and delegates to the same guarded initialization
policy; it is not retained and is not an authentication bypass. Console-generated keys
use 256 bits from Go's `crypto/rand`; previously issued keys remain valid. Key creation
and deletion durably persist a detached account-pool copy before publishing it in memory.
Pre-replacement failures make no visible change, while uncertain post-replacement errors
are returned and memory is reconciled to the file that won. Create, delete, final-key
delete, and failed mutation audit events contain no raw key material. None of these rules
grants administrator authority.

Intentional final-key deletion remains supported. The administrator API requires both
`credential.write` and `dangerous.execute`, plus an explicit confirmation that client API
authentication will be disabled. After successful deletion, the verified empty-key
compatibility policy applies and the public model/inference API accepts anonymous calls.

Provider API keys, OAuth tokens and Cline refresh tokens are Provider Credentials;
they do not authenticate RouteNest clients or administrators.

### Temporary Delete All Accounts warning

The Phase 1 warning is resolved by the Phase 2 account-only implementation described
below. This heading is retained so existing operator links remain valid.

### Delete All Accounts safety

**Delete All Accounts** now removes only Cline account records and account-owned secrets,
status, cooldowns and usage. It resets the current account index while preserving client
API keys, model/default settings, catalog state, removal reservations, listener state and
legacy migration sentinels. Other stores remain untouched.

The operation builds a detached copy, durably writes and syncs it, then publishes it
under the account-pool lock. Pre-replacement failures leave memory and disk unchanged;
a post-replacement directory-sync error is returned while memory is reconciled to the
file that actually won. The endpoint returns an explicit JSON success envelope only
after successful persistence. It remains protected by `credential.write`,
`dangerous.execute`, same-origin checks and session CSRF.

On Windows the durable replacement uses `MoveFileEx` with replacement and write-through
flags, followed by `FlushFileBuffers` on a directory handle opened with write access.
Post-replacement flush errors remain explicitly classified as uncertain and reconcile
memory to the file that won. Cross-compilation does not validate these runtime semantics;
real Windows execution remains required for full cross-platform approval.

## Storage and limitations

Administrator state lives under `admin-auth/` in the RouteNest data directory. Legacy
password fields in the account-pool file are migration input/sentinels, not current
administrator authority. Provider Credentials and generated client API keys are stored
in plaintext in their existing RouteNest state files; `.routenest-accounts.json` also
contains the saved listener host and legacy migration sentinels. Protect the entire data
directory with owner-only access and secure backups. Administrator passwords and
session/CSRF tokens are not stored in plaintext.

The fsynced administrator journal is revision/hash chained and tamper-evident, but it
is not tamper-proof against a host administrator who can rewrite the executable and all
local files. Export security events to an independent SIEM/WORM system when
independent retention is required.

The local identity and authentication-flow stores are single-node. A `security_admin`
can create local administrators with provider, credential and security roles, so
identity administration is privilege-granting authority rather than a strict separation-
of-duties boundary. Multi-replica administrator sessions, MFA/WebAuthn, and independent
audit export require additional infrastructure and are not claimed by the current
implementation. Identity-provider logout or later group changes do not continuously revoke an already
issued RouteNest session; use the administrator session/user controls when immediate
local revocation is required. Rotating only a referenced SSO secret file or environment
value does not itself perform the provider-configuration lifecycle mutation that revokes
sessions. `/health`, `/v1/health`, `/ready`, and `/v1/ready` remain unauthenticated;
readiness exposes only a redacted availability state.
