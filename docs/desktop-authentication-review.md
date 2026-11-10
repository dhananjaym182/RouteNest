# Desktop authentication and password management review

Review date: 2026-11-10. Base revision:
`4099b30fbecb97f0fe4c21416412565ac523da21`.

The implementation is left uncommitted and unstaged for independent review. No push,
release, or production-data reset was performed. Packaged Windows/macOS/Linux window
and real OS-vault testing is **not** certified by the evidence below.

## Rebuilt CLI and browser landing follow-up

The reported `./routenest --help` and `http://localhost:3457/admin` problems were
reproduced after the initial review. Root help listed the `auth` family without
explaining password workflows. The default `127.0.0.1` canonical origin rejected
`localhost` navigation with `400 invalid_request_context`. At the canonical URL,
the login card was a translucent overlay above a still-visible dashboard.

- Root and `auth help` now list status/recovery commands, password creation/change,
  forgotten-password recovery, device management, and the CLI/native build distinction.
  Help remains non-mutating and never accepts a password through command-line flags.
- Safe direct loopback GET/HEAD login navigation redirects to the configured canonical
  origin. The exception is static-only, same scheme/port, and creates no credentials;
  API, origin, CSRF, remote-client, and trusted-proxy restrictions remain unchanged.
- The login surface is opaque and standalone from first paint. The dashboard and
  credential/SSO forms stay hidden while session validation is pending. Verified
  sessions reveal the console; expiry clears the displayed principal and restores
  login without repeatedly stealing keyboard focus. EN/ZH and theme controls are
  available before sign-in.
- The CLI was rebuilt with `go build -trimpath -o routenest .`. Native desktop builds
  still require `desktop production` plus target-platform WebView dependencies.

Verification: eight new landing regressions; 204 MJS tests; CLI and desktop help
entrypoint tests; full sequential normal/race Go suites; `go vet` for both entrypoints;
module/build/format gates. A real IPv4-loopback Chromium run in an isolated network
namespace verified both `localhost/admin` forms redirect to canonical password setup,
GET/HEAD redirects set no cookies, and alias APIs still return 400. The final canonical
HTTP harness passed 398 security, 178 HTTP, 844 accessibility, 209 contrast, and 80
reflow checks, with 17 redacted screenshots and zero failures/page exceptions/network
failures/external requests. Expected negative 401/429 diagnostics remain recorded.
The updated desktop simulation passed 32 cases, 750 assertions, and 80 screenshots;
it is not packaged WebView/vault runtime evidence. The older Access Settings sequence
failure and target-OS runtime limitations below remain unresolved.

Follow-up evidence is retained under
`review-artifacts/desktop-authentication/login-landing-followup/`.

## Feature completion matrix

| Feature | Implemented | Verified | Remaining limitation |
| --- | --- | --- | --- |
| First-time password creation | Yes, native creation/confirmation using the existing administrator authority | Actual fresh-install Go orchestration, durable authority, acknowledgement-gated handoff, UI simulation | Real packaged first launch still needs an OS/window run |
| Automatic desktop login | Yes, explicit trust with a separate random credential and fresh ordinary sessions | Authority-backed native Go transitions, expiry/revocation/replay tests, fake-vault failure tests | OS-vault retrieval and close/reopen of a real packaged app remain unverified |
| Browser password login | Yes, independent of the native session | Real isolated HTTP browser login, cookie policy, fresh-context denial, RBAC/CSRF negatives | Native-origin browser behavior relies on the private transport and Wails origin guards, not browser cookies |
| Password change | Yes, existing current-password workflow | Real browser form, old-password rejection, all affected sessions/devices revoked in Go | Existing model-list notices can interfere with older broad UI harnesses; auth-specific harness dismisses them without mocking APIs |
| Recovery code reset | Yes, single use, replacement shown once, no automatic sign-in | Real browser reset, old-code replay rejection, replacement-code reset, cross-user session revocation; durable prepared-state tests | Lost recovery credentials still require the owner-controlled offline terminal procedure |
| Recovery-code management | Yes, password-verified generation/rotation | Real management UI and persistence tests; rotation alone preserves valid devices | Clipboard/secure user saving cannot be guaranteed by application tests |
| Trusted-device revocation | Yes, reserved local administrator can inspect/revoke all devices; other security administrators are self-scoped | Domain/store/native transitions; HTTP permission, origin, CSRF, and reauthentication tests | Remote revocation cannot guarantee destruction of copied OS credentials, so server revocation is authoritative |
| Complete logout | Yes, own session ends and automatic access is paused; Lock does not disable trust; Forget revokes and verifies deletion | Atomic bound/unbound-device sign-out, failed-vault pause, lock/unlock, reload guard, browser logout | If the vault is unavailable before any device identity can be verified, session termination succeeds but pause uncertainty is reported instead of claiming completion |
| CLI recovery/help | Yes, existing owner recovery and CLI options retained; new non-mutating help/status | Binary help checks, status while server retains its lock, read-only/prepared/corrupt/missing fixtures | Offline recovery deliberately requires intact owner-controlled authority, an exclusive lock, and a controlling terminal |

