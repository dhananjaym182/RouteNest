# Contributing

The repository root contains the CLI and desktop entry points plus the local
administrator recovery command. Service composition lives in `internal/app`;
implementation lives under `internal/`. The Go module path is `routenest`, so
repository-local imports use `routenest/internal/...`.

The Phase 1 authority and behavior proposal is recorded in
[CONTRACTS.md](CONTRACTS.md). [CONTEXT.md](CONTEXT.md) is the domain glossary,
[DESIGN.md](DESIGN.md) owns current UI design and interaction rules, and accepted ADRs
own hard-to-reverse architecture and persistence decisions. Until the reconciliation
proposal is accepted, accepted ADRs and server-enforced security policy retain their
existing authority. Treat current code and tests as implementation evidence; do not
silently convert an observed behavior into intended product policy.

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
| `CONTEXT.md` | Domain vocabulary only. |
| `CONTRACTS.md`, `DESIGN.md` | Phase 1 cross-cutting reconciliation proposal and current UI design contract. |
| `docs/adr` | Accepted hard-to-reverse architecture and persistence decisions; `.gitignore` permits new ADR Markdown files. |
| `docs/admin-security.md` | Current administrator security operations; intended to be tracked with this proposal. |
| `docs/plans`, `docs/reports`, `.omx` | Local or dated planning/evidence unless explicitly promoted; not current contract authority. |
| `internal/integration` | Cross-package tests, including the route inventory golden. |

Put new behavior in its owning package rather than in an entry point. Cross-package
dependencies are wired in `internal/app`. Do not reintroduce compatibility wrappers
in the repository root.

### Which screen owns what

| Canonical admin page (hash) | Owns | Does not own |
| --- | --- | --- |
| Overview (`#dashboard`) | read-only traffic, provider and model summaries; links to owner workflows | provider, credential, model or routing mutations |
| Providers (`#providers`) | provider setup and instances, credential binding, source model membership/import/recheck, provider-native configuration | client API keys or global logical-model policy |
| Accounts & Credentials (`#credentials`) | Cline accounts, Provider Connections and redacted API-credential inventory | Provider Instance routing rules or provider-secret editing |
| Global Egress Proxy (`#network`) | shared default egress pool for all providers and read-only named global/Zen profile inventory | provider-specific proxy/header settings or generic profile binding |
| Models & Offerings (`#models`) | logical-model catalog, provider offerings, enablement, metadata, sync and provider-scoped removal | provider credentials or global fallback policy |
| Routing Policy (`#routing`) | default model, global fallback, aliases, exclusions, retry/stream/affinity/error policy and read-only inspection | Provider Instance or credential CRUD |
| Routing Combos (`#combos`) | named ordered route definitions and their tests | physical provider/model configuration |
| Request Logs (`#logs`) | request records, filters, attempt traces, export and explicit clearing | routing-policy mutation |
| Usage & Quotas (`#quota`) | read-only provider budget/usage summaries and provider cooldown reset | budget editing, credential or account editing |
| Provider Health (`#health`) | read-only availability, cooldown and quota-reset evidence | provider mutation |
| Administrator Security (`#security`) | current-admin password and recovery code; trusted desktop devices; users, roles, sessions, SSO and listener security; explicitly scoped dangerous actions | Provider Credentials |
| Settings & Runtime (`#about`) | process/version information and shortcuts to setting owners | independent copies of linked settings |
| Quick Start (`#quickstart`) | static onboarding and links to owner workflows | mutable gateway state |

Canonical hashes and DOM panel IDs are public interfaces. Do not rename them without
an approved compatibility change. Keep these compatibility aliases:

| Compatibility hash | Canonical destination |
| --- | --- |
| `#global` | `#providers` |
| `#accounts` | `#credentials` |
| `#import` | `#credentials`, then open Cline setup/import |
| `#upstreams` | `#network` |

Aliases remain supported but must not reappear as duplicate sidebar destinations. The
route, primary/shared JavaScript owner, dependency and permission matrix is in
[CONTRACTS.md](CONTRACTS.md).

## Adding an admin endpoint

1. Put the owner logic in the owning package as a plain Go function or service
   method with no `net/http` types.
2. Add the HTTP adapter in `internal/admin/<domain>_api.go`. It parses the request,
   calls the owner, and writes the envelope through `internal/httpx`.
