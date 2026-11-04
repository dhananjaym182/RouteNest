# Global provider system — verification report

- Date: 2026-11-04
- Branch: `refactor/global-provider-system`
- Head: `0ec5b92` (base `5c4942c` from `refactor/provider-architecture`)
- Go: `go1.26.7 linux/amd64`
- Plan: `docs/plans/2026-11-03-global-provider-system-implementation.md`
- ADR: `docs/adr/0001-global-provider-capability-system.md`

## Scope

This report covers Tasks 0–16 and 18–19 of the implementation plan. Task 17
(admin UI rebuild) is deferred; no UI markup, script, stylesheet, manifest, DOM
ID, hash route, or translation key was changed by this refactor.

The migration keeps every persisted JSON schema, provider ID, storage key,
HTTP route, payload shape, and routing semantic intact. Global Provider
Instances, Credentials, Model Offerings, Availability Observations, and Egress
Profiles are projections composed at the composition root from the existing
stores.

## Gate results

All commands were run from the worktree root with
`GOFLAGS='-buildvcs=false'`; the full and integration suites additionally ran
with `ROUTENEST_DATA_DIR` and `CLINE2API_DATA_DIR` unset.

| Gate | Command | Result |
| --- | --- | --- |
| Formatting | `gofmt -l .` | clean (icon generator drift fixed in `0ec5b92`) |
| Diff hygiene | `git diff --check` | clean |
| Build | `go build ./...` | pass |
| Vet | `go vet ./...` | pass |
| Focused race | `go test -race ./internal/config ./internal/providerreg ./internal/provideradapters/... ./internal/providers ./internal/credentials ./internal/models ./internal/providerstate ./internal/egress ./internal/routing ./internal/admin` | pass |
| Full suite | `env -u ROUTENEST_DATA_DIR -u CLINE2API_DATA_DIR go test ./... -count=1` | pass, zero failures |
| Integration ×2 | `go test ./internal/integration -count=2` | pass, 105.6 s |
| i18n | `node tools/i18n_check.js` | gaps 0, duplicate keys 0, dead keys 0, literals without keys 0 |
| Preset generator | `node tools/gen-provider-presets.test.mjs` | 4/4 pass |

The route inventory and probe goldens changed only by five added GET lines
(`/admin/api/provider-instances`, `/credentials`, `/model-offerings`,
`/provider-state`, `/egress-profiles`). No existing golden line was modified.

## Capability matrix

`supported` means the behavior exists and is exercised by a test. `unknown`
means no trustworthy evidence exists and the system presents it as unknown
rather than guessing. `unsupported` means the provider exposes no such signal
and nothing is fabricated.