## Security implementation

- One administrator authority: existing password hashing, sessions, RBAC, CSRF,
  snapshots, journal, recovery, and audits. No second identity or device database.
- Device IDs and secrets are independent canonical encodings of 32 random bytes.
  Only the secret verifier and revocation metadata enter persisted administrator state.
  The persistent secret is stored only through an OS credential-store adapter.
- Enrollment is register, verified OS write/read-back, then single-use activation.
  Pending activation expires after five minutes. Password/user/recovery revisions
  invalidate old trust; device trust expires after 90 days.
- Valid device proof issues a fresh ordinary 12-hour local administrator session.
  Handoffs expire after two minutes and can be consumed once. Setup does not mint its
  handoff until recovery-code acknowledgement. Redemption revalidates the session and
  its CSRF proof, and rejects stale, expired, revoked, or replayed authority.
- Setup and `/admin/` remain on the embedded Wails origin. Additional binding origins
  are empty. External/development assets cannot initialize or automatically sign in.
  Native API calls go through a private bound in-process transport using the same route
  registry and ordinary session/CSRF checks. Ordinary HTTP clients cannot redeem a
  native handoff, enroll a device, or authenticate with a persistent device secret.
- The desktop reserves its gateway listener before credential issuance and does not
  infer ownership from an arbitrary HTTP 200. Existing-port takeover is rejected,
  later listener failure closes native access, and cancelled cooldown workers are
  joined before the runtime returns.
- The persistent device credential never enters JavaScript, localStorage, URLs,
  logs, or server state. Native ordinary session tokens stay in Go. JavaScript uses
  only ephemeral session-bound CSRF and one-use handoffs. A fixed-message Wails logger
  suppresses framework dispatcher payload logging, including malformed argument errors.
- Invalid stored device proof is distinct from an absent/revoked registration. It
  cannot silently produce successful Forget or orphan a previous valid registration.
  Explicit local-only cleanup never claims that it revoked a server record.

### OS credential mechanisms

| Platform | Mechanism | Fail-closed behavior and caveat |
| --- | --- | --- |
| Windows | Current-user generic Credential Manager through go-keyring/wincred | Missing/unavailable/verification failure requires password login; same-user endpoint compromise is outside vault protection |
| macOS | Direct Security.framework `SecItem` access to the default file-based Keychain, retaining creator access controls | No helper-wide access or plaintext fallback; CGO-disabled builds return unavailable. Real signed upgrade/ACL behavior has not run on macOS |
| Linux | Login-session Secret Service through go-keyring/godbus | Optional service/collection access may fail or prompt. The specification does not guarantee a universal encryption-at-rest or application-isolation policy |

