<div align="center">

# RouteNest

**One endpoint. Multiple providers. Your models.**

RouteNest is a self-hosted, Go-powered AI gateway for coding agents and AI applications. Route OpenAI- and Anthropic-compatible requests through configured provider APIs, local model runtimes, and supported account integrations from a single endpoint.

It provides provider presets, account management, model discovery, ordered fallback, request logging, and a built-in admin console.

[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/Platform-Windows%20%7C%20macOS%20%7C%20Linux-blue)](#build-from-source)
[![Provider presets](https://img.shields.io/badge/Provider%20presets-110-brightgreen)](#provider-coverage)

**中文:** [中文 README](README.zh-CN.md)

</div>

---

## What RouteNest provides

- **Multi-account routing** — select and rotate configured accounts, with cooldowns and retryable-error handling.
- **Provider catalog** — 110 provider preset definitions, plus support for custom OpenAI-compatible endpoints. Availability and protocol support vary by provider.
- **Multiple API formats** — OpenAI Chat Completions, supported Responses API functionality, and Anthropic Messages endpoints.
- **Model combos** — define ordered model lists with fallback on eligible upstream failures.
- **Model discovery** — synchronize supported model catalogs, configure custom model IDs, and select a default model.
- **Admin console** — manage accounts, connections, providers, models, combos, API keys, proxies and request logs.
- **Egress proxy pools** — configure outbound proxies for supported upstream connections.
- **Request observability** — inspect request logs, latency, token usage and available provider statistics.
- **Flexible deployment** — run the Go binary, use the desktop application, or deploy with Docker.

Provider availability, authentication requirements, free allowances and supported features depend on the upstream service. A preset is a configuration template, not a tested integration, and its inclusion does not guarantee that every model has been tested or is available to every account.

## Before you deploy

> **Security:** RouteNest has no default admin password and no built-in TLS. Anyone who can reach `/admin/` on a password-less instance can add or export accounts, read stored credentials, change routing, and create API keys. Set an admin password and restrict network access before exposing RouteNest to a LAN or the internet.

- **Listen address:** the binary and desktop app bind to `127.0.0.1` by default. The Docker image binds `0.0.0.0` inside the container, so a published port is reachable from outside the container.
- **Two separate controls:** API keys protect the `/v1/*` proxy endpoints only. They do **not** protect `/admin/*`. Admin access is controlled by the admin password and its session cookie (24-hour expiry, held in memory, reset when the process restarts). Setting an API key alone does not secure the console.
- **No TLS:** RouteNest serves plain HTTP. Put it behind a TLS-terminating reverse proxy if traffic crosses an untrusted network.
- **Sensitive data:** account refresh tokens, provider keys and generated API keys are stored in plaintext in the data directory. Protect that directory, and never commit or share it.
- **Docker:** do not publish port `3457` publicly. Restrict it with firewall rules or bind it to a local address.

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

Requires Go 1.25 or newer to build:

```bash
go build -o routenest .
./routenest                # 127.0.0.1:3457
./routenest -port 8080     # custom port
./routenest -host 0.0.0.0  # all interfaces; see the security note above
```

Open <http://127.0.0.1:3457/admin/> for the admin console. The listen address can also be set with `CLINE_PROXY_HOST`.

### Docker

```bash
docker compose up -d
docker compose logs -f
docker compose down
```

The container listens on `0.0.0.0:3457`. State lives in `/app/data`, mounted to `./data` by the compose file, so recreating the container keeps your data as long as `./data` is preserved. The compose file also mounts `./override.md` read-only; create that file before `docker compose up` if you use a prompt override.

### First run

1. Open `/admin/` and set an admin password.
2. Add a Cline account, an OpenCode Zen connection, or a provider API key.
3. Generate an API key for your clients.
4. Pick a default model, or leave it unset to use the first available free model.

## Client setup

Both protocol families run on the same port (default `3457`). API keys are accepted from `Authorization: Bearer <key>` or `x-api-key: <key>`.

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

The shipped registry contains **110 provider preset definitions**:

| Source | Count | Notes |
| --- | --- | --- |
| Built-in presets | 17 | OpenRouter, Groq, Cerebras, Google AI Studio, Mistral, Together AI, DeepSeek, OpenAI, vLLM, Ollama, Kilo Code, GitHub Copilot, Qwen Chat, Kimi for Coding, CodeBuddy CN/Intl, WorkBuddy |
| Imported presets | 93 | Derived from the ExtremeRouter provider registry; includes free, free-tier, API-key-with-free-tier and local-runtime entries |
| **Total** | **110** | **35 marked `freeTier` as a provider-level hint** |

`freeTier` and `kind` are display/grouping hints from the registry, not per-model access entitlements. Presets include seed model IDs and, where defined, a catalog fetcher. A registry entry does not establish that the service is currently reachable, that an account is entitled to a model, or that provider-specific features work.

Authentication depends on the provider:

- API keys for most hosted providers.
- Browser or device login for some presets (for example GitHub Copilot, Kilo Code, Kimi for Coding, Qwen Chat, CodeBuddy and WorkBuddy).
- No authentication for local runtimes.
- Cline account credentials and OpenCode Zen connections for those integrations.

Only a subset of presets and request patterns is exercised by the repository's tests and real deployments. Treat the catalog as a starting point and verify each provider with a small request before relying on it.

## Model discovery

- RouteNest fetches Cline's recommended-model list at startup, re-syncs hourly, and can sync on demand from the console.
- Presets with a catalog fetcher can refresh their own model or pricing lists.
- Custom model IDs can be added for endpoints that are not in any catalog.
- The default model falls back to the first available free model when none is configured.
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

The console at `/admin/` manages the gateway:

- Dashboard, request volume and provider health.
- Account pool, connections, rotation strategy and import/export.
- Provider presets, custom providers, keys, base URLs, cooldowns and quotas.
- Model catalog, custom models, default model and model tests.
- Model combos and ordered fallback.
- Request logs with latency, tokens, TPS, upstream and model.
- API keys, listen address, admin password, egress proxies and runtime tuning.

The UI is bilingual (English/Chinese), follows the browser language, and can be switched from the sidebar. On desktop widths the sidebar collapses to a rail; `Ctrl+B` toggles it.

## Egress proxies

For upstreams that are region-restricted or rate-limited:

- **Cline egress pool** — used for chat, login/token refresh and model sync.
- **OpenCode Zen egress pool** — separate pool with cooldown for rate-limited exits.

Priority is application proxy pool → environment proxy (`HTTPS_PROXY`) → direct. Loopback and private-network targets always connect directly.

## Data and upgrades

State is looked up in the executable directory, then the working directory, then `~/.routenest/`. Set `ROUTENEST_DATA_DIR` to fix all data files to one directory (the legacy `CLINE2API_DATA_DIR` variable is still honored when `ROUTENEST_DATA_DIR` is unset).

| File | Purpose |
| --- | --- |
| `.routenest-accounts.json` | Account pool, API keys, model settings, admin password hash |
| `.routenest-config.json` | Proxy configuration, aliases, policies and model combos |
| `.routenest-connections.json` | Non-Cline connection metadata (Zen, Qoder and others) |
| `.routenest-credentials.json` | Stored connection credentials |
| `.routenest-providers.json` | Custom providers |
| `.routenest-providers-cooldowns.json` | Provider cooldown state |
| `.routenest-providers-sync.json` | Provider catalog sync state |
| `.routenest-proxy.json` | Cline egress proxy pool |
| `.routenest-zen.json` | OpenCode Zen configuration and egress pool |
| `.routenest-request-logs.json` | Request logs |
| `override.md` | Optional system-prompt override, read from the working directory |

When the data directory is resolved to a new location, legacy `.cline-*` data files found next to the executable or in the working directory or in `~/.cline2api/` are copied into it under the new `.routenest-*` names on first use if the destination files do not already exist. This keeps existing deployments upgrading in place; the legacy `CLINE2API_DATA_DIR` environment variable is also still honored when `ROUTENEST_DATA_DIR` is unset.

> The data files contain plaintext refresh tokens, provider keys and generated API keys. Back them up securely and never publish them or commit them to Git.

## Build from source

The CLI binary requires Go 1.25 or newer. Building the desktop app also requires the platform toolchain listed below.

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
main.go, desktop_main.go   CLI and desktop entry points (package main, root)
proxy.go, responses.go     HTTP routes, protocol bridging, SSE
combos.go, providers.go    model combos, presets and provider dispatch
admin*.go                  admin REST API
adminui/                   embedded admin console
internal/                  config, protocol adapters, upstream clients, storage
desktop/                   Wails shell and build scripts
docs/                      design documents and provider-registry audits
scripts/                   update.sh, docker-smoke.sh
Dockerfile, docker-compose.yml
```

The repository's Go source at the root compiles as a single `package main`; all reusable logic lives in `internal/`.

## Attribution and license

RouteNest is an independent fork of [Cline2API](https://github.com/luawei1/cline2api), originally created by luawei1, and is distributed under the MIT License. The upstream copyright notice is retained in [LICENSE](LICENSE).

The 93 imported provider preset definitions are derived from the ExtremeRouter provider registry, which is MIT-licensed. The corresponding copyright and license notice is preserved in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

RouteNest is not affiliated with or endorsed by Cline, OpenCode, Qoder, or any provider listed in the catalog. Use it only with accounts and credentials you are authorized to use, and comply with each provider's terms of service. Provider names and trademarks belong to their owners.

## License

[MIT](LICENSE)
