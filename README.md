<div align="center">

# RouteNest

**One endpoint. Multiple providers. Your models.**

RouteNest is a self-hosted, Go-powered AI gateway for coding agents and AI applications. Route OpenAI- and Anthropic-compatible requests through configured provider APIs, local model runtimes, and supported account integrations from a single endpoint.

It provides provider presets, account management, model discovery, ordered fallback, request logging, and a built-in admin console.

[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/Platform-Windows%20%7C%20macOS%20%7C%20Linux-blue)](#build-from-source)
[![Provider definitions](https://img.shields.io/badge/Provider%20definitions-111-brightgreen)](#provider-coverage)

**中文:** [中文 README](README.zh-CN.md)

</div>

---

## What RouteNest provides

The proposed contract authority, terminology and unresolved behavior decisions are
recorded in [CONTRACTS.md](CONTRACTS.md) for review. Historical plans and verification
reports are dated evidence, not current test results.

- **Multi-account routing** - select and rotate configured accounts, with cooldowns and retryable-error handling.
- **Provider catalog** - 111 built-in Provider Definitions, including 109 selectable setup presets, plus support for custom OpenAI-compatible endpoints. Availability and protocol support vary by provider.
- **Multiple API formats** - OpenAI Chat Completions, supported Responses API functionality, and Anthropic Messages endpoints.
- **Model combos** - define ordered model lists with fallback on eligible upstream failures.
- **Model discovery** - synchronize supported model catalogs, configure custom model IDs, and select a default model.
- **Admin console** - manage accounts, connections, providers, models, combos, client API keys, proxies and request logs.
- **Egress proxy pools** - configure the shared Cline/default and native Zen pools plus custom-provider overrides; no generic profile-binding UI is claimed.
- **Request observability** - inspect request logs, latency, token usage and available provider statistics.
- **Flexible deployment** - run the Go binary, use the desktop application, or deploy with Docker.

Provider availability, authentication requirements, free allowances and supported features depend on the upstream service. A preset is a configuration template, not a tested integration, and its inclusion does not guarantee that every model has been tested or is available to every account.

## Before you deploy

> **Security:** RouteNest has no universal default password and never permits anonymous administrative operations. A fresh install starts in one-time bootstrap mode and requires a permanent local administrator password before normal administration. The static admin shell and narrow status, bootstrap, login and SSO protocol-entry routes are public but do not grant administrative access.

- **Bootstrap:** the CLI prints a generated one-time secret only to its controlling terminal. A first start without a controlling terminal must provide `ROUTENEST_ADMIN_BOOTSTRAP_PASSWORD`; the retained initialized authority ignores it. The desktop app uses a one-shot native dialog.
- **Passwords:** permanent administrator passwords require at least 8 characters, with a maximum of 1024 UTF-8 bytes. Password changes revoke all sessions for that administrator.
- **Separate controls:** Client API keys protect model and inference routes, including the key-wrapped `/v1/*` routes, once at least one key is loaded; `/health` and `/v1/health` are always public. An empty key list leaves the key-wrapped routes open for compatibility. Durable administrator sessions and server-side RBAC protect administrative operations; the listed bootstrap/login/SSO entry routes remain public and separately validated. Admin sessions store only hashed tokens, survive restart, can be revoked, and require session-bound CSRF tokens for authenticated mutations.
- **Account-only deletion:** **Delete All Accounts** removes Cline accounts and their account-owned state only. Client API keys, models, the default model, listener settings, removal reservations and legacy migration sentinels are preserved. The operation reports success only after durable persistence.
- **Hosted administration:** the binary and desktop app bind to `127.0.0.1` by default. A production non-loopback listener is rejected unless an exact public URL, trusted-proxy mode and trusted proxy CIDRs are configured. RouteNest accepts an HTTP canonical origin when HSTS is off, so HTTPS termination at the trusted proxy remains a required deployment practice rather than a universal server invariant.
- **Client-key limitation:** corrupt, unreadable, partially restored, or uncertain missing account-pool state fails closed on key-wrapped routes with an authentication-storage error. A confirmed fresh installation must set `ROUTENEST_INITIALIZE_EMPTY_ACCOUNT_STORE=true` for one startup; remove it after initialization. A verified intentionally empty key list remains anonymously accessible for compatibility. Console-generated client keys are still clock-derived, and ordinary key writes still use best-effort persistence. Do not treat them as an internet-grade security boundary without additional hardening; verify keys after restart and keep the gateway behind trusted network controls.
- **Sensitive data:** account refresh tokens, Provider Credentials and generated client API keys are stored in plaintext in the data directory. Protect that directory, and never commit or share it.
- **Docker:** the sample compose file binds the backend port to host loopback and requires explicit bootstrap/public-URL/proxy settings. Do not expose the backend port directly to the internet.

See [Administrator security and SSO](docs/admin-security.md) for RBAC, OIDC, SAML, recovery and reverse-proxy configuration.

## Quick start

### Desktop app (prebuilt)

Download the desktop build for your platform from [Releases](https://github.com/dhananjaym182/RouteNest/releases) and run it.

| Platform | What you need to run a release |
| --- | --- |
| Windows x64 | Microsoft Edge WebView2 Runtime. Windows 11 includes it; updated Windows 10 usually has it. If the app reports a missing WebView2 runtime, install the Evergreen Runtime from Microsoft. |
| macOS Apple Silicon | A supported macOS version (WKWebView is built in). No Xcode tools are required to run a release. The official release is arm64-only; Intel Macs can build from source. The build is not notarized, so Gatekeeper may require right-click **Open** or approval under **System Settings → Privacy & Security**. |
| Linux x64 | GTK 3 and WebKitGTK 4.0 runtime libraries, matching the official build. Development headers are not needed. |

Windows SmartScreen may warn because the build is not code-signed. Choose **More info → Run anyway** only if you obtained the file from the official release page.

### Run the binary

Requires Go 1.26.10 or newer to build:

```bash
go build -o routenest .
./routenest                # 127.0.0.1:3457
./routenest -port 8080     # custom port
./routenest -host 0.0.0.0  # requires the hosted-admin settings below
```

Open <http://127.0.0.1:3457/admin/> for the admin console. The listen address can also be set with `CLINE_PROXY_HOST`.

### Docker

```bash
docker compose up -d
docker compose logs -f
docker compose down
```

The container listens on `0.0.0.0:3457`, while the sample compose file publishes it only on host loopback for a local TLS reverse proxy. Set the required administrator variables in `.env` first; see [the Docker example](docs/admin-security.md#docker-and-reverse-proxy). State lives in `/app/data`, mounted to `./data`, so recreating the container preserves identity and gateway data when the volume is retained.

### First run

1. For a confirmed new data directory, set `ROUTENEST_INITIALIZE_EMPTY_ACCOUNT_STORE=true` for the first startup only, then remove it. Never use this variable to repair or reset an established installation.
2. Read the one-time bootstrap secret from the local terminal/native dialog, open `/admin/`, and set a permanent password.
3. Add a Cline account, enable native OpenCode Zen, or configure a provider API key.
4. Generate a client API key. Until at least one key exists, key-wrapped model and inference routes accept anonymous requests.
5. Pick a default model. If left unset, RouteNest prefers an active Cline model classified as free; after catalog initialization, it can fall back to the first active enabled model of any cost.

## Client setup

Both protocol families run on the same port (default `3457`). Once at least one client API key is loaded, keys are accepted from `Authorization: Bearer <key>` or `x-api-key: <key>`. With an empty key list, model and inference endpoints remain open for compatibility; `/health` and `/v1/health` are always public.

```bash
export ROUTENEST_KEY="<key generated in the console>"
curl http://127.0.0.1:3457/v1/models -H "Authorization: Bearer $ROUTENEST_KEY"
```

Use a model ID returned by `/v1/models`, or the name of an enabled model combo.

### OpenAI-compatible

```bash
curl http://127.0.0.1:3457/v1/chat/completions \
  -H "Authorization: Bearer $ROUTENEST_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"<model-or-combo>","messages":[{"role":"user","content":"hi"}]}'
```

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:3457/v1",
    api_key="<key generated in the console>",
)
```

### Anthropic-compatible

```bash
curl http://127.0.0.1:3457/v1/messages \
  -H "x-api-key: $ROUTENEST_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{"model":"<model-or-combo>","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}'
```

Point Claude Code at RouteNest:

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:3457
export ANTHROPIC_AUTH_TOKEN="$ROUTENEST_KEY"
```

## API compatibility

RouteNest provides OpenAI- and Anthropic-compatible endpoints for supported request patterns. Compatibility is implementation-level; it does not guarantee every field, provider extension or model feature.

| Endpoint | Format | Notes |
| --- | --- | --- |
| `POST /v1/chat/completions` | OpenAI Chat Completions | Streaming and non-streaming; tools, reasoning fields and usage metadata where the upstream supplies them |
| `POST /v1/responses` | OpenAI Responses | Converted to an upstream chat-completions call; supports text, tools, reasoning content, streaming events and usage |
| `POST /v1/messages` | Anthropic Messages | Streaming and non-streaming; tool use/results, thinking content and usage |
| `POST /v1/messages/count_tokens` | Anthropic token count | Local estimate, not an upstream tokenizer |
| `GET /v1/models` | OpenAI model list | Enabled models and enabled combos |
| `GET /v1/health` | Health probe | Version and active account count |

Supported and verified behavior includes SSE streaming for text, tool calls and reasoning content; conversion of tool definitions and tool results between OpenAI and Anthropic shapes; usage metadata when the upstream returns it; Responses API event sequences; and failure recording when a stream is truncated or the client disconnects.

Known limitations:

- Protocol translation currently targets text, tool calls and thinking/reasoning content. Non-text content blocks (for example images) are not translated between protocols.
- `response_format` and similar OpenAI fields are forwarded to OpenAI-compatible upstreams, but RouteNest does not normalize structured-output behavior across providers.
- The Responses API is implemented by converting to chat completions; behavior that cannot be represented in that shape is not supported.
- `count_tokens` returns a conservative local estimate, not the provider's exact token count.
- Upstream models, authentication methods and provider-specific features can change without notice.

## Accounts and upstreams

### Cline accounts (unofficial)

RouteNest can add a Cline account through Cline's device-authorization flow, by importing a refresh token, or by batch import, and can rotate a pool of accounts. This integration is **unofficial and not supported or endorsed by Cline**. It uses Cline account credentials and Cline's API from outside the Cline client.

Cline's documentation states that free-model usage is not supported through the Cline API and is limited to the Cline IDE extension and CLI. RouteNest reads Cline's recommended-models endpoint, which groups free, Cline Pass and recommended models, but free/Cline Pass access through RouteNest is not an officially supported path and may stop working or be restricted at any time. Review [Cline's free-model documentation](https://docs.cline.bot/getting-started/free-models) and [Terms of Service](https://cline.bot/tos) before using this integration.

### OpenCode Zen

A dedicated integration for OpenCode Zen free models. Paid Zen models are rejected rather than forwarded. Zen has its own egress proxy pool and cooldown handling.

### Qoder

Device-login connections are supported alongside Cline accounts.

### Provider APIs and local runtimes

Add any OpenAI-compatible endpoint as a custom provider with its own base URL, key, model IDs, limits and egress proxy. Local runtimes (Ollama, vLLM, LM Studio, llama.cpp and similar) can be used without a key; loopback and private-network targets bypass egress proxies.

## Provider coverage

The built-in registry contains **111 Provider Definitions**: 21 RouteNest-maintained definitions plus 90 ExtremeRouter-derived imports. Two maintained definitions are fixed runtime providers (Cline and OpenCode Zen), so the Connect Provider directory exposes **109 selectable presets**.

| Source | Definitions | Selectable setup presets | Notes |
| --- | ---: | ---: | --- |
| RouteNest-maintained | 21 | 19 | Includes Cline and OpenCode Zen as fixed runtime providers |
| ExtremeRouter-derived imports | 90 | 90 | Includes free, free-tier, API-key-with-free-tier and local-runtime entries |
| **Total** | **111** | **109** | **34 selectable presets carry a `freeTier` provider-level hint** |

`freeTier` and `kind` are display/grouping hints from the registry, not per-model access entitlements. Presets include seed model IDs and, where defined, a catalog fetcher. A registry entry does not establish that the service is currently reachable, that an account is entitled to a model, or that provider-specific features work.

Authentication depends on the provider:

- API keys for most hosted providers.
- Browser or device login for some presets (for example GitHub Copilot, Kilo Code, Kimi for Coding, Qwen Chat, CodeBuddy and WorkBuddy).
- No authentication for local runtimes.
- Cline account credentials and native OpenCode Zen configuration for those integrations.

Only a subset of presets and request patterns is exercised by the repository's tests and real deployments. Treat the catalog as a starting point and verify each provider with a small request before relying on it.

## Model discovery

- RouteNest fetches Cline's recommended-model list at startup, re-syncs hourly, and can sync on demand from the console.
- Presets with a catalog fetcher can refresh their own model or pricing lists.
- Custom model IDs can be added for endpoints that are not in any catalog.
- When no default is configured, RouteNest first prefers an active, enabled, non-delisted Cline model classified as free. After catalog initialization, if none exists, it can select the first active, enabled, non-delisted catalog model of any cost.
- An optional free-only mode rejects models that are not classified as free. **This is not a zero-cost guarantee:**
  - Filtering relies on RouteNest's current model classification, which may be incomplete, stale or inaccurate.
  - A model or provider being labelled free does not guarantee that the underlying account, route or request is free of charge.
  - Before relying on this setting, verify your provider's terms of service, account entitlements, pricing and usage limits. Until route/account-level access has been independently verified, do not treat Free-only mode as a guarantee of zero cost.

## Routing and combos

1. A client sends a request with a model name or combo name to a `/v1/*` endpoint.
2. RouteNest resolves the model to a Cline account pool, an OpenCode Zen upstream, or a provider preset/custom provider.
3. The pool selects an account using `round_robin`, `fill` or `random`. Accounts that hit `402`/`429` cool down per model and recover automatically.
4. On an eligible upstream failure, RouteNest tries another account or provider; for a combo, it moves to the next entry in the configured order.
5. Responses are relayed as SSE in the inbound protocol, so OpenAI clients receive OpenAI chunks and Anthropic clients receive Anthropic events.

Combo order is authoritative: unhealthy entries are skipped, not reordered. Emergency fallback to the global free chain is opt-in and labeled in the console. Optional session affinity and model aliases are configurable.

## Admin console

The console at `/admin/` requires administrator bootstrap/login and manages the gateway:

- Dashboard, request volume and provider health.
- Account pool, connections, rotation strategy and import/export.
- Provider presets, custom providers, keys, base URLs, cooldowns and quotas.
- Model catalog, custom models, default model and model tests.
- Model combos and ordered fallback.
- Request logs with latency, tokens, TPS, upstream and model.
- Client API keys, administrator users/roles/sessions, OIDC/SAML providers, listen security, password, egress proxies and runtime tuning.

The UI is bilingual (English/Chinese), follows the browser language, and can be switched from the sidebar. On desktop widths the sidebar collapses to a rail; `Ctrl+B` toggles it.

## Egress proxies

For upstreams that are region-restricted or rate-limited:

- **Cline egress pool** - used for chat, login/token refresh and model sync.
- **OpenCode Zen egress pool** - separate pool with cooldown for rate-limited exits.

Priority is application proxy pool → environment proxy (`HTTPS_PROXY`) → direct. Loopback and private-network targets always connect directly.

## Data and upgrades

By default, RouteNest stores state in the operating system's per-user config
directory:

- Linux: `$XDG_CONFIG_HOME/routenest`, normally `~/.config/routenest`
- macOS: `~/Library/Application Support/routenest`
- Windows: `%AppData%\routenest`

Set `ROUTENEST_DATA_DIR` to use an explicit directory, such as a mounted Docker
volume. The legacy `CLINE2API_DATA_DIR` variable remains a lower-priority
compatibility override.

| File | Purpose |
| --- | --- |
| `.routenest-accounts.json` | Cline account pool, client API keys, logical-model settings, saved listener host, and legacy administrator migration sentinels |
| `.routenest-accounts.json.initialized` | Initialization witness for the account/client-authentication store; back up and restore it with the account-pool file |
| `admin-auth/.routenest-admin-auth.json` | Durable administrator users, role/provider metadata and hashed sessions |
| `admin-auth/.routenest-admin-auth.journal` | Fsynced, hash-chained administrator security and audit journal |
| `admin-auth/sso-secrets/` | Approved root for owner-only OIDC/SAML secret files |
| `.routenest-config.json` | Proxy configuration, aliases, policies and model combos |
| `.routenest-connections.json` | Provider Connection records and their access/refresh tokens (Qoder and other non-Cline logins) |
| `.routenest-credentials.json` | Inert legacy single Cline refresh-token helper retained for file-format/migration compatibility; current account login and routing do not read or write it |
| `.routenest-providers.json` | Custom providers |
| `.routenest-providers-cooldowns.json` | Provider cooldown state |
| `.routenest-providers-sync.json` | Provider catalog sync state |
| `.routenest-proxy.json` | Cline egress proxy pool |
| `.routenest-zen.json` | OpenCode Zen configuration and egress pool |
| `.routenest-request-logs.json` | Request logs |
| `capture-logs/` | Interactive OAuth capture output; may contain authorization traffic |
| `override.md` | Optional system-prompt override, read from the working directory |

On first use, RouteNest copies missing current `.routenest-*` files and legacy
`.cline-*` files from the executable directory, working directory,
`~/.routenest/`, and `~/.cline2api/` into the selected data directory. Existing
destination files always win, migration is idempotent, and source files are
preserved as recovery backups.

If the pool and witness are both absent, RouteNest will not infer that an existing
or partially restored data directory is a fresh installation. Restore a valid pool
file and restart. A valid restored pool without its witness is accepted and the
witness is recreated only after the pool has been validated.

On Unix-like systems the data and capture directories are restricted to `0700`
and known state files to `0600`. Windows uses protected owner/SYSTEM ACLs for
private RouteNest paths. RouteNest fails startup with an actionable error if it
cannot establish safe permissions; it no longer falls back to writing secrets
beside the executable.

The Windows durable writer uses `MoveFileEx` write-through replacement followed by
a writable directory-handle flush. The correction is cross-compiled in CI/local
verification, but real Windows runtime execution is still required before claiming
full cross-platform C1 approval.

> The data files contain plaintext refresh tokens, Provider Credentials and generated client API keys. Back them up securely and never publish them or commit them to Git.

## Build from source

The CLI binary requires Go 1.26.10 or newer. Building the desktop app also requires the platform toolchain listed below.

### Binary

```bash
go build -o routenest .
```

### Desktop app

Wails uses each platform's native WebView and must be built on the target OS:

```bash
# Windows: Go + C toolchain; WebView2 Runtime to run
./desktop/build.sh

# macOS: Xcode Command Line Tools
xcode-select --install
./desktop/build.sh

# Linux: GTK 3 and WebKitGTK development packages, e.g.
# Debian/Ubuntu (use 4.0 or 4.1 to match your distribution)
sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev
# Fedora: sudo dnf install gtk3-devel webkit2gtk4.1-devel
# Arch:   sudo pacman -S gtk3 webkit2gtk
./desktop/build.sh
```

Development packages are only needed to compile. Release users need the corresponding runtime libraries, not the headers.

## Project layout

```
main.go, desktop_main.go   thin CLI and desktop entry points
internal/app/              service composition shared by both entry points
internal/proxycore/        HTTP routes, protocol bridging, Responses and SSE
internal/routing/          model discovery, policy and upstream invocation
internal/providers/, zen/  provider registry and OpenCode Zen integration
internal/egress/           shared HTTP transport, proxy and retry policy
internal/combos/           named model combinations and fallback execution
internal/compaction/       context management and compaction
internal/admin/            admin API plus embedded UI in internal/admin/ui/
internal/accounts/, connections/, requestlog/, store/   persistent state
desktop/                   Wails shell and build scripts
CONTRACTS.md, DESIGN.md     Phase 1 contract proposal and active UI design rules
CONTEXT.md                  domain glossary
docs/, scripts/             tracked references, local historical notes and operational scripts
```

The Go module path is `routenest`. The repository root keeps the CLI and desktop entry
points plus the local administrator recovery command; application composition and all
service implementation code live under `internal/`.

## Attribution and license

RouteNest is an independent fork of [Cline2API](https://github.com/luawei1/cline2api), originally created by luawei1, and is distributed under the MIT License. The upstream copyright notice is retained in [LICENSE](LICENSE).

The 90 imported Provider Definitions are derived from the ExtremeRouter provider registry, which is MIT-licensed. The corresponding copyright and license notice is preserved in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

RouteNest is not affiliated with or endorsed by Cline, OpenCode, Qoder, or any provider listed in the catalog. Use it only with accounts and credentials you are authorized to use, and comply with each provider's terms of service. Provider names and trademarks belong to their owners.

## License

[MIT](LICENSE)