OS calls have a five-second default total operation budget. Production calls share a
single-flight executor, preventing unlimited blocked goroutines. Uncertain writes are
quarantined within the process until verified removal. Durable activation/discard and
server revocation remain necessary across process restart. Installation namespaces are
hashes of the selected absolute data-directory path, not passwords, ports, or process
names. There is no plaintext fallback.

Wails is pinned to **v2.17.1**, beyond the first fix in v2.13.0 for
GHSA-47hv-j4px-h3c9 and including the Linux child-frame IPC fix
GHSA-r5j2-ghh9-2jqw. See [primary-source research](desktop-trusted-auth-research.md).

## Verification evidence

### Go, static checks, and compilation

Executed from the repository root:

```sh
go test -count=1 ./...
go test -tags desktop -count=1 .
go test -tags desktop -race -count=1 .
go test -race -count=1 ./internal/adminauth ./internal/admin \
  ./internal/desktopauth ./internal/store/adminauth ./internal/proxycore
go build ./...
go vet ./...
go vet -tags desktop ./...
go mod verify
git diff --check
```

- The complete normal Go suite passed. Focused native/root/domain/store/transport
  transition tests and static/module checks passed.
- Initial desktop race execution exposed an existing fire-and-forget cooldown worker
  surviving listener shutdown. The listener now cancels and joins that worker;
  the full desktop root race suite subsequently passed, as did routing/proxy race
  tests and ten repetitions of focused cooldown/listener races. No test lock was added
  merely to hide the leak.
- A broad race attempt overlapped a delegated test rename and failed to compile that
  generated test main. A later broad race run executed every package; it reported no
  data races but one pre-existing offline-recovery retry test hit the five-second
  password-hashing deadline under parallel race/Argon2 load. The isolated recovery
  retry passed three repetitions, the sequential administrator package race rerun
  passed, and the final `go test -race -p 1 ./...` passed every package, reusing
  successful race results where Go caching applied. The earlier resource-sensitive
  broad run is not described as a clean pass, and production deadlines were not weakened.
- Windows `GOOS=windows GOARCH=amd64 CGO_ENABLED=0` production desktop cross-build
  passed. macOS arm64 `CGO_ENABLED=0` test compilation passed with `-exec=true`.
  Neither is packaged runtime or real vault verification. The native macOS cgo adapter
  has not compiled against Apple frameworks in this Linux environment.
- Native Linux production compilation was attempted and blocked by missing GTK3 and
  WebKit2GTK development packages. This is an environment limitation, not a claimed
  native-runtime pass. Docker, PowerShell, axe-core, govulncheck, and staticcheck were
  unavailable; `go vet` and the guarded dependency baseline were executed instead.

The host's IPv4 loopback connections failed even for a standalone Python listener.
IPv6 worked, so actual browser runs used a disposable IPv6 loopback service. Networked
Go gates were also run inside a private user/network namespace with its own loopback
interface, without modifying host networking:

```sh
unshare --user --map-root-user --net python3 -c '
import fcntl, os, socket, struct
control = socket.socket()
flags = fcntl.ioctl(control, 0x8913, struct.pack("16sH14s", b"lo", 0, b""))
fcntl.ioctl(control, 0x8914, struct.pack("16sH14s", b"lo", struct.unpack("16sH14s", flags)[1] | 1, b""))
control.close()
os.environ.pop("ROUTENEST_DATA_DIR", None)
os.environ.pop("CLINE2API_DATA_DIR", None)
os.execvp("go", ["go", "test", "-race", "./..."])
'
```

### JavaScript and actual browser journeys

```sh
node --test tools/*.test.mjs
node tools/admin_delete_all_accounts.test.js
node tools/i18n_check.js
node tools/admin_security_journeys_verify.mjs --self-test
```

All 196 MJS tests, two account-deletion tests, twelve new harness self-tests, assembled
administrator JS syntax, and setup syntax passed. Localization has 1,566 keys with zero
missing, duplicate, dead, or unkeyed Chinese literals; existing English-value collisions
are advisory.

