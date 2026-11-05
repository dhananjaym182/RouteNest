# Contributing

The repository root contains only the CLI and desktop entry points. Service
composition lives in `internal/app`; implementation lives under `internal/`.
The Go module path is `routenest`, so repository-local imports use
`routenest/internal/...`.
Keep the dependency direction:

```
platform -> domain -> config -> transport / httpx / i18n -> store/*
  -> providerreg / upstream/* / protocol/* / deviceflow
  -> modelcatalog -> dispatch -> routing / providers / zen / egress
  -> compaction / combos -> proxycore / admin -> app -> entry points
```

Rule of thumb: stores persist state and never call upstream APIs; device flows
return results and the caller persists them; converters never write HTTP
responses; dispatch owns retries and the SSE writer.

## Repository map

Each package owns one concern. HTTP code lives in `internal/admin` (admin) and
`internal/proxycore` (public `/v1`); owner packages expose service APIs without
HTTP types.

| Path | Owns |
| --- | --- |
| `main.go`, `desktop_main.go` | Thin CLI and desktop entry points. CLI modes include `--login`, `--add-account`, `--capture`, `--list` and `--start`. |
| `internal/app` | Composition root. `app.Configure()` installs every cross-package seam; wiring-dependent tests must call it first. |
| `internal/config` | Live proxy configuration (`config.Live()` under `config.Mu`), overrides, snapshot and publish. |
| `internal/platform` | Data directory resolution and legacy-file migration. |
| `internal/buildinfo` | Version and update-status information. |
| `internal/proxycore` | Public `/v1` routes, OpenAI/Anthropic/Responses handlers, SSE streams, capture mode. |
| `internal/admin` | Admin route registration and HTTP adapters (`*_api.go`). Embedded UI under `internal/admin/ui/`. |
| `internal/adminapi`, `internal/i18n` | Reusable admin primitives and the admin message catalogue. |
| `internal/httpapi`, `internal/httpx` | Shared HTTP transport helpers (responses, CORS, JSON writing). |
| `internal/routing` | Model policy, the `ComboCatalog` port, and cross-upstream fallback. |
| `internal/dispatch` | Retry classification and cooldown policy helpers. |
| `internal/combos` | Named model combinations; implements `routing.ComboCatalog`. |
| `internal/providerreg` | Source definition catalogue: `manual/*.json` and `imported.json`. |
| `internal/providers` | Custom provider owner service: presets, model import, cooldowns, account health. |
| `internal/deviceflow` | Device-code login strategies for providers. |
| `internal/models` | Live model catalogue, guarded by `accounts.PoolMu`. |
| `internal/modelcatalog` | Model discovery types and free/paid classification. |
| `internal/zen` | OpenCode Zen integration: catalogue sync, responses, sessions, model-gone classifier. |
| `internal/compaction` | Context management and Anthropic conversion options. |
| `internal/protocol/...` | Wire-protocol converters: `anthropic`, `openaichat`, `responses`. |
| `internal/upstream/...` | Upstream clients: `cline`, `openaicompat`, `qoder`, `zen`. |
| `internal/egress`, `internal/transport` | Shared outbound HTTP client, proxy pools, transport policy. |
| `internal/accounts` | Cline account pool and the CLI login entry (`AddAccountFromDeviceAuth`). |
| `internal/connections` | Provider connection owner service (no HTTP). |
| `internal/requestlog` | Request-log owner service. |
| `internal/store/...` | JSON-file persistence, one package per domain. |
| `internal/domain` | Shared domain types. |
| `internal/integration` | Cross-package tests, including the route inventory golden. |

Put new behavior in its owning package rather than in an entry point. Cross-package
dependencies are wired in `internal/app`. Do not reintroduce compatibility wrappers
in the repository root.

### Which screen owns what

| Admin page (hash) | Owns | Does not own |
| --- | --- | --- |
| Upstreams (`#upstreams`) | opencode Zen and Cline egress, request headers, proxy, health | provider instances, global model policy |
| Providers (`#providers`) | preset selection, provider instance config, credential binding, per-source model import and recheck, per-source health | global model enable/disable and aliases |
| Accounts (`#accounts`) | Cline account pool and provider connection pool (separate subtabs) | provider instance routing rules |
| Routing and models (`#routing`) | model catalogue, source badges, global enable/disable, sync status, aliases, exclusions, fallback | provider credentials and instance CRUD |
| Combos (`#combos`) | named, ordered route definitions and their tests | physical model-source configuration |

