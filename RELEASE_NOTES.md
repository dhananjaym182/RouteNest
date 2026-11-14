# RouteNest v0.1.0-beta.1 - First public preview

**Prerelease · for evaluation, not a stable or production-certified release.** APIs, state formats and upstream integrations may change during the beta. Back up existing data before trying it.

## Highlights

- One local AI gateway for OpenAI Chat Completions, a supported subset of OpenAI Responses, and Anthropic Messages, with streaming, tools, reasoning content and usage where the upstream supports them.
- Account rotation, cooldowns, ordered model combos, fallback chains, aliases, optional session affinity and a read-only Route check.
- 111 provider definitions (109 selectable presets), custom OpenAI-compatible providers, local runtimes, model discovery, provider limits and per-provider egress controls.
- An embedded English/Chinese admin console with light/dark themes, request logs, one-time password setup, roles, OIDC/SAML, recovery codes and optional OS-protected desktop trust.
- Built with Go 1.26.11 and current patched SAML, JOSE and Echo dependencies.

## Downloads and runtime requirements

Get your archive, its matching `.spdx.json` SBOM and `SHA256SUMS` from the [official release](https://github.com/dhananjaym182/RouteNest/releases/tag/v0.1.0-beta.1).

| Platform | Release archive | Runtime requirements |
| --- | --- | --- |
| Windows x64 | `routenest_0.1.0-beta.1_windows_amd64.zip` | Microsoft Edge WebView2 Runtime; install Microsoft's Evergreen Runtime if missing. |
| macOS Apple Silicon | `routenest_0.1.0-beta.1_darwin_arm64.tar.gz` | macOS with system WKWebView; built on macOS 15. No Intel macOS artifact. |
| Linux x64 | `routenest_0.1.0-beta.1_linux_amd64.tar.gz` | Compatible glibc-based desktop with GTK 3 and WebKitGTK **4.0**; built on Ubuntu 22.04. WebKitGTK 4.1 alone is insufficient. |

Each archive contains `routenest_<version>_<platform>/` with `routenest-desktop.exe` (Windows) or `routenest-desktop` (macOS/Linux), `LICENSE`, `THIRD_PARTY_NOTICES.md` and these notes as `README.md`. The three separate SBOM assets are `routenest_0.1.0-beta.1_windows_amd64.spdx.json`, `routenest_0.1.0-beta.1_darwin_arm64.spdx.json` and `routenest_0.1.0-beta.1_linux_amd64.spdx.json`. `SHA256SUMS` covers all three archives and three SBOMs; GitHub build attestations cover those files and the manifest. SBOMs are inventories, not vulnerability-free certification.

No Go, Node.js or development headers are needed to run the extracted app. Linux desktop trust optionally needs an unlocked Secret Service; password sign-in remains available without it. There is no installer, Intel macOS download, separate CLI asset or published container image. CLI/server and Docker are source-build options using Go 1.26.11 or newer; desktop source builds also need the native toolchain.

**Unsigned / unnotarized:** Windows has no trusted code signature; macOS has no Developer ID signature or notarization. SmartScreen or Gatekeeper may block launch. Verify first, then approve only the individual trusted executable using **More info → Run anyway** on Windows or **Open / Privacy & Security** on macOS. Do not disable system-wide protections. Checksums and attestations do not remove these warnings.

## Install and verify

Use GitHub CLI 2.97.0 or newer in an empty download directory; this is the first version line used to validate the `gh release verify` commands below. Verify the archive **before extraction**, its SBOM and `SHA256SUMS`. Stop on a missing entry, mismatch, failed or missing attestation. The commands enforce this repository's Build workflow and `refs/tags/v0.1.0-beta.1`.

**Windows (PowerShell)**

```powershell
$ErrorActionPreference = "Stop"
$base = "routenest_0.1.0-beta.1_windows_amd64"
gh release verify v0.1.0-beta.1 --repo dhananjaym182/RouteNest
if ($LASTEXITCODE -ne 0) { throw "Immutable release verification failed" }
gh release download v0.1.0-beta.1 --repo dhananjaym182/RouteNest --pattern "$base.zip" --pattern "$base.spdx.json" --pattern SHA256SUMS
if ($LASTEXITCODE -ne 0) { throw "Download failed" }
foreach ($file in @("$base.zip", "$base.spdx.json", "SHA256SUMS")) {
    gh release verify-asset v0.1.0-beta.1 $file --repo dhananjaym182/RouteNest
    if ($LASTEXITCODE -ne 0) { throw "Immutable release asset verification failed: $file" }
}
foreach ($file in @("$base.zip", "$base.spdx.json")) {
    $entries = @(Select-String -Path .\SHA256SUMS -Pattern ("^[0-9a-f]{64}  " + [regex]::Escape($file) + "$"))
    if ($entries.Count -ne 1) { throw "Missing or duplicate checksum: $file" }
    $expected = ($entries[0].Line -split '\s+')[0]
    if ((Get-FileHash $file -Algorithm SHA256).Hash -ne $expected) { throw "SHA-256 mismatch: $file" }
}
foreach ($file in @("$base.zip", "$base.spdx.json", "SHA256SUMS")) {
    gh attestation verify $file --repo dhananjaym182/RouteNest --signer-workflow dhananjaym182/RouteNest/.github/workflows/build.yml --source-ref refs/tags/v0.1.0-beta.1
    if ($LASTEXITCODE -ne 0) { throw "Attestation verification failed: $file" }
}
Expand-Archive -Path "$base.zip" -DestinationPath .
Start-Process ".\$base\routenest-desktop.exe"
```

**macOS Apple Silicon**

```bash
(
set -euo pipefail
base=routenest_0.1.0-beta.1_darwin_arm64
gh release verify v0.1.0-beta.1 --repo dhananjaym182/RouteNest
gh release download v0.1.0-beta.1 --repo dhananjaym182/RouteNest --pattern "$base.tar.gz" --pattern "$base.spdx.json" --pattern SHA256SUMS
for file in "$base.tar.gz" "$base.spdx.json" SHA256SUMS; do
    gh release verify-asset v0.1.0-beta.1 "$file" --repo dhananjaym182/RouteNest
done
for file in "$base.tar.gz" "$base.spdx.json"; do
    awk -v file="$file" '$2 == file { print; n++ } END { if (n != 1) exit 1 }' SHA256SUMS | shasum -a 256 -c -
done
for file in "$base.tar.gz" "$base.spdx.json" SHA256SUMS; do
    gh attestation verify "$file" --repo dhananjaym182/RouteNest --signer-workflow dhananjaym182/RouteNest/.github/workflows/build.yml --source-ref refs/tags/v0.1.0-beta.1
done
tar -xzf "$base.tar.gz"
chmod +x "$base/routenest-desktop"
"./$base/routenest-desktop"
)
```

**Linux x64**

On Ubuntu 22.04, install the runtime libraries if needed with `sudo apt install libgtk-3-0 libwebkit2gtk-4.0-37`.

```bash
(
set -euo pipefail
base=routenest_0.1.0-beta.1_linux_amd64
gh release verify v0.1.0-beta.1 --repo dhananjaym182/RouteNest
gh release download v0.1.0-beta.1 --repo dhananjaym182/RouteNest --pattern "$base.tar.gz" --pattern "$base.spdx.json" --pattern SHA256SUMS
for file in "$base.tar.gz" "$base.spdx.json" SHA256SUMS; do
    gh release verify-asset v0.1.0-beta.1 "$file" --repo dhananjaym182/RouteNest
done
for file in "$base.tar.gz" "$base.spdx.json"; do
    awk -v file="$file" '$2 == file { print; n++ } END { if (n != 1) exit 1 }' SHA256SUMS | sha256sum --check -
done
for file in "$base.tar.gz" "$base.spdx.json" SHA256SUMS; do
    gh attestation verify "$file" --repo dhananjaym182/RouteNest --signer-workflow dhananjaym182/RouteNest/.github/workflows/build.yml --source-ref refs/tags/v0.1.0-beta.1
done
tar -xzf "$base.tar.gz"
chmod +x "$base/routenest-desktop"
"./$base/routenest-desktop"
)
```

Confirm the archive and SBOM checksum results are `OK` and provenance identifies the expected repository, workflow and tag. Checksums check bytes; build attestations check provenance, not code safety, OS signing or provider certification. These instructions verify the packaged files, not the extracted executable against an archive checksum.

The `gh release verify` and `verify-asset` steps require an immutable published release. They bind each downloaded asset digest to that release and are separate from Build-workflow provenance; a workflow-created draft is not proof of immutability.

## Setup and preview caveats

- Fresh desktop setup creates your administrator password and shows a recovery code once; save it privately. Device trust is optional, browsers sign in independently, and closing the window stops the gateway at `127.0.0.1:3457`.
- Connect an authorized provider, enable a model and create a client API key before relying on authenticated inference. A validated empty key list leaves model/inference routes anonymous for compatibility; client keys never grant administrator access. Headless first-run controls are not password-reset mechanisms.
- Keep hosted administration behind HTTPS with an exact canonical URL and narrow trusted-proxy CIDRs; do not expose the backend directly. See the [security guide](https://github.com/dhananjaym182/RouteNest/blob/v0.1.0-beta.1/docs/admin-security.md).
- Provider presets are templates, **not certification**. Verify each provider with a small request. Free-first/free-only labels are not zero-cost guarantees. Follow upstream terms, entitlements, pricing and limits; Cline account access is unofficial, and its free-model path is not supported by Cline's API documentation.
- Responses is a chat-completions-backed **subset**, not the full OpenAI API. Non-text protocol translation is unsupported, structured outputs vary by upstream and token counts are local estimates.
- Provider credentials, refresh tokens and generated client keys remain **plaintext**, protected by filesystem permissions, not encryption at rest. Secure data, OAuth captures and backups. OS-vault protection applies to desktop-trust credentials, not all provider secrets, and does not defeat same-user malware.
- The built-in update check follows stable releases only; it may not advertise this beta. Obtain previews explicitly from the tagged release page.

## Upgrade and validation scope

Stop every RouteNest process and securely back up the entire selected data directory before replacing the app. Preserve `.routenest-accounts.json` with its `.initialized` witness and the administrator snapshot with its journal; include referenced SSO secrets and save the recovery code separately. Reuse the same directory and OS user. Never delete identity files or use first-run flags to reset an established install. Downgrade only with the old executable and a complete compatible backup; OS-vault trust is not portable with that backup.

CI is configured for Go tests, vet and race on Linux/Windows; frontend/localization, Docker persistence smoke, authenticated Chromium journeys and desktop bridge simulations on Linux; native desktop builds, production-tag test compilation and windowless `/ready` self-checks for all three platforms; and archive/SBOM/manifest validation and attestations before creating a reviewable prerelease draft. Consult the tagged run for actual results. These checks do **not** certify packaged WebViews, real OS credential stores, clean-system install/reopen, signed upgrades, every OS version or every live provider.

[Changelog](https://github.com/dhananjaym182/RouteNest/blob/v0.1.0-beta.1/CHANGELOG.md) · [English README](https://github.com/dhananjaym182/RouteNest/blob/v0.1.0-beta.1/README.md) · [中文说明](https://github.com/dhananjaym182/RouteNest/blob/v0.1.0-beta.1/README.zh-CN.md) · [Desktop guide](https://github.com/dhananjaym182/RouteNest/blob/v0.1.0-beta.1/desktop/README.md)
