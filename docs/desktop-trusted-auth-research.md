# Desktop trusted-auth credential research

Status: implementation research, 2026-11-09. “Trusted credential” below means a
revocable, random desktop token, not the administrator password, recovery code,
or browser cookie.

## Conclusions for RouteNest

1. **Upgrade Wails before adding a credential bridge.** RouteNest now pins
   `github.com/wailsapp/wails/v2 v2.17.1`. Wails 2.11.0 added binding origin
   checks; GHSA-47hv-j4px-h3c9 covers `>=2.0.0,<2.13.0`, and the advisory names
   **2.13.0 as the first patched release**. Wails 2.17.1 also fixes Linux child-
   frame access to privileged IPC under GHSA-r5j2-ghh9-2jqw.
   [advisory](https://github.com/wailsapp/wails/security/advisories/GHSA-47hv-j4px-h3c9),
   [2.17.1 release](https://github.com/wailsapp/wails/releases/tag/v2.17.1),
   [fix](https://github.com/wailsapp/wails/commit/bcc5941b72fdab6a13942b58871a2696faa85b81).
2. Store only an opaque, server-verifiable desktop credential in the OS store.
   Treat store read failure, lock, cancellation, corruption, unavailable service,
   or missing item as **not trusted**, without resetting authority or silently
   weakening storage. Pause automatic sign-in on sign-out; delete the local item on
   explicit Forget/revocation cleanup. Server expiry/revocation remains authoritative
   because local deletion can fail or copies may survive.
3. Use narrow OS-specific adapters behind one Go interface (`Get`, `Put`,
   `Delete`, and typed `NotFound`/`Unavailable`/`InteractionRequired` errors).
   Do not expose raw secret bytes or generic keychain operations through Wails;
   expose only high-level enroll/resume/forget methods, and zero temporary byte
   buffers where practical.

## Wails v2 binding and origin boundary

Wails binds every exported method of each instance in `options.App.Bind` and
makes generated asynchronous JavaScript methods for them. RouteNest binds
`DesktopSetupBridge`; its current public `State` and `Complete` methods are thus
IPC authority, not ordinary in-process calls. Wails' v2 documentation describes
this export model and generated bindings.
[official v2 architecture](https://github.com/wailsapp/wails/blob/v2.13.0/website/docs/howdoesitwork.mdx#method-binding).

In 2.11+, Wails constructs the default allow-origin from the WebView start URL
(`http://wails.localhost` on Windows and `wails://wails` on macOS/Linux) and
adds only configured `BindingsAllowedOrigins`. Windows checks both top-frame and
message-source origins; macOS rejects non-main-frame binding messages then checks
the source; Linux checks the current WebView URI. The option is explicitly for
**additional** origins and supports comma-separated host wildcard patterns.
[source introduction](https://github.com/wailsapp/wails/commit/857f7b75184f8f6563941e4a1eaa03f83e96d294),
[2.13 validator](https://github.com/wailsapp/wails/blob/v2.13.0/v2/internal/frontend/originvalidator/originValidator.go),
[options](https://github.com/wailsapp/wails/blob/v2.13.0/website/docs/reference/options.mdx#bindingsallowedorigins).

GHSA-47hv-j4px-h3c9 is specifically a wildcard suffix bypass: the old conversion
made `https://myapp.com*` match attacker origins such as
`https://myapp.com.attacker.com`. The 2.13 fix expands `*` only as a complete
origin component; partial/trailing forms are literals and fail closed. RouteNest
allows only the embedded Wails origin and keeps the console there; additional binding
origins are empty. Its in-process transport, rather than a socket reverse proxy, retains
native sessions and redeems handoffs. Origin checking is defense in depth, not authentication:
embedded/XSS-controlled JavaScript on
the legitimate asset origin can call every bound exported method, so each
credential operation must enforce lifecycle, one-shot/replay, and server-side
authorization invariants.

**Upgrade compatibility.** The public fields RouteNest uses (`AssetServer`,
`Bind`, `BindingsAllowedOrigins`, callbacks, `SingleInstanceLock`, and Windows options) remain in 2.17.1.
Wails 2.17.1 requires Go 1.26, and RouteNest declares Go 1.26.10. Run desktop
builds/tests on all three operating systems because source/API comparison cannot
prove WebView runtime or packaging compatibility.
[2.17.1 module](https://github.com/wailsapp/wails/blob/v2.17.1/v2/go.mod),
[2.17.1 release](https://github.com/wailsapp/wails/releases/tag/v2.17.1).

## Windows: Credential Manager preferred; DPAPI is a blob primitive

Use a `CRED_TYPE_GENERIC` credential with a stable namespaced `TargetName`
(e.g. `RouteNest:desktop-trust:v1`), opaque bytes in `CredentialBlob`, and
`CRED_PERSIST_LOCAL_MACHINE`. Microsoft says generic credentials are stored
securely but otherwise application-defined; `(TargetName, Type)` identifies the
item; `CredWriteW` replaces an existing match. The blob limit is 2,560 bytes.
`LOCAL_MACHINE` means this user across later logon sessions on this computer—not
all machine users; `SESSION` ends at logout and `ENTERPRISE` may roam, so neither
fits device-local trust. `CredReadW`/`CredDeleteW` operate on the current token's
logon-session credential set and can return `ERROR_NOT_FOUND` or
`ERROR_NO_SUCH_LOGON_SESSION`. Free returned data with `CredFree`.
[`CREDENTIALW`](https://learn.microsoft.com/en-us/windows/win32/api/wincred/ns-wincred-credentialw),
[`CredWriteW`](https://learn.microsoft.com/en-us/windows/win32/api/wincred/nf-wincred-credwritew),
[`CredReadW`](https://learn.microsoft.com/en-us/windows/win32/api/wincred/nf-wincred-credreadw),
[`CredDeleteW`](https://learn.microsoft.com/en-us/windows/win32/api/wincred/nf-wincred-creddeletew).

Concrete Go choice: `github.com/danieljoos/wincred` wraps these APIs and defaults
new generic credentials to local-machine persistence; direct calls are also
possible with Windows-only files and `golang.org/x/sys/windows`. Avoid `cmdkey`
or PowerShell: they expose secrets in process arguments and `cmdkey` cannot read
secret material.
[wincred API](https://pkg.go.dev/github.com/danieljoos/wincred).

DPAPI (`CryptProtectData`/`CryptUnprotectData`) is the alternative when RouteNest
must own the encrypted file format. Default protection is normally same user and
same computer and includes a MAC; `CRYPTPROTECT_LOCAL_MACHINE` instead permits
**any user on that computer**, so do not set it. RouteNest must atomically persist,
permission, version, locate, delete, and back up the ciphertext blob, and must
use the same optional entropy on decrypt. Use `CRYPTPROTECT_UI_FORBIDDEN`, pass a
null prompt structure, `LocalFree` outputs, and clear plaintext. Microsoft's
prompt-based DPAPI flow is deprecated for removal in April 2027. DPAPI and
Credential Manager protect at user/machine context, not by RouteNest code
signature; neither source promises isolation from all code already executing as
the same user.
[`CryptProtectData`](https://learn.microsoft.com/en-us/windows/win32/api/dpapi/nf-dpapi-cryptprotectdata),
[`CryptUnprotectData`](https://learn.microsoft.com/en-us/windows/win32/api/dpapi/nf-dpapi-cryptunprotectdata),
[prompt deprecation](https://learn.microsoft.com/en-us/windows/win32/api/dpapi/ns-dpapi-cryptprotect_promptstruct).

## macOS: use SecItem and preserve signing identity

Apple recommends `SecItem` and, unless legacy-item access is required, the data
protection keychain via `kSecUseDataProtectionKeychain=true`. Store a
`kSecClassGenericPassword` selected by fixed `kSecAttrService` and
`kSecAttrAccount`, with bytes in `kSecValueData`, no iCloud synchronization, and
`kSecAttrAccessibleWhenUnlocked`. The latter is the default and limits access to
when the user has unlocked the device. SecItem operations block, so run them off
the UI thread. Handle duplicate/not-found, unavailable/auth-failed,
interaction-not-allowed, and user-cancel separately.
[Apple TN3137](https://developer.apple.com/documentation/technotes/tn3137-on-mac-keychains),
[`kSecUseDataProtectionKeychain`](https://developer.apple.com/documentation/security/ksecusedataprotectionkeychain),
[`SecItemAdd`](https://developer.apple.com/documentation/security/secitemadd(_:_:)),
[`SecItemCopyMatching`](https://developer.apple.com/documentation/security/secitemcopymatching(_:_:)),
[`kSecAttrAccessibleWhenUnlocked`](https://developer.apple.com/documentation/security/ksecattraccessiblewhenunlocked).

Data-protection keychain access groups come from code-signing entitlements and
must be authorized by a provisioning profile. The app ID (team ID + bundle ID)
is its private default group; an app in that group can read the item. Ship every
upgrade with a stable bundle identifier, team/signing identity, entitlements,
and designated requirement. Apple documents that Keychain access controls track
the creating app by its designated requirement and that consistently signed
updates avoid repeated access prompts. Ad-hoc/self-signed development and
production identities must therefore use separate test items; changing identity
can make existing items inaccessible or prompt the user.
[access groups](https://developer.apple.com/documentation/security/sharing-access-to-keychain-items-among-a-collection-of-apps),
[code-signing upgrade identity](https://developer.apple.com/library/archive/documentation/Security/Conceptual/CodeSigningGuide/Introduction/Introduction.html),
[keychain ACL/designated requirements](https://developer.apple.com/library/archive/technotes/tn2206/_index.html#//apple_ref/doc/uid/DTS40007919-CH1-TNTAG2).

Concrete Go choice: a small cgo adapter to Security.framework offers exact
`SecItem*`/data-protection behavior. Existing `github.com/zalando/go-keyring`
executes `/usr/bin/security`, which Apple says is primarily focused on the legacy
file-based keychain; it does not select the data-protection keychain. If used as
a compatibility compromise, invoke the absolute path, provide the secret through
the helper's stdin rather than as an `exec` argument, never use `-A` (the tool
labels it insecure), and understand that the helper—not RouteNest—is the item
creator. `-T appPath` applies legacy ACL/path semantics and is not a substitute
for stable app signing. A native adapter avoids
helper stdout/argv and creator-identity ambiguity.
[`security(1)`](https://keith.github.io/xcode-man-pages/security.1.html),
[go-keyring implementation](https://github.com/zalando/go-keyring/blob/v0.2.8/keyring_darwin.go).
This helper implementation actually puts an encoded secret into an interactive
command line sent over the child's stdin; it avoids OS process arguments but not
plaintext in the helper's command parser/memory.

## Linux: Secret Service is optional; fail closed when absent

Secret Service is a D-Bus API for a service in the user's login session, not a
universal Linux facility. Connect to the session bus and
`org.freedesktop.secrets`, use the default collection alias, and identify an
item with non-secret attributes such as `application=RouteNest`,
`purpose=desktop-trust`, `version=1`. Attributes may be stored unencrypted, so
never put tokens or sensitive account data there. Collections/items may be
locked at any time; `Unlock` may return a prompt object, which the client must
show and await, and cancellation must remain an authentication failure.
[introduction](https://specifications.freedesktop.org/secret-service-spec/latest/ch01.html),
[unlocking](https://specifications.freedesktop.org/secret-service-spec/latest/unlocking.html),
[prompts](https://specifications.freedesktop.org/secret-service-spec/latest/prompts.html).

The specification says secrets are **usually** encrypted at rest and explicitly
does not mandate master passwords or any access-control model. Transfer also may
use the `plain` session algorithm and its encryption is not intended to defeat an
active attacker. Do not claim app-identity isolation or guaranteed encrypted-at-
rest storage across providers.
[scope limits](https://specifications.freedesktop.org/secret-service-spec/latest/ch10.html),
[transfer limits](https://specifications.freedesktop.org/secret-service-spec/latest/transfer-secrets.html).

Concrete Go choice: `github.com/zalando/go-keyring` is pure Go on Linux and uses
`godbus`, but its current implementation opens a **plain** Secret Service session
and expects a usable login/default collection. A direct `godbus` adapter can
negotiate the spec's DH/AES session and attach prompts to the Wails window, at
higher implementation/test cost. Native `secret-tool` is optional
(`libsecret-tools`), may block for prompts, returns secrets on stdout, and
accepts stored secrets on stdin; never pass them in argv. libsecret's synchronous
APIs may block indefinitely and should not run on a UI thread.
[go-keyring source](https://github.com/zalando/go-keyring/blob/v0.2.8/secret_service/secret_service.go),
[libsecret API](https://gnome.pages.gitlab.gnome.org/libsecret/),
[`secret-tool`](https://manpages.ubuntu.com/manpages/noble/en/man1/secret-tool.1.html).

If the session bus, service, collection, or prompt UI is unavailable (minimal
WM, headless launch, SSH, container, or broken keyring), disable “trust this
desktop” and require normal login. The secure default is **no plaintext file and
no automatic software-key fallback**. An explicit fallback can be offered only
as a separately consented encrypted vault with a user-supplied passphrase,
a memory-hard KDF, authenticated encryption, mode `0600`, atomic writes, and no
automatic unlock; that is a different UX and threat model, not equivalent to an
OS credential store. RouteNest does not implement that fallback.

## Implemented platform adapters

- Windows uses the current user's generic Credential Manager through go-keyring/wincred.
- Linux uses the current login session's Secret Service through go-keyring/godbus.
  Collection access and prompts depend on the installed Secret Service provider.
- macOS now uses direct `SecItem` calls through Security.framework, scoped to the default
  file-based Keychain. Omitting a broad access override retains the creating application's
  Keychain access controls. This avoids `/usr/bin/security` helper-created items, but is
  not an entitled data-protection Keychain implementation. Stable distribution signing
  and upgrade behavior still require real macOS verification. CGO-disabled macOS builds
  explicitly report unavailable storage and fall back to password login.
- All operations have a five-second default total budget. One production OS call may
  remain blocked, but later calls do not create unlimited waiting goroutines or block a
  manager mutex indefinitely. A timed-out mutation quarantines that installation slot
  within the process until verified removal reconciles it. Durable registration
  activation/revocation, not this in-memory quarantine, prevents late OS writes from
  granting access after a restart.
- Installation slots use a hash of the selected absolute data-directory namespace, not
  a port, process name, or administrator password. There is no plaintext file fallback.

## Limitations and required validation

Primary documentation does not establish resistance to malware running as the
same logged-in user; OS stores reduce at-rest exposure, not endpoint compromise.
The Wails sources describe origin gating but do not make a bound method safe from
same-origin XSS. No cross-platform Go library examined exactly combines Windows
Credential Manager, macOS data-protection SecItem, encrypted Secret Service
transport, prompt/window integration, and fail-closed fallback. Before release,
test install → enroll → restart → signed upgrade → retrieve → revoke/delete on
each supported OS, plus locked store, cancelled prompt, missing service, corrupt
item, user switch, logout, offline server, and old-signature migration cases.