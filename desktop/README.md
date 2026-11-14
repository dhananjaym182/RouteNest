# RouteNest Desktop

The Wails v2 desktop app embeds the gateway, admin console and native setup window in one executable. It uses the operating system's WebView; the WebView runtime is not bundled.

## First public preview

**[RouteNest v0.1.0-beta.1 - First public preview](https://github.com/dhananjaym182/RouteNest/releases/tag/v0.1.0-beta.1)** is a prerelease for evaluation, not a stable or production-certified release. APIs, state formats and upstream integrations may change. Back up existing data before trying it.

The preview publishes versioned desktop archives, three matching SPDX SBOMs and `SHA256SUMS`, not an installer, an Intel macOS artifact, a separate CLI executable or a container image. See [release notes](../RELEASE_NOTES.md) for download/install commands and [the changelog](../CHANGELOG.md) for capabilities and limitations.

## Supported platforms and runtime requirements

| Platform | Release asset | Runtime requirements |
| --- | --- | --- |
| Windows x64 | `routenest_0.1.0-beta.1_windows_amd64.zip` | Microsoft Edge WebView2 Runtime. Windows 11 includes it; updated Windows 10 usually has it. Install Microsoft's Evergreen Runtime if it is missing. |
| macOS Apple Silicon | `routenest_0.1.0-beta.1_darwin_arm64.tar.gz` | macOS with system WKWebView; built on macOS 15. No Xcode tools are needed to run it. No Intel macOS artifact is published. |
| Linux x64 | `routenest_0.1.0-beta.1_linux_amd64.tar.gz` | Compatible glibc-based desktop with GTK 3 and WebKitGTK **4.0**; built on Ubuntu 22.04. WebKitGTK 4.1 alone cannot run this artifact. |

Each archive extracts to a folder of the same base name containing `routenest-desktop.exe` (Windows) or `routenest-desktop` (macOS/Linux), `LICENSE`, `THIRD_PARTY_NOTICES.md` and release instructions as `README.md`. Separate SBOMs are `routenest_0.1.0-beta.1_windows_amd64.spdx.json`, `routenest_0.1.0-beta.1_darwin_arm64.spdx.json` and `routenest_0.1.0-beta.1_linux_amd64.spdx.json`. An SBOM inventories the built artifact; it is not vulnerability-free certification.

No Go, Node.js or development headers are needed to run a download. Linux desktop trust optionally requires an unlocked Secret Service in the login session; unavailable protected storage falls back to password sign-in. The app needs a graphical desktop; use the source-built CLI/server for headless deployments. Older OS versions and clean-system installation are not broadly certified.

On Ubuntu 22.04, the runtime packages are:

```bash
sudo apt install libgtk-3-0 libwebkit2gtk-4.0-37
```

If your distribution no longer supplies WebKitGTK 4.0, use a source build targeting 4.1 or the CLI/server, not a library-name symlink or an ABI substitution.

## Verify before opening

Download only from the official tagged release. Before extraction, check the archive and SBOM against `SHA256SUMS`, then verify GitHub build attestations for both files and the manifest with a recent GitHub CLI. The manifest covers all three archives and three SBOMs. After downloading the Linux archive, SBOM and manifest:

```bash
(
set -euo pipefail
base=routenest_0.1.0-beta.1_linux_amd64
for file in "$base.tar.gz" "$base.spdx.json"; do
    awk -v file="$file" '$2 == file { print; n++ } END { if (n != 1) exit 1 }' SHA256SUMS | sha256sum --check -
done
for file in "$base.tar.gz" "$base.spdx.json" SHA256SUMS; do
    gh attestation verify "$file" --repo dhananjaym182/RouteNest --signer-workflow dhananjaym182/RouteNest/.github/workflows/build.yml --source-ref refs/tags/v0.1.0-beta.1
done
)
```

Both archive and SBOM checksums must report `OK`; all attestations must identify this repository's Build workflow and release tag. Stop on a mismatch, failed or missing attestation. [Release notes](../RELEASE_NOTES.md#install-and-verify) include Windows `Get-FileHash`, macOS `shasum`, download, extraction and launch commands. Verify packaged files, not the extracted executable against its archive digest. Checksums and attestations do not certify code safety or platform/provider compatibility.

For an immutable published release, additionally verify GitHub's signed release/asset binding before extraction or launch:

```bash
gh release verify v0.1.0-beta.1 --repo dhananjaym182/RouteNest &&
gh release verify-asset v0.1.0-beta.1 ./routenest_0.1.0-beta.1_linux_amd64.tar.gz --repo dhananjaym182/RouteNest
```

Substitute your platform's archive and repeat `verify-asset` for its SBOM and `SHA256SUMS` when checking every asset. Immutable-release verification is separate from Build-workflow provenance and requires an immutable published release; a draft does not prove immutability.

**Unsigned / unnotarized:** Windows has no trusted code signature, and macOS has no Developer ID signature or notarization. After verification, SmartScreen may require **More info → Run anyway**; Gatekeeper may require **Open** or approval under **System Settings → Privacy & Security**. Approve only the individual verified file; do not disable system-wide protections. Provenance verification does not remove these OS warnings.

## First launch and security expectations

1. Open RouteNest normally. On a confirmed fresh data directory, native setup creates the client-authentication store without a terminal, environment variable or copied bootstrap secret.
2. Create your permanent administrator password and save the recovery code shown once. Optionally trust this desktop device for automatic sign-in. The device credential is random and revocable, stored in Windows Credential Manager, macOS Keychain or Linux Secret Service; the administrator password is never stored there.
3. Connect a provider you are authorized to use, enable a model and test it with a small request. The Quick Start page generates examples from your actual configuration; use a model ID from `/v1/models` or an enabled combo, not a guaranteed preset model.
4. Generate a client API key before relying on authenticated model/inference access. A validated, intentionally empty key list leaves those routes anonymous for compatibility; administrator authentication remains separate and mandatory.
5. Point OpenAI clients at `http://127.0.0.1:3457/v1` or Anthropic clients at `http://127.0.0.1:3457`. Closing the desktop window stops the entire app and gateway.

On later launches, a valid enabled trusted device receives a fresh ordinary administrator session. Browsers still sign in independently. **Administrator Security → Desktop Sign-In** manages pause, resume, forget and revocation. Lock ends the session; Sign out also pauses automatic sign-in; Forget this device revokes the registration and removes its local credential.

**Forgot password?** uses the saved single-use recovery code, resets the main administrator password, revokes sessions and trusted devices, and shows one replacement code once. If every recovery credential is lost, use the owner-controlled offline procedure in [the security guide](../docs/admin-security.md#offline-recovery). Localhost access alone is not recovery authority. Missing, corrupt or partially restored established authentication state requires recovery, not fresh setup.

The gateway binds to `127.0.0.1` by default. A hosted/non-loopback listener requires an exact canonical URL and narrow trusted-proxy configuration; use HTTPS termination and never expose the backend directly. No automatic LAN exposure is enabled by installation.

Provider credentials, refresh tokens and generated client keys remain **plaintext in the data directory**, protected by filesystem permissions rather than encryption at rest. Desktop OS-vault trust does not encrypt provider secrets or defeat same-user malware. Protect capture logs, data and backups. See [security guidance](../README.md#security).

## Preview limitations

- Presets are configuration templates, not certification of all providers, models, authentication flows or account entitlements. Follow upstream terms, pricing and limits. Free-first/free-only routing is not a zero-cost guarantee, and the Cline account integration is unofficial.
- OpenAI Responses is a chat-completions-backed subset. Full Responses features, non-text cross-protocol translation and normalized structured output are not promised; token counts are local estimates.
- No Intel macOS download or automatic installer is provided. Linux downloads require WebKitGTK 4.0; a source build against 4.1 does not change that release dependency.
- Native build success, windowless self-checks and UI simulations do not certify actual packaged WebViews, real OS credential stores, clean-system install/reopen, signed upgrades or every live provider.

## Data, upgrades and backups

State uses RouteNest's normal per-user config directory, not the executable directory:

- Windows: `%AppData%\routenest`
- macOS: `~/Library/Application Support/routenest`
- Linux: `$XDG_CONFIG_HOME/routenest`, normally `~/.config/routenest`

`ROUTENEST_DATA_DIR` overrides the directory; the legacy `CLINE2API_DATA_DIR` is lower priority. Unix private directories/files use `0700`/`0600`. Windows protected ACLs grant the current user and SYSTEM access, while accepting Windows-assigned ownership for files this process creates. Durable writes sync a temporary file, replace it using Go's directory-rooted rename on Windows and attempt a directory flush; they do not claim a write-through move or universal power-loss guarantee. Uncertain post-replacement failures are not reported as ordinary success.

Before an upgrade, stop every RouteNest process and securely back up the whole selected directory, including `.routenest-accounts.json` with its `.initialized` witness, and the administrator snapshot with its journal. Include referenced SSO secrets and save the recovery code separately. Verify the new archive, SBOM and manifest before extraction, extract into a separate directory, replace only the executable, and reuse the same data directory and OS user. Never use initialization flags or delete identity files to reset an established installation.

Older binaries may reject newer beta administrator metadata. Downgrade with the old executable and a complete compatible backup, not mixed snapshots/journals or hand-edited identity fields. OS-vault trust is not part of a portable directory backup; moving user, machine or directory may require password login and explicit re-enrollment. Details: [Data and upgrades](../README.md#data-and-upgrades).

## Building for each platform

Source builds require **Go 1.26.11 or newer**. Wails uses native WebView bindings: build on each target OS, and keep compilation separate from runtime validation. `desktop/build.sh` builds the current OS/architecture with `desktop production` tags and embeds version, commit and build date. Set `VERSION=v0.1.0-beta.1` for a local version-labelled build.

### Windows

Use a shell that can run the build script (for example Git Bash), Go and a C toolchain. WebView2 is needed to run the result.

```bash
./desktop/build.sh
```

Output: `desktop/build/windows-amd64/routenest-desktop.exe`.

The icon, file-version metadata and application manifest come from the tracked root `resource_windows_amd64.syso`. To regenerate after an approved resource change:

```bash
go -C desktop/icon-gen run . -out ../../resource_windows_amd64.syso
```

This writes the root `resource_windows_amd64.syso` directly. Review the generated resource before building. The generator's version constants control Windows file properties separately from the embedded application version.

### macOS

Install Xcode Command Line Tools, then link the native framework used by the desktop build:

```bash
xcode-select --install
CGO_LDFLAGS="${CGO_LDFLAGS:+$CGO_LDFLAGS }-framework UniformTypeIdentifiers" ./desktop/build.sh
```

Output: `desktop/build/darwin-arm64/routenest-desktop` on Apple Silicon or `desktop/build/darwin-amd64/routenest-desktop` on Intel. Intel is a source-build option, not a published preview artifact or a CI-certified target. This does not perform Developer ID signing or notarization.

### Linux

The default script and release workflow use GTK 3 and WebKitGTK **4.0**. On Ubuntu 22.04:

```bash
sudo apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.0-dev
./desktop/build.sh
```

Output: `desktop/build/linux-amd64/routenest-desktop`.

On a distribution with WebKitGTK **4.1** development packages, add the `webkit2_41` tag explicitly. For example, on Ubuntu 24.04:

```bash
sudo apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev
go build -tags "desktop production webkit2_41" -trimpath -o routenest-desktop .
```

This local build needs 4.1 runtime libraries and is not the official 4.0-linked artifact. Equivalent distribution packages may have different names. Development packages are for compilation only; release users need matching runtime libraries.

## Packaging and signing local builds

Keep build output separate from live state. Never bundle `.routenest-*`, `admin-auth/`, `capture-logs/`, legacy `.cline-*` credentials or your data-directory backup into a package.

`desktop/dist.sh` is a Windows-only local ZIP helper, not the published release workflow. It does not sign, attest or notarize the result, and its bundled instructions are not the authoritative preview setup guide; distribute the current security/setup guidance with local packages.

`desktop/sign-self.ps1` is an optional local self-signing helper. A self-signed certificate is not a trusted publisher identity and does not eliminate SmartScreen warnings; signing also changes the file digest, so it no longer matches official checksums or attestations. Do not import an unfamiliar root certificate or bypass system-wide protections. macOS Developer ID signing and notarization are not performed by the preview workflow.

## Debug builds and self-checks

A Windows build with console output omits the GUI-subsystem linker flag:

```bash
go build -tags "desktop production" -o routenest-desktop-debug.exe .
```

A self-check starts the gateway, verifies `/ready` and exits **without opening a window or exercising an OS credential store**. Use a disposable data directory that contains no live or legacy credentials, never your normal installation. A fresh self-check needs explicit first-run authorization and a private, test-only bootstrap value; remove those variables afterwards.

```bash
./routenest-desktop -selfcheck -initialize-empty-account-store
```

The artifact filename differs by platform. `-initialize-empty-account-store` is an advanced self-check/headless control, not required for normal native setup and never a password reset.

## CI validation scope

The [Test workflow](../.github/workflows/test.yml) covers module verification, Go tests/vet/race on Linux and Windows, desktop-tag authentication tests, dedicated Windows persistence tests, JavaScript/localization contracts, authenticated Chromium journeys, a desktop bridge simulation and a Docker persistence smoke test.

The [Build workflow](../.github/workflows/build.yml) depends on those tests, validates release metadata, compiles production-tag desktop tests without executing them, builds natively on Windows x64, macOS 15 arm64 and Ubuntu 22.04 x64, and runs windowless readiness self-checks. It packages versioned archives with license/notices and instructions, generates SPDX SBOMs, validates the exact asset inventory and `SHA256SUMS`, and generates tag-bound build/SBOM attestations before creating a reviewable prerelease draft. Read the tagged run for actual results, not historical reports; a draft is not publication or proof of immutability.

Simulations, native builds, checksums and provenance do not certify packaged Wails/WebView IPC, real Windows Credential Manager/macOS Keychain/Linux Secret Service behavior, installation/reopen, signed upgrades, all OS versions or every live provider.

## Technical notes

- `go build .` builds the CLI/server, not the desktop. Native desktop builds use both `desktop` and `production`; Linux 4.1 builds also need `webkit2_41`.
- The built-in update check follows stable releases only; obtain beta previews explicitly from their tagged release page.
- `CLINE_PROXY_PORT` or `-port` overrides port `3457`. An occupied gateway port is rejected, not killed or treated as this installation.
- Native setup and the console stay on the embedded Wails origin with no extra binding origins. Private Go API transport reuses administrator sessions, server-side roles and session-bound CSRF; ordinary localhost browsers cannot redeem native session handoffs.
- Trusted-device secrets use the OS credential-store adapter with no plaintext fallback. Password login remains available when the vault is unavailable, locked or corrupt.