3. Register the route in `RegisterRoutes` in `internal/admin/admin.go` with one
   explicit HTTP method, the least privilege required, and an explicit CSRF mode.
   Read routes use `csrfSafe`; authenticated browser mutations normally use
   `csrfSession`. Public local-entry/SSO exemptions are security contracts and may
   not be added by analogy.
4. Cover method rejection, authentication, every required permission, same-origin
   and CSRF behavior, invalid JSON, not found, upstream error, and redaction of any
   credential fields in the response.
5. Run `go test ./internal/integration -run TestRouteInventoryStatic -update-route-golden`,
   then review the golden diff. Only intended methods, paths, permissions and CSRF
   policies may change.

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
The existing `tools/ui_inventory.js` script does not bootstrap or log in and therefore
is historical until it gains the authenticated harness contract below. Do not cite its
current output as protected-route coverage or feed it production diagnostic URLs. Once
repaired, run it with `PLAYWRIGHT_CORE` and `CHROME_PATH` before and after a UI change,
then compare controls, IDs, owner calls and diagnostics.

## UI behavior and browser verification

Review the proposed role, unavailable-state, dirty-form, confirmation, focus and
responsive requirements in [CONTRACTS.md](CONTRACTS.md) before changing those
behaviors. Server route policy remains the authorization authority; client-side
visibility is presentation only.

Use `node --test tools/admin_login_landing.test.mjs` for the first-paint login gate,
bootstrap/session transitions, and expired-session focus regressions. The real
`tools/admin_security_journeys_verify.mjs` harness checks both `/admin` and `/admin/`
with JavaScript disabled before testing authenticated journeys. Wait for
`data-admin-auth="signed-out"` and a visible login form, not the initially visible
loading surface, when driving unauthenticated browser or desktop-simulation forms.

A browser result is current only when it records the source revision, disposable data
directory and port, explicit bootstrap/login state, retained administrator cookies and
CSRF state, role, viewport/theme/language matrix, fixture/interception scope and browser
diagnostics. Route assertions cover final hash, active navigation, visible owner panel,
key owner requests and unexpected login/403/5xx responses. Historical screenshots and
assertion counts are dated evidence, not current passes. The navigation and provider
setup and inventory harnesses must be updated for mandatory administrator
authentication before their documented fresh-instance matrices are cited again.

Do not run browser or smoke tests against a live RouteNest server. Use a scratch port
and fresh `ROUTENEST_DATA_DIR`, record the process you start, and remove only its
validated temporary data. Never log bootstrap secrets, passwords, cookies, CSRF
tokens, Provider Credentials or client keys.

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

Provider-scoped custom offering state is identified by stable Provider Instance ID and
model ID. Each custom provider's declared model IDs own the key set for its additive
offering overlay in `.routenest-providers-sync.json`; the colocated runtime counters,
receipts, observations and tombstones have separate retention rules. `AccountPool.Models`
remains a compatibility logical-model/catalog projection. New mutations must not infer a custom
Provider Instance from a raw model ID when more than one offering can match. Provider-
scoped controls must submit the stable ID plus raw model ID and compatibility source;
ID-wide logical-model controls must be named and tested as ID-wide.

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

Documentation-only changes must at minimum run `git diff --check`, validate every
changed local link against the intended final tracked-file inventory (including new
untracked documents), compare English/Chinese public claims, and inspect the complete
diff for application-code changes. Behavioral counts, security warnings and limitations
must change in both public READMEs in the same patch. Contract changes that propose
future behavior do not count as implementation tests.

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
- Desktop authentication transitions use `go test -tags desktop -race -count=1 .`.
  Keep packaged WebView/OS-vault execution separate from these Go tests.
- `node tools/admin_security_journeys_verify.mjs --self-test` checks the browser
  harness without a server. Its real HTTP run requires private test credentials,
  `ROUTENEST_SECURITY_VERIFY_MUTATIONS=1`, and a separately initialized disposable
  server; see `docs/desktop-authentication-review.md` for commands and evidence.
- `node tools/desktop_setup_verify.mjs` is explicitly a native-bridge/API simulation,
  not Wails or credential-vault verification. `node --test tools/*.test.mjs` and
  `node tools/i18n_check.js` validate the frontend contracts.
- Parallel race/Argon2 tests can exceed the existing hashing deadline on constrained
  runners. Retry with `go test -race -p 1 ./...` without relaxing production deadlines.

Do not run smoke tests against a live RouteNest server. Use a scratch port and a
fresh `ROUTENEST_DATA_DIR`, record the PID you start, and terminate only that
process.