Hash routes and DOM tab IDs are public. Do not rename them.

## Adding an admin endpoint

1. Put the owner logic in the owning package as a plain Go function or service
   method with no `net/http` types.
2. Add the HTTP adapter in `internal/admin/<domain>_api.go`. It parses the request,
   calls the owner, and writes the envelope through `internal/httpx`.
3. Register the route in `RegisterRoutes` in `internal/admin/admin.go` with the
   domain's auth wrapper.
4. Cover method rejection, auth, invalid JSON, not found, upstream error, and
   redaction of any credential fields in the response.
5. Run `go test ./internal/integration -run TestRouteInventoryStatic -update-route-golden`,
   then review the golden diff. Only intended routes may change.

## Adding admin UI assets

The admin page is assembled at startup from explicit manifests in
`internal/admin/ui_manifest.go`. Filename globs are not used for ordering.

- **Page or dialog markup:** add a file under `ui/html/pages/` (a tab panel) or
  `ui/html/dialogs/` (an overlay, drawer or modal), then list it in
  `adminUIBodyParts` in the order it appears in the page.
- **Script:** add a file under `ui/js/` with a unique numeric prefix, then list it in
  `adminUIJSAssets`. Scripts share one global scope, so a function name may be
  declared only once.
- **Stylesheet:** add a layer under `ui/css/` and list it in `adminUICSSLayers`.
  Layer order is part of the cascade.

The tests fail if a listed file is missing, a file is listed twice, or an embedded
file is not listed. They also fail if a duplicate top-level function exists, or if an
inline handler (`onclick="fn(...)"`) calls a function that no script declares.
Run `node tools/ui_inventory.js <baseUrl> <out.json>` with `PLAYWRIGHT_CORE` set to a
`playwright-core` install and `CHROME_PATH` set to a Chromium binary before and after a
UI change. Compare the output to confirm that no control, element ID, or `/admin/api`
call disappeared.

## State and secret handling

- Resolve persisted state through `internal/platform`; never default to the
  executable or working directory.
- `ROUTENEST_DATA_DIR` is the explicit deployment override. Without it,
  RouteNest uses `os.UserConfigDir()/routenest`.
- Create state directories with `0700` and credential/state files with `0600`
  on Unix-like systems.
- Migration copies and verifies old files; it must never overwrite the selected
  destination or delete recovery sources automatically.
- Capture logs can contain authorization headers and bodies. Keep them under
  the selected data directory and never print captured secret values in error
  logs.

## Embedded presets vs runtime providers

There are two different provider paths:

- **Embedded presets** form one catalogue, `internal/providerreg`. It merges
  `manual/*.json` (hand-maintained) and `imported.json` (generated) at build time
  and is compiled into the binary with `//go:embed`. Changing a preset requires a
  rebuild.
- **Runtime custom providers** are stored in `.routenest-providers.json` under the
  data directory and take effect without a rebuild (see the admin UI).

A provider-level `kind`/`freeTier` flag is metadata used by the admin UI. It is
not a per-model access entitlement.

### Adding an embedded preset

The built-in catalogue is `internal/providerreg`, built from two sources with
distinct provenance:

- `internal/providerreg/manual/<provider-id>.json`: hand-maintained presets
  (provenance `manual`). The filename must equal the preset `id`.
- `internal/providerreg/imported.json`: generated presets (provenance
  `imported`). Do not edit it by hand.

Steps:

1. Add `manual/<provider-id>.json` with the fields in `Preset`
   (`internal/providerreg/registry.go`): `id`, `name`, `baseURL`, `kind`,
   `freeTier`, plus `protocol`, `authFlow`, `modelsFetcher`, `headers` and
   `notes` where they apply.
2. For a login (device-flow) provider, set `authFlow` to an ID of a spec in
   `internal/deviceflow`. `loginFlow` is derived from `authFlow`; do not store it.
3. Imported presets come from the ExtremeRouter registry. Regenerate with
   `node tools/gen-provider-presets.mjs <registry-dir>`. The output is
   deterministic. Manual corrections to generated entries go in
   `tools/provider-presets.overrides.json`, with a `reason`.