The real authentication browser harness used private temporary inputs, a new data
folder, actual Go HTTP APIs, retained cookies/CSRF, and Chromium 1,440/390/360-pixel
EN/ZH/light/dark surfaces. It passed password login/change, code issue/rotation/reset,
old-code rejection, replacement-code reuse once, complete cross-user revocation,
negative origins/CSRF, absence of native browser authority, owner-route checks, labels,
modal/background inertness, focus, text contrast, reduced motion, and 200%/400%
zoom-equivalent layout reflow.

The retained initial confirmation report records **397 security checks, 177 HTTP checks,
848 targeted accessibility checks, 213 contrast samples, 80 reflow cells,
32 zoom-equivalent cells, 17 redacted screenshots, and zero failures**. It records
401/429 responses and console messages from deliberate invalid credentials, session
revocation, and throttling; those diagnostics were not falsely called zero errors.
There were no page exceptions, failed network requests, or external browser requests.
The full authenticated navigation harness separately passed all twelve viewport/theme
cells: 156 canonical-route checks, 48 alias checks, 48 back/forward checks, 72 font checks,
96 focus checks, and 96 screenshots, with zero browser diagnostics.

```sh
ROUTENEST_SECURITY_VERIFY_MUTATIONS=1 \
BASE_URL='http://[::1]:3469/admin/' \
PLAYWRIGHT_MODULE=/tmp/routenest-playwright/node_modules/playwright-core \
CHROMIUM_PATH=/usr/bin/chromium \
ARTIFACT_DIR=/tmp/routenest-security-review \
node tools/admin_security_journeys_verify.mjs
```

`ADMIN_TEST_PASSWORD` and, for fresh bootstrap, `ADMIN_TEST_BOOTSTRAP_SECRET` must be
supplied privately and match the separately started disposable server. The harness
verifies that the pool path is inside a separate temporary directory before mutations.
It restores the test password, removes only its own disposable viewer, and explicitly
revokes its final verification session. Never run it on a deployed server.

The older Access Settings harness passed its twelve responsive layout cells and
reference-only SSO presentation, but its Enter-submission lifecycle checks were
interfered with by the existing asynchronously reopened model-sync notice. It is not
cited as a complete pass. The new authentication harness handles that notice and checks
real HTTP/session/owner DOM rather than relying on a status or visible overlay alone.

### Desktop UI simulations

```sh
PLAYWRIGHT_MODULE=/tmp/routenest-playwright/node_modules/playwright-core \
CHROMIUM_PATH=/usr/bin/chromium \
DESKTOP_VERIFY_WIDTHS=1440,390 \
ARTIFACT_DIR=/tmp/routenest-desktop-auth-repaired-simulation \
node tools/desktop_setup_verify.mjs
```

Passed **32 cases, 742 assertions, 80 screenshots**, with no injected missing
translations and no page/console errors. These are explicitly simulated bridge/API
journeys using the shipped assets. They do not validate Wails injection or OS storage.
The simulated error text contrast measured at least 6.12:1 in setup/login and 13.04:1
in the security card. Targeted checks are not an axe or WCAG conformance certification.

## Required transition journeys