| Provider | Free evidence | Catalogue / removal | Reactive quota | Proactive quota | Reset evidence | Cooldown scopes | Credentials | Egress | Routing outcome |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Cline | Recommended catalogue classified per offering; `provider-free` hint maps to `AccessUnknown`, never free | Adapter `DiscoverModels`; absence delists after 1 authoritative snapshot; manual records protected | 429/402 classified into `providerstate` | `unsupported` (no upstream quota API) | `Try again in 1h 1m` body → explicit reset; 429 without body → 1 h fallback cooldown (`ResetAt` empty); 402 → 30 min fallback | Provider, credential, model, egress | WorkOS login, refresh-token accounts, imported/connection tokens; redacted global IDs | Global profile, per-provider override, legacy Cline proxy pool as a profile source | Legacy account rotation ordering unchanged; eligibility projected, never fabricated |
| OpenCode Zen | Free model list per offering; anonymous public identity + registered sessions | Adapter `DiscoverModels`; absence delists after 1 authoritative snapshot | 429/503 classified; Zen 503 treated as free-tier overload | `unsupported` | Standard reset headers / `Retry-After`; otherwise fallback cooldown | Provider, credential (session), model, egress endpoint | Public key, session pool, hashed session credential IDs | Global profile, per-provider override, legacy Zen proxy list as a profile source | Legacy Zen dispatch, session rotation, compaction, failover unchanged |
| API-key OpenAI-compatible | Pricing/tier classifier per offering; unknown pricing stays `AccessUnknown` | `/models` when a fetcher exists; absence delists after 2 snapshots by default | 429/5xx, rate-limit-worded 403/421/502/503, and model-gone bodies classified | Configured RPM/daily/token budget only (`configured_window`, explicit); no upstream quota API | `RateLimit-*`/`Retry-After` → explicit; otherwise 429 2 min, 403 30 min, 5xx 30 s fallback | Provider, credential (`api-key:<id>`), model, egress endpoint | API key or connection-bound OAuth token; presence exposed, secret never | Global profile, per-provider `direct`/profile/proxy URL override, environment fallback | Adapter dispatch; connection failover and attribution preserved |
| OAuth connection provider | Offering classification from catalogue; no provider-level free claim | Catalogue fetcher when configured; same 2-snapshot threshold | 401/403 → auth expired; 429/5xx classified | `unknown` unless a configured budget exists | Standard headers; fallback cooldowns | Provider, connection credential, model, egress | Device-flow connection tokens, refresh, org headers, shared bindings | Same resolver path as API-key providers | Connection rotation parks 401/403/429 and retries the next connection in place |
| Qoder | Model list is authoritative; access follows classified cost | `qoder` fetcher; adapter-owned catalogue | Signed protocol errors classified through the shared failure interface | `unknown` | Standard detectors only; no fabricated reset | Provider, connection credential, model, egress | Device-flow connection (COSY signature), validated by adapter | Same resolver path | `qoder` adapter selected from the provider definition |
| Local / self-hosted | `freeTier` is a UI hint only; free requires `CostFree` evidence | `/models` when exposed; 2-snapshot threshold | Standard status classification | `unknown` | Standard headers; fallback cooldowns | Provider, model, egress | Usually none; optional API key | Direct or profile override | Eligible as `AccessUnknown`; never ranked as free without evidence |

## Evidence by subsystem

- Global definitions and validation: `internal/providerreg` presets
  (`cline.json`, `opencode.json`, `qoder.json`, imported catalogue), canonical
  golden records, and `Validate` cross-reference tests.
- Composition: `internal/app` publishes one adapter registry, provider state,
  credentials, and egress resolver; `provideradapters` rejects incomplete
  bundles and validates every definition at startup
  (`TestComposedRegistryCoversEveryDefinition`,
  `TestEveryInvokerPropagatesCancellation`).
- Provider instances: `providers.SnapshotInstances` returns deterministic deep
  copies with redacted endpoints and rejects built-in ID collisions
  (`TestSnapshotInstancesIncludesBuiltInsAndCustomProviders`,
  `TestSnapshotInstancesReturnsDeepCopies`,
  `TestSnapshotInstancesRejectsBuiltInIDCollision`).
- Model offerings: `models.ProjectOfferings` keeps provider-scoped routes
  independent and classifies free / subscription / paid / unknown without
  conflating providers (`TestProjectOfferingsKeepsProviderRoutesIndependent`,
  `TestProjectOfferingsAccessClasses`).
- Catalogue reconciliation: `models/reconcile.go` treats empty or failed
  snapshots as non-destructive and honors per-provider thresholds and recovery
  (`TestReconcilePresenceThresholdAndRecovery`,
  `TestReconcilePresenceRejectsUnsafeSnapshotsWithoutMutatingState`).
- Availability: standard, Cline, and Zen detectors share one observation type;
  confirmed reset is stored separately from cooldown expiry
  (`TestDetectStandardFailureAt`, `TestDetectClineFailureAt`,
  `TestStoreUsesConfirmedQuotaResetAsExpiry`).
- Recovery: due scopes are unique, deterministic, and retried before recovery
  (`TestDueScopesAreUniqueAndDeterministic`,
  `TestRunRecoveryRetriesFailedProbeThenRecovers`).
- Credentials: every source projects redacted, deterministic snapshots and
  shares one ID namespace (`TestRegistryReturnsDeterministicDeepRedactedSnapshots`,
  `TestProjectAccountsRedactsAndKeepsCooldownMetadata`,
  `TestProjectZenCredentialsHashesSessionSecrets`).
- Egress: provider override → global profile → environment → direct precedence,
  endpoint cooldown, and redaction (`TestResolverPrecedence`,
  `TestResolverHonorsEndpointCooldown`,
  `TestDecisionNeverExposesRedactedProxyToTransport`).