4. Run `go test ./internal/providerreg/ ./internal/providers/` and
   `node --test tools/gen-provider-presets.test.mjs`. Duplicate IDs, duplicate
   base URLs with conflicting protocols, unknown auth flows and unknown fetcher
   types are rejected.
5. Rebuild. Embedded preset edits do not apply to an existing binary.

A conventional OpenAI-compatible provider usually needs only a preset because
the shared `internal/upstream/openaicompat` client handles its protocol. A
provider with a private authentication flow, nonstandard request shape, or
special stream format needs a Go adapter as well — do not claim that every
provider can be added with data alone.

### Adding a runtime provider

Use the admin UI or `POST /admin/api/providers/save`. Runtime providers are
validated at save time and are not embedded into the binary.

## Adding a device-auth flow

1. Add a flow spec in `internal/deviceflow/spec.go` (provider, mode, endpoints,
   JSON paths, polling interval, expiry).
2. Reuse an existing strategy (`rfc8628`, `statepoll`, `qoder`, `kilo`) where
   possible; add a new strategy only when the exchange semantics differ.
3. Return a `deviceflow.Result`. `deviceflow` must not import the connections
   store. The calling service persists the connection and refreshes tokens.
4. Cover initiate, pending, approved, denied, expired, and refresh paths with
   `httptest` upstreams in `internal/deviceflow/deviceflow_test.go`.

## Adding a model capability or entitlement

Keep these concepts separate:

- `domain.Model` catalog fields (`ID`, `Provider`, `Cost`, `Context`, `Output`,
  `TierSignal`, …).
- Access entitlement: `domain.AccessClass` — `free`, `free_tier`,
  `subscription`, `paid_api`, `trial`, `unknown`.
- Provider metadata: preset `kind`/`freeTier`, provider `free` flags.

Classification lives in `modelcatalog` (`Classify`, `TrackMissing`,
`FilterNew`). Entitlement policy lives in `dispatch`:

- `free` is eligible for Free-only routing.
- `free_tier` is eligible only with a verified active allowance.
- `subscription`, `paid_api`, `trial`, and `unknown` never silently qualify.
- A provider-level free flag maps to `unknown` until the route is verified, so
  it must not be used to mark a model `free`.

When adding a capability, update domain types, classifier signals, and tests.
When changing Free-only behavior, add a dispatch policy test and a routing test;
never enforce the policy only in UI labels.

## Adding a protocol adapter

1. Implement `protocol.Protocol` (`Name`, `Capabilities`). Include only features
   the wire shape can actually carry.
2. Implement optional interfaces as needed: `RequestDecoder`, `RequestEncoder`,
   `BodyBuilder`, `StreamTransformer`, `Replayable`.
3. Return `*protocol.Error` with the intended status; converters must not write
   HTTP responses.
4. Preserve tool calls, IDs, reasoning fields, and usage wherever the target
   protocol can represent them.
5. Honor `context.Context` and never leak goroutines. Do not retry across
   providers after stream bytes have been committed unless the adapter reports
   replay support.
6. Add regression tests for streaming ordering, tool calls, reasoning, usage,
   cancellation, and malformed upstream frames.

Dispatch owns the SSE writer and retry policy; the adapter only transforms
bytes.

## Testing

Run the full gates from the repository root:

```bash
gofmt -l .
go build ./...
go vet ./...
env -u ROUTENEST_DATA_DIR -u CLINE2API_DATA_DIR go test ./... -count=1
go list ./...
go build -o /tmp/routenest .
```

- Tests must use temporary data directories (`ROUTENEST_DATA_DIR`; the legacy
  `CLINE2API_DATA_DIR` remains supported) or the existing test isolation
  helpers, and must never touch the real data directory.
- Registry changes need validation/parity tests.
- Protocol changes need streaming and tool-call regression coverage.
- Store/config changes must prove temporary data never lands in production
  paths.
- If a change touches `internal/admin/ui/`, concatenate the JS and run
  `node --check`.
- Desktop builds (`-tags "desktop production"`) need GTK/WebKit system
  libraries; they may be verified in CI when the local environment lacks them.

Do not run smoke tests against a live RouteNest server. Use a scratch port and a
fresh `ROUTENEST_DATA_DIR`, record the PID you start, and terminate only that
process.