| # | Journey | Evidence |
| --- | --- | --- |
| 1 | Fresh desktop password setup | Actual fresh root Go orchestration plus shipped-UI simulation |
| 2 | Recovery-code saving | One-time display and acknowledgement gates tested in Go/UI; real browser code-management acknowledgement |
| 3 | Explicit device enrollment | Authority plus verified fake-vault write/read-back/activation tests |
| 4 | Close/reopen desktop | Session lifecycle/reconstructed-launch tests; real packaged window close/reopen remains pending |
| 5 | Automatic desktop sign-in | Actual native Go acquisition/handoff/current session tests, not an OS-vault runtime claim |
| 6 | Ordinary browser still requires login | Real fresh browser contexts and private native-session isolation tests |
| 7 | Browser password login | Real UI and independent HTTP session verification |
| 8 | Sign out without immediate reauthentication | Atomic session/device pause and one-launch bridge guard; browser logout |
| 9 | Remove device authorization | Native/domain/HTTP policy and persistence tests |
| 10 | Re-enroll after password proof | Native revoked/expired/corrupt transitions; explicit consent and password reauthentication |
| 11 | Change administrator password | Actual browser form and native transport tests |
| 12 | Previous sessions/devices revoked | Cross-session/user/device domain/store/native tests and real browser inventories |
| 13 | Forgotten-password recovery | Actual browser reset UI and native recovery bridge tests |
| 14 | Old recovery code cannot be reused | Real old/replaced/used-code negative journeys plus persistence/replay tests |
| 15 | Replacement code works | Real replacement-code reset and verifier rotation |
| 16 | Invalid/replaced/revoked/expired proof and replayed/expired handoffs fail | Authority, native transport, private-context, lifetime, mutation, and replay tests |
| 17 | Unavailable OS storage falls back safely | Hung/unavailable/corrupt/late-write fake-vault and actual password-only native acquisition tests |
| 18 | CLI/headless unchanged | Existing CLI/auth tests, binary help/status, and real password-only HTTP startup; Docker runtime was unavailable |

## Files and compatibility

[The machine-readable file inventory](../review-artifacts/desktop-authentication/file-inventory.json)
separates authentication/integration, docs/CI, and evidence. The worktree already
contained unrelated changes, and concurrent branding/icon assets were preserved.
Review the full diff; the inventory is not a claim of exclusive authorship.

- Domain/persistence: `internal/adminauth/`, `internal/store/adminauth/inspect*`.
- Protected credential adapters: `internal/desktopauth/`.
- Native orchestration/bridge/lifecycle/logging: root `desktop_*.go` and focused tests.
- Shared HTTP/native transport: `internal/admin/desktop_*`, security route/handler
  wiring and negative tests.
- UI: setup assets, Administrator Security/login/recovery controllers and markup,
  localization, focus/contrast fixes, and explicit manifest wiring.
- Gateway lifecycle: synchronously owned listeners and joined cooldown recovery,
  with no routing/model/provider-selection policy change.
- Commands: `admin_cli*`, recovery command dispatch, thin CLI/desktop entry points.
- Verification/docs: `tools/*auth*`, `tools/admin_security_journeys_verify.mjs`, route
  goldens, workflows, and bilingual operational docs.

All thirteen canonical sidebar destinations and four UI aliases remain. The
pre-existing egress API rename is accompanied by protected compatibility aliases for
`/admin/api/cline-proxy/config` and `/admin/api/cline-proxy/config/update`, retaining
identical permissions and CSRF policy. Legacy file/directory migration remains intact.

Older strict-decoding binaries can fail closed once new populated trusted-device or
recovery metadata exists. Back up authority with its journal and account witness;
downgrade by restoring a compatible backup, never by deleting authentication fields.

## Screenshots and remaining runtime limits

[Selected review screenshots](../review-artifacts/desktop-authentication/README.md)
include actual browser login/security/recovery and separately labelled desktop UI
simulations. The persistent device secret is never in an artifact; actual recovery
values are masked. Ten representative PNGs and redacted browser reports are retained
in the review folder, with larger matrices in temporary artifact directories. All
parent-started test servers were stopped and their temporary passwords, account files,
bootstrap inputs, and administrator authority removed; only redacted reports and
screenshots were retained.

Real packaged Wails IPC delivery, WebView navigation/frame restrictions, Windows
Credential Manager, signed macOS Keychain access, Linux Secret Service prompts,
install/reopen/signed-upgrade behavior, clipboard delivery, and assistive-technology
behavior still require target-OS runtime testing. Missing local toolchains were tried
and recorded; compilation and simulations are not substitutes. Same-user malware or
host compromise is not prevented by OS-vault storage or an application name.
