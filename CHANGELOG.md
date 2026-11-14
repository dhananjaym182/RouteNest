# Changelog

Notable user-facing changes are grouped by release, newest first. Beta releases are previews, not stable releases.

## Unreleased

## [0.1.0-beta.1] - 2026-11-14

**RouteNest v0.1.0-beta.1 - First public preview.** This is the first public prerelease for evaluation; APIs and state formats may change during the beta.

### Added

- A self-hosted AI gateway with OpenAI Chat Completions, a chat-completions-backed subset of OpenAI Responses, and Anthropic Messages, including streaming, tools, reasoning content and usage where supported by the upstream.
- Account rotation, per-model cooldowns, ordered routing combos, a global fallback chain, aliases, optional session affinity and a read-only Route check.
- 111 built-in provider definitions, including 109 selectable presets, custom OpenAI-compatible endpoints and local model runtimes. Model discovery supports approval of newly found models and provider-scoped offering management.
- Provider limits, usage and quota views, request-attempt logs, a default egress proxy pool, per-provider overrides and a separate OpenCode Zen pool.
- An embedded English/Chinese admin console with light/dark themes and client setup examples; CLI/server and Docker deployment from source.
- Versioned desktop archives for Windows x64 (`routenest_0.1.0-beta.1_windows_amd64.zip`), macOS Apple Silicon (`routenest_0.1.0-beta.1_darwin_arm64.tar.gz`) and Linux x64 (`routenest_0.1.0-beta.1_linux_amd64.tar.gz`). Each extracts to a folder of the same base name containing `routenest-desktop.exe` (Windows) or `routenest-desktop` (macOS/Linux), `LICENSE`, `THIRD_PARTY_NOTICES.md` and release instructions as `README.md`.
- Three matching SPDX SBOMs (`routenest_0.1.0-beta.1_windows_amd64.spdx.json`, `routenest_0.1.0-beta.1_darwin_arm64.spdx.json`, `routenest_0.1.0-beta.1_linux_amd64.spdx.json`), `SHA256SUMS` covering the six archive/SBOM files, and GitHub build/SBOM attestations. SBOMs inventory components; they are not vulnerability-free certification.

### Changed

- Updated the release toolchain to Go 1.26.11 and upgraded SAML, JOSE and Echo dependencies to current patched releases.

### Security

- One-time administrator setup with no default password, Argon2id password hashing, durable revocable sessions, server-side roles, session-bound CSRF, OIDC/SAML sign-in and a security audit journal.
- Single-use administrator recovery codes and explicitly enrolled, revocable desktop trust using Windows Credential Manager, macOS Keychain or Linux Secret Service. Unavailable protected storage falls back to password sign-in, not plaintext device credentials.
- Loopback listeners by default; hosted administration requires an exact canonical URL, trusted-proxy configuration and operator-provided HTTPS termination.
- Client-key changes are published after durable persistence. Unavailable or uncertain authentication storage fails closed; a validated, intentionally empty key list still permits anonymous model/inference access for compatibility.
- Account-only deletion preserves client keys and unrelated gateway configuration. Windows private paths use protected current-user/SYSTEM ACLs; replacement uses Go's directory-rooted rename, not a claimed write-through move.

### Preview limitations

- Windows artifacts are unsigned; macOS artifacts are not Developer ID-signed or notarized. There is no Intel macOS artifact, installer, separate CLI release asset or published container image in this preview.
- Desktop users need the platform WebView runtime: WebView2 on Windows, system WKWebView on macOS, and GTK 3 plus WebKitGTK 4.0 on a compatible glibc-based Linux desktop. WebKitGTK 4.1 alone does not satisfy the Linux artifact. Go and Node.js are not required to run these downloads; source builds use Go 1.26.11 or newer.
- Presets are configuration templates, not integration certification. API translation is incomplete, Responses features must be expressible as chat completions, non-text protocol translation is unsupported, and token counts are local estimates.
- Upstream availability, pricing, authentication, entitlements and terms remain the user's responsibility. Free-first/free-only classifications are not zero-cost guarantees; the Cline account integration is unofficial.
- Provider credentials, refresh tokens and generated client API keys remain plaintext in the data directory, protected by filesystem permissions rather than encryption at rest. Secure the directory and its backups.
- CI is configured for automated Go, race, frontend, localization, Docker and browser checks, native builds, windowless readiness self-checks, archive/SBOM/manifest validation and attestations before creating a reviewable prerelease draft. Desktop simulations and compilation do not certify packaged WebViews, real OS credential stores, clean-system installation, signed upgrades or every live provider. Consult tagged workflow results for pass/fail evidence; a draft is not publication or proof of immutability.
- The built-in update check follows stable releases only; download previews explicitly from the tagged release page.

### Upgrade guidance

- Stop all RouteNest processes and back up the entire selected data directory before replacing a development build or beta. Keep the account pool and `.routenest-accounts.json.initialized` together, and keep the administrator snapshot and journal together; protect referenced SSO secrets and save the recovery code separately.
- Verify the archive and SBOM against `SHA256SUMS`, and verify GitHub build attestations for those files and the manifest before extraction. For an immutable published release, also use `gh release verify v0.1.0-beta.1 --repo dhananjaym182/RouteNest` and `gh release verify-asset v0.1.0-beta.1 <downloaded-asset> --repo dhananjaym182/RouteNest` to verify the signed release/asset binding. These are separate from build provenance. Reuse the same data directory and OS user; first-run controls are never resets.
- Older binaries may reject newer administrator metadata. Roll back with the old executable and a complete compatible backup, not by deleting identity files or mixing snapshot/journal versions. OS-vault desktop trust is not part of a portable directory backup.

See [release notes](RELEASE_NOTES.md) for installation and verification commands and [data and upgrades](README.md#data-and-upgrades) for backup details.

[0.1.0-beta.1]: https://github.com/dhananjaym182/RouteNest/releases/tag/v0.1.0-beta.1