- Planner: free-first ordering, verified free-tier requirement, pins, quality
  and weight strategies, all cooldown scopes, credentials, budgets, and legacy
  order equivalence (`TestPlanCandidatesFreeFirst`,
  `TestPlanCandidatesRequiresVerifiedFreeTier`,
  `TestPlannerMatchesLegacyProviderOrder`).
- Adapter migration: custom providers, Zen, and Cline dispatch through
  adapters with identical compatibility inputs and scoped failure projections
  (`TestCallInvokesProviderAdapterWithCompatibilityInputs`,
  `TestCallInvokesOpenCodeProviderAdapter`,
  `TestCallClineAccountInvokesProviderAdapter`,
  `TestCallProjectsClassifiedFailureIntoProviderState`).
- Admin projections: authenticated, read-only, redacted, and deterministic;
  `resetAt` stays separate from `until`
  (`TestProviderSystemHandlersExposeRedactedGlobalViews`,
  `TestProviderStateHandlerSeparatesResetFromCooldown`,
  `TestProviderSystemRoutesRequireAdminSession`).

## Preserved contracts

- No persisted schema changed. `.routenest-accounts.json`,
  `.routenest-connections.json`, `.routenest-providers.json`,
  `.routenest-providers-cooldowns.json`, `.routenest-zen.json`,
  `.routenest-proxy.json`, and `.routenest-providers-sync.json` remain
  authoritative compatibility stores.
- Provider IDs and credential ID prefixes (`cline:`, `connection:`, `api-key:`,
  `opencode:public`, `opencode-session:`) are unchanged.
- Every legacy admin endpoint remains registered; the five new GET endpoints are
  additive and authenticated.
- Existing routing semantics (account affinity, connection rotation, provider
  priority, combos, free-only eligibility, failover) are unchanged.

## Manual checks and what could not be verified

The plan asks for manual browser verification of hashes, languages/themes,
credential login/import, proxy selection, quota labels, cooldown reset, model
delisting, and free-only routing.

- Not performed: browser/Playwright is unavailable in this environment, the
  desktop build lacks GTK/WebKit, and the `designer` subagent required for UI
  work returned `401 Unauthorized`.
- No UI asset changed, so hash routes, DOM IDs, languages, and themes are
  unchanged by construction; the UI contract, manifest, syntax, inline-handler,
  and globals tests pass unmodified.
- The underlying behaviors were verified by the automated tests listed above:
  credential import/login (`accounts`, `connections`, `deviceflow`), proxy
  precedence and endpoint cooldown (`egress`), quota/reset separation
  (`providerstate`, admin projection), cooldown expiry and recovery, model
  delisting/reappearance (`models`), and free-only eligibility
  (`TestRoutingFreeOnlyRefusesPaidPin`,
  `TestFreeOnlyListingKeepsFreeModelsAndEnabledCombos`).
- Recorded as residual risk rather than a pass: visual confirmation of the
  global views in a browser.

## Migration boundaries (intentional)

- The pure candidate planner in `internal/routing/candidates.go` is not wired
  into production dispatch. Legacy provider/account ordering still owns live
  selection; the planner is validated by an equivalence test.
- `provideradapters.QuotaReader` is defined but no adapter implements it.
  Provider-reported proactive quota is therefore labelled `unsupported` or
  `unknown`; only configured budgets produce proactive quota state.
- Persisted JSON has not been consolidated into a new schema.
- Legacy admin endpoints are retained for the announced compatibility window.
- The admin UI has not been rebuilt around global concepts (Task 17).

## Commits under verification

`41c3b6c` characterization → `ef4a412` domain → `acecfd0` definitions →
`56da489` adapters → `8f7233b` instances → `0ded015` offerings → `9fc5e2a`
reconciliation → `3319e41` detector → `74bcb51` state store → `ab9f168`
credentials → `f87f3f9` egress → `f9efd5d` planner → `25a24c3` custom
dispatch → `3dcda73` Zen → `59ff8af` Cline → `f558ca5` admin projections →
`a3a4092` residue removal → `0ec5b92` formatting.
