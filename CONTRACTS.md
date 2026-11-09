# RouteNest Contract Reconciliation

Status: **Phase 1 contract baseline with Phase 2-4 implementation evidence**
Reviewed: **2026-11-09**
Scope: Phase 1 documentation reconciliation plus the isolated C1 account/authentication
storage corrections, Phase 3 client-key hardening, and Phase 4 deterministic C2 model
deletion corrections. Broader model-storage consolidation remains outside this scope.

## Evidence baseline

The Phase 1 audit baseline was clean `main` at `de36c5d` on 2026-11-08. The uncommitted
working tree now also contains the isolated Phase 2 C1 and authentication-storage safety
implementation described below. The two new documents and new regression files remain
untracked until the patch is eventually staged for review/commit. Source inspection and
focused automated verification do not prove that historical browser matrices still pass.

## Contract states

This proposal can record already accepted constraints without making the proposal as a
whole accepted. Every statement in this document has one of four states:

- **Approved contract**: the behavior or ownership that future work must preserve
  unless an explicit product decision changes it.
- **Confirmed implementation behavior**: behavior verified in the current source.
  It is evidence, not automatically the intended contract.
- **Proposed behavior**: the narrow behavior recommended for a later implementation
  phase. It is not current behavior.
- **Unresolved decision**: a product or security choice that must be approved before
  application code changes.

Historical plans, reviews, and verification reports remain dated evidence. They do
not become current test results or override an approved contract unless they are
explicitly promoted here, in `DESIGN.md`, or in an accepted ADR. This Phase 1 proposal
records documentation authority, terminology, compatibility preservation and UI
behavioral requirements for review. Every item explicitly labelled proposed or
unresolved still requires the approval named in its section.

## Proposed authority and precedence

The accepted contract is the normative product baseline. Current implementation and
executable tests remain the highest-confidence evidence of what this revision actually
does and whether a change conforms, but they do not silently redefine product intent.
When evidence and contract disagree, record the implementation as nonconforming and
resolve the conflict through the contract owner rather than weakening security or
retroactively approving the behavior.

| Concern | Authoritative source | Precedence rule |
| --- | --- | --- |
| Domain vocabulary | `CONTEXT.md` | Defines terms only. It must not contain route, persistence, or implementation decisions. |
| Provider identity | Accepted provider-system ADR, then `internal/domain` types and provider-registry validation as implementation evidence | Stable Provider Instance IDs identify configured upstreams. Display names are not mutation identities. |
| Model and offering identity | `CONTEXT.md` and this document; accepted provider-system ADR for migration constraints | A Logical Model is client-facing identity. A Model Offering is identified by Provider Instance ID plus model ID. |
| Administrative screen and route ownership | This document's canonical navigation table, then `DESIGN.md` for visual/interaction rules | Canonical hashes win over historical names. Compatibility aliases remain supported. |
| Backend endpoint permissions and CSRF policy | `internal/admin/admin.go` route registration and `internal/admin/routes.go` policy validation | Executable server policy is security authority. Documentation must not weaken or broaden it. A permission change requires an explicit security review. |
| Administrator authentication | Intended tracked operator guide `docs/admin-security.md`; `internal/adminauth` and `internal/admin` are implementation evidence | The static shell and enumerated protocol-entry routes are public; administrative operations require durable sessions and server-side RBAC independent of client API keys. Corrupt or partially missing committed authority fails closed; complete authority loss can appear as fresh bootstrap and remains an unresolved hardening decision. |
| Destructive operations | This document plus a separately approved destructive-operation decision | Labels, confirmation text, endpoint behavior, and persistence effects must name the same resources. Current wider behavior is not approved merely because it exists. |
| UI design and component behavior | `DESIGN.md`, supplemented by this document's behavioral requirements | Reuse the embedded vanilla HTML/CSS/JS architecture and existing components. Historical visual reviews are evidence only. |
| Browser verification | `CONTRIBUTING.md` and the verification matrix in this document | A result is current only when its authenticated harness, commit, viewport/theme/language matrix, and role are recorded. Screenshots alone are not assertions. |
| Persistence compatibility | Accepted provider-system ADR and `CONTRIBUTING.md` | Existing state files remain compatibility stores until a separately approved migration. Projections do not silently become new persistence authorities. |

Use two precedence orders, because current-state evidence and intended behavior answer
different questions.

For claims about **what this revision does now**:

1. Runtime behavior and current implementation, including registered route policy and
   persistence code.
2. Executable tests that exercise that behavior.
3. Current operational and public documentation.
4. Dated reports, screenshots, local plans, and historical audit artifacts.

For **normative product intent after this proposal is accepted**:

1. Server-enforced authentication, authorization, and CSRF invariants, plus accepted
   ADRs within their scopes.
2. The accepted version of this reconciliation contract.
3. `CONTEXT.md` for vocabulary and `DESIGN.md` for UI design within their declared
   scopes.
4. `CONTRIBUTING.md` for contributor workflow and extension rules.
5. Public README files and feature runbooks.
6. Dated reports, screenshots, local plans, and historical audit artifacts.

Current code and executable tests remain conformance evidence against that normative
order. A divergence is a defect or unresolved decision, not an implicit amendment.
Until acceptance, existing accepted ADRs and server security policy remain authoritative
within their scopes; this document records the proposed reconciliation.

## Approved existing constraints

The accepted provider-system ADR keeps existing persistence stores as compatibility
owners until a separately approved migration. This Phase 1 instruction also preserves
all 13 canonical routes and four aliases, forbids a broader reset without separate
approval, and forbids implementing C1 or C2 in this documentation phase.

## Desktop first-run and administrator recovery contract

- A confirmed fresh Wails installation may authorize account-store initialization and
  administrator creation only through the embedded native setup origin and its bound
  setup bridge. The localhost HTTP administration surface does not gain an anonymous
  administrator-creation capability.
- Freshness requires absence of independent RouteNest, legacy, migration,
  administrator-authority, corrupt-backup, account-pool and initialization-witness
  evidence. Missing, corrupt, unreadable, partially restored or otherwise uncertain
  established state fails closed and requires explicit recovery.
- First-run setup uses the existing administrator authority, password hashing, audit
  journal and session service. It issues a 256-bit recovery credential once, stores only
  an Argon2id verifier, and makes the native setup bridge inert after durable completion.
- A two-minute, single-use, loopback-only handoff may transfer the already-created
  administrator session from the trusted native setup page to the existing `/admin/`
  console. The handoff cannot create or reset an identity.
- Online password recovery requires the current recovery credential, is rate limited,
  rejects replay/concurrent use, rotates the verifier, revokes all administrator
  sessions and pending SSO flows, and never records raw credentials in audit events.
- Authenticated recovery-code issuance requires the reserved local break-glass identity,
  `security.write`, session CSRF and current-password reauthentication. Existing
  installations are not forced to reset their password during upgrade.
- A mutation durably prepared before a later persistence error returns the exact
  prepared recovery credential once with a restart-required outcome. The process does
  not retry by generating another credential or report ordinary success.
- The existing offline owner-terminal recovery command remains the emergency fallback.
  Localhost access, browser state, localStorage or possession of an application window
  is not administrator ownership proof.
- The canonical `#dashboard` route owns both the state-aware Welcome checklist and the
  existing metrics dashboard. Backend stores and successful request records determine
  progress; browser storage may only retain temporary presentation preferences.

## Unresolved decisions requiring explicit product approval

1. **C1 account deletion (implemented in Phase 2):** `/admin/api/accounts/delete-all`
   now has the account-only meaning and strict persistence boundary recorded below. A
   separately named broader reset remains unapproved and would require its own decision.
2. **C2 provider-scoped removal (core deletion path implemented in Phase 4):** provider-
   scoped UI removal now uses stable provider identity, and ambiguous legacy ID-only
   deletion fails closed. Broader item 11 classification/survivor policy for ID-wide
   enable/disable, metadata, test and tier operations remains an unresolved decision.
3. **Role composition:** whether provider setup that reads/replaces provider secrets,
   binds or uses existing credentials, or directs them to an administrator-selected
   endpoint requires both `provider.write` and `credential.write`, or is split into
   permission-specific steps. Current secret-bearing legacy provider/config/OpenCode
   endpoints use `provider.write`; existing route permissions remain unchanged meanwhile.
4. **Read projections:** whether provider and security administrators receive new
   redacted read endpoints for routing configuration and credential availability, or
   composite pages are split so permitted sections do not depend on stronger reads.
   Phase 1 does not broaden read permissions.
5. **Public API default:** a valid intentionally empty key list remains anonymous for
   compatibility. Phase 2B requires the one-time
   `ROUTENEST_INITIALIZE_EMPTY_ACCOUNT_STORE=true` opt-in before creating a genuinely
   evidence-free store. Missing, partially restored, unreadable, corrupt, or otherwise
   uncertain established state fails closed and cannot use that opt-in as an automatic
   reset.
6. **Technical font delivery:** the current approved baseline does not rely on an active
   bundled monospace face. Whether to activate one for technical identifiers remains
   unresolved; current CSS names a monospace stack while the IBM declarations remain
   inside an unclosed historical comment and are inactive. Historical browser evidence
   claiming active IBM loading is stale. Any approved font change requires matching-face
   load and fallback coverage checks.
7. **Named egress profiles:** they are a domain abstraction and read projection today;
   the current projection contains only named global and Zen pools and no Provider-to-
   profile binding. Generic profile CRUD/binding or custom-override inventory is not an
   approved UI capability.
8. **Dirty drafts after authoritative cleanup:** whether to approve the default rule
   that safely identified removed references are pruned from both server baseline and
   current draft while unrelated edits stay dirty. Ambiguous identity must block and
   require explicit reload/discard; it must never resurrect the reference. Existing
   cleanup already reconciles several provider/combo/alias/chain drafts, but ordinary
   config reload still discards edits and survivor criteria are inconsistent.
9. **C1 durable failure semantics (implemented in Phase 2):** the account-pool owner
   reloads authoritative disk state under the pool lock, builds a detached account-only
   copy, durably persists it, and publishes through the existing shared pool object.
   Pre-replacement errors publish nothing. Post-replacement directory-sync uncertainty
   returns an error and reconciles memory to the replaced file rather than blindly
   rolling back.
10. **C1 account-owned runtime state (still unresolved):** Phase 2 removes the account
   records and their embedded secrets, status, cooldowns and usage, but does not clear
   the separate affinity or provider-runtime stores without product approval. Stale
   affinity bindings self-evict when looked up; durable observations remain historical/
   runtime evidence. Whether to clear credential-scoped runtime observations while
   preserving accounting and receipts remains an explicit follow-up decision.
11. **C2 logical versus offering mutations and survivor policy:** the Phase 1 proposed
   classification makes provider-scoped removal the target. Existing generic Catalog
   controls remain logical-model-wide: enable/disable affects every compatibility row
   for the raw ID,
   while context/capability and test behavior need a separately approved deterministic
   logical-model aggregation before they can claim model-wide semantics. Manual tier
   editing and every Provider-labelled control are offering-scoped and must carry stable
   provider identity. Approve whether disabled or delisted declarations count as valid
   survivors for defaults and bare routing references; backend and current draft cleanup
   use inconsistent criteria.
12. **Client-key authority hardening (resolved in Phase 3):** Phase 2B makes account-pool read,
   corruption, partial-restoration and missing-file uncertainty fail closed while
   preserving verified empty-key compatibility. Authentication reads now use copied
   keys-and-error snapshots under the pool lock. Phase 3 replaces clock-derived keys
   with 256-bit `crypto/rand` values and makes ordinary key mutations detached,
   durable, serialized, and publish-after-persistence.
13. **Provider-secret role boundary:** whether `provider.write` continues to expose and
   replace raw provider keys/headers/proxy URLs and bind/use existing connections toward
   administrator-selected destinations, or new redacted reads and `credential.write`
   checks split secret access from provider configuration. Current secret-bearing legacy
   endpoints remain unchanged meanwhile.
14. **Destructive key policy (resolved in Phase 3):** `/keys/delete` requires both
   `credential.write` and `dangerous.execute`. Deleting the final effective key also
   requires the exact `disable-client-authentication` confirmation token; this preserves
   intentional empty-key compatibility without allowing an ambiguous transition.
15. **Hosted-admin enforcement:** whether non-loopback administration must require HTTPS
   as a server invariant, reject repeated physical `Forwarded` fields, and apply the same
   HSTS validation through embedding interfaces. Current HTTPS guidance is an operator
   deployment requirement; production non-loopback listeners still require proxy mode.
16. **Administrator-authority loss and recovery:** whether an initialization marker must
   distinguish complete authority-file loss from a fresh install, and how desktop/
   hosted recovery completion is constrained. Deleting authority files is not an
   approved recovery procedure; recovery completion currently uses a public protocol
   route and is not server-enforced as local-only.
17. **Browser-bound SSO:** whether OIDC/SAML flow handles must bind to the initiating
   browser in addition to current state/nonce/PKCE or RelayState/InResponseTo, signature,
   time, and replay checks, and whether resolved secret/signing-material rotation should
   revoke active sessions without a provider-configuration mutation.
18. **Built-in provider enablement:** whether the Cline Provider Instance should remain
   projected enabled when no usable Cline account exists, or expose a distinct configured
   versus currently routable state. Phase 1 does not reinterpret enabled as healthy or
   entitled.
19. **Internal/operator localization:** whether `CONTRACTS.md` and the administrator
   security guide require Chinese counterparts or summaries. Phase 1 requires parity for
   substantive public README claims; English-only internal/operator documents remain
   acceptable until this publication decision is made.
20. **Technical-value typography:** the current design keeps normal UI text in the UI
   stack and uses the shared monospace token selectively, with existing setup-form
   exceptions. Any move to a universally bundled technical face or broader monospace
   coverage requires explicit approval and browser verification.

## Approved persistence compatibility and confirmed implementation detail

Existing files remain authoritative compatibility owners during the behavioral
migration. Global Provider Instances, Provider Credentials, Model Offerings,
Availability Observations and Egress Profiles are projections; they do not own a new
consolidated persistence schema.

Provider-scoped custom offering overlays are additive state under each provider in
`.routenest-providers-sync.json`, keyed within that provider by model ID. A custom
provider's declared model IDs are the authoritative key set for those offering overlays.
The same provider partition also holds runtime state whose counters, receipts,
observations, scopes, and tombstones use their own keys and retention rules; declared
model membership does not authorize deleting that runtime evidence. `AccountPool.Models`
remains a compatibility logical-model/catalog projection and must not be used to infer
an ambiguous custom-provider mutation target. No storage consolidation or endpoint
removal is approved in Phase 1.

Client API-key generation uses 32 bytes from Go's `crypto/rand` and retains the existing
`cline_` prefix without invalidating previously issued keys. Creation and deletion load
the authoritative account pool under its owner lock, mutate a detached copy, durably
persist it, and publish only afterward. Pre-replacement errors publish nothing;
post-replacement uncertainty returns an error and reconciles memory to the winning file.
Successful and failed create/delete operations write redacted administrator audit events
that name only the client-key resource class, never the key value.

## Critical contract C1: Delete All Accounts

### Implemented behavior

The visible **Delete all accounts** action calls `POST /admin/api/accounts/delete-all`
and now means account-only deletion:

| Account-pool resource | Phase 2 effect |
| --- | --- |
| Cline account records, refresh tokens, status, cooldowns, usage and per-model stats | Removed |
| Current account index | Reset to zero |
| Client gateway API keys | Preserved byte-for-byte |
| Persisted model records, metadata, default and catalog marker | Preserved |
| Explicit built-in offering-removal reservations | Preserved |
| Persisted listen host | Preserved |
| Legacy administrator migration sentinels | Preserved |

Provider instances, connections, proxy configuration, combos, request logs, Zen state,
provider runtime state and durable administrator identity remain in their independent
stores. Subject to unresolved decision 10, Phase 2 does not actively prune the separate
account-affinity or provider-runtime stores; stale affinity entries self-evict on lookup.

The owner operation holds the pool lock, reloads authoritative persisted state, builds a
detached account-only copy, performs a durable temporary-file write, file sync, atomic
replacement and directory sync, and publishes through the existing shared pool object.
Pre-replacement failures return an error without publishing. If replacement succeeded
but directory sync reports uncertainty, the operation returns an error and reloads the
winning file so memory and disk remain truthful. The handler returns a JSON success
envelope only after successful persistence.

The Windows implementation opens the directory with write access before
`FlushFileBuffers` and retains write-through atomic replacement. Cross-compilation is
required for every change, but only a real Windows runtime result can close the Windows
verification item.

The route remains protected by `credential.write`, `dangerous.execute`, same-origin
checks and session CSRF. The confirmation and success copy identify the resources that
are deleted and preserved, suppress duplicate activation, and surface persistence
failure. No broader reset operation was introduced.

### Implemented C1 regression coverage

The isolated fix adds or updates coverage for:

1. Seed every `AccountPool` field, delete all accounts, reload from disk, and prove
   `Accounts` becomes empty, `CurrentIdx` resets to zero, and every other field is
   byte-for-byte equivalent after decoding.
2. Prove client keys survive byte-for-byte and still authenticate `/v1/models` and
   an inference endpoint after deletion.
3. Prove unauthenticated and incorrect-key requests remain HTTP 401 on key-wrapped
   model/inference routes when surviving keys exist; `/health` and `/v1/health` remain
   public liveness probes, while `/ready` and `/v1/ready` remain public and fail readiness
   when client-authentication storage is unavailable.
4. Separately characterize the existing rule that an empty loaded key list leaves key-
   wrapped model/inference routes open; `/health` and `/v1/health` remain public
   regardless, while readiness fails for unavailable authentication storage. Cover both
   intentional empty configuration and missing/unreadable/corrupt account-pool state.
   Account deletion must not transition a protected gateway to the empty-key state.
5. Prove models, default model, manual metadata, catalog marker, and removal
   reservations survive restart.
6. Prove listen host and legacy migration sentinels survive restart.
7. Prove connections, providers, proxy/Zen state, combos, request logs, and
   administrator identity/sessions are unchanged. Already-started inference behavior is
   characterized separately from subsequent credential resolution.
8. Prove missing `credential.write`, missing `dangerous.execute`, invalid CSRF,
   cross-origin requests, and unauthenticated requests are rejected without account or
   gateway-domain mutations. Security denial audit events may still be recorded.
9. Inject serialization, write, permission, rename, file-sync, and directory-sync
   failures. Prove pre-commit failure returns an error without publishing, and post-
   replacement uncertainty follows the approved degraded/shutdown or reconciliation
   policy rather than blindly restoring old memory.
10. Prove success returns a valid success envelope before the browser reports
    completion.
11. Characterize account-affinity bindings and active provider-runtime observations:
    Phase 2 leaves those separate stores unchanged pending decision 10; stale affinity
    bindings self-evict on lookup and unrelated accounting/receipts remain untouched.
12. Prove concurrent account selection, token refresh, single-account mutation, and
    OAuth/import completion obey an approved commit boundary so removed account pointers
    cannot be repersisted after the strict save without an explicit new-account action.
13. Prove deleting an already empty account list is idempotent, preserves every unrelated
    field, persists strictly, and returns the same valid success envelope.
14. The existing interaction receives a source-level English/Chinese regression for
    accurate resource copy, cancellation, duplicate activation, successful refresh and
    success feedback. The authenticated browser harness, Escape/overlay and focus matrix
    remain unavailable until the Phase 3 harness repair.

## Critical contract C2: Provider-scoped model identity

### Proposed behavior: canonical identity distinctions

**Logical Model**
The client-facing model identity. A bare logical-model name may resolve automatically
to more than one offering.

**Provider Instance**
One configured routable upstream, identified by a stable Provider Instance ID.
Display name, endpoint, and enabled state do not replace that identity.

**Model Offering**
One Provider Instance's ability to serve one Logical Model. Its deterministic key is
`ProviderInstanceID + ModelID`. Access class, availability, cooldown, upstream model
ID, limits, and provider-scoped removal state belong here.

**Model Source**
The compatibility-store origin used while persistence remains distributed. Canonical
removal references use `remote` for Cline, `zen` for OpenCode Zen, and `provider` for
custom Provider Instances. The account catalog also retains legacy `seed` Zen rows and
empty-source administrator-added/Cline-compatible rows. Source is not a substitute for
a stable Provider Instance ID.

**Default Model**
A global client model reference used when a request omits a model. Current administrator
updates accept a bare catalog ID; provider-qualified defaults are a proposed extension
and are not generally supported by current validation/fallback lookup. Removal keeps a
bare default only while a surviving offering keeps that logical ID valid; any future
qualified-default behavior requires separate approval and end-to-end tests.

**Routing Alias**
A client-visible name mapped to a target model reference, optionally provider-pinned.
It is configuration, not a Model Offering. Provider-qualified aliases must resolve to
a registered stable provider namespace.

**Combo Reference**
An ordered routing entry. Newly normalized entries retain a raw model ID plus source
and, for custom providers, stable Provider Instance ID in the compatibility `Provider`
field. Existing legacy entries may still contain a display-name pin and require unique
resolution. Display labels and qualified client names are projections over canonical
identity.

### Confirmed implementation behavior

- The newer explicit removal accepts structured `ModelRemovalRef` values and requires
  a stable `providerId` for explicit removal.
- Provider-scoped cleanup can remove the provider declaration, offering state, legacy
  cooldown, catalog projection, and stale routing references. For a built-in offering,
  it records an explicit removal reservation instead of deleting a provider declaration.
  Existing tests cover many
  sibling cases, but display-name/stable-ID collisions, literal-`@` reference cleanup,
  sparse sibling metadata, normalized runtime blockers, and direct invocation after
  built-in reservation are confirmed implementation gaps.
- The legacy `/models/delete` path still accepts only a raw ID for compatibility, but
  now rejects duplicate compatibility rows or multiple provider offerings with HTTP 409
  before mutation. Unique custom rows remain deletable; synchronized live rows remain
  protected. Persistence errors return HTTP 500 without a success envelope.
- Legacy context and capability edits also update the first matching catalog row;
  enable/disable is deliberately raw-ID-wide across every compatibility row.
- Manual tier editing can already accept `providerId` and fails ambiguous custom-
  provider matches, but the merged Models row currently submits only a raw ID.
- Provider-detail delisted cleanup reads sync state by stable provider ID and calls the
  structured removal endpoint with `source=provider`, `providerId`, and the literal raw
  model ID. The generic Models deletion flow also reads provider offerings, blocks
  ambiguous candidates, and uses structured removal for authoritative offerings.
- Automatic Cline and Zen model-gone cleanup is source-aware. Cline removes only a
  delisted `remote` row; Zen removes only delisted `zen`/legacy `seed` rows. Same-ID
  sibling offerings and sibling-backed defaults survive.
- Explicit provider-scoped removal is a sequence of strict writes, not one cross-store
  transaction. Earlier stages can commit before a later failure, retry is the supported
  completion mechanism, and the batch HTTP envelope can still be HTTP 200 with per-item
  entries in `data.errors`. Account-catalog writes now persist detached candidates and
  reconcile memory from disk when replacement succeeded but a post-replacement
  durability step reports an uncertain outcome.
- Custom-provider cleanup removes the legacy `providerId:modelId` cooldown entry. It
  preserves the colocated runtime accounting partition and does not currently perform a
  complete exact-scope cleanup of durable availability observations. Built-in Cline
  model cooldowns remain embedded per account; Zen proxy cooldowns are egress state.

### Proposed behavior: deterministic mutation targets

- Every mutation presented as acting on a Provider's model must carry the Model
  Offering key: stable provider ID plus raw model ID; source must also be supplied while
  compatibility stores require it. The existing JSON wire fields remain `providerId`,
  `id`, and `source`; semantic `model ID` wording does not rename `id`.
- Built-in identities are stable: Cline uses provider ID `cline` with source
  `remote`; OpenCode Zen uses provider ID `opencode` with source `zen`.
- The current explicit-removal handler already requires one stable `providerId` for all
  refs in a request, even though lower-level legacy normalization accepts omitted
  built-in IDs. Future callers use the stable built-in ID consistently; Phase 1 does
  not change the endpoint or decoder.
- A provider display name may be accepted only as legacy input to an explicitly
  documented compatibility adapter when it resolves to exactly one Provider Instance.
  New provider-scoped callers and the explicit-removal endpoint require stable
  `providerId`; duplicate or missing name matches are rejected without mutation.
- A raw upstream ID, including literal `@`, is never split merely because it contains
  `@`. Exact known upstream IDs and compact registered namespaces take precedence in
  every mutation and cleanup path, not only request routing. Reserved compact namespaces
  remain bound to stable provider IDs after provider deletion and must not be reassigned.
- ID-wide logical-model operations must be explicitly named and must define their
  effect on every offering. They must not be used by provider-scoped controls.
- An ambiguous ID-only legacy deletion must fail closed before any write without
  deleting, disabling, or pruning any record or reference, and return a stable
  validation error distinct from not-found and protected/built-in results.
- An unambiguous legacy deletion may retain current endpoint compatibility during a
  deprecation period, but must not be used to infer a provider when more than one
  candidate exists. Candidate detection includes built-in/offline offerings, disabled
  custom declarations, and duplicate compatibility rows. Method, path, permission, and
  CSRF policy remain compatible unless separately approved.
- Removing one offering preserves sibling providers and every sibling offering field,
  including context, output and capability metadata. Stable ID matching takes precedence
  over display-name compatibility, so one provider's name cannot capture another
  provider's stable-ID pin. A logical reference is valid when at least one declared,
  invocable surviving offering remains under the approved enabled/disabled/delisted
  policy. Bare defaults, aliases, fallback entries and combo entries then survive;
  qualified or pinned references to the removed offering are pruned deterministically.
  An empty combo is disabled under current compatibility policy.
- Eligibility blockers for the removed offering are cleared at every matching provider+
  model scope, including credential-scoped Cline model cooldowns. Recovery preserves
  runtime accounting, anti-replay receipts, and tombstone history; credential-wide and
  egress-proxy cooldowns remain because they do not belong solely to that offering.
- Phase 4 retains endpoint and persistence-shape compatibility: provider-scoped callers
  reuse `/models/remove` or `/models/delisted/remove`, while `/models/delete` remains only
  for unambiguous legacy compatibility rows and fails closed on ambiguity.

### Existing and required C2 regression tests

Existing namespace, provider-offering, removal-controller and routing tests cover many
collision, chooser, literal-`@`, retry and sibling-preservation cases. Phase 4 adds
deterministic legacy-deletion, source-aware cleanup, persistence-failure and UI contract
coverage. The broader release verification matrix remains:

1. Same raw ID across Cline, Zen, and multiple custom providers removes only the
   selected offering; one batch cannot mix provider IDs.
2. Duplicate provider display names require stable ID and reject name-only input;
   unique-name acceptance is tested only on legacy adapters that explicitly retain it.
3. Literal `@` model IDs remain intact in raw and compact-qualified references, even
   when the suffix equals an actual provider ID/name; legacy `model@ProviderName` is
   interpreted only after exact known identities fail and the suffix resolves uniquely.
4. Provider rename, deletion, restart, reserved compact-prefix reuse, and a display
   name equal to another provider's stable ID preserve stable identity and fail safely.
5. Removal clears legacy and durable eligibility blockers for the selected provider+
   model, including matching Cline account model cooldowns, while preserving sibling
   catalog rows, credentials, credential-wide/egress cooldowns, sibling-provider
   cooldowns, runtime accounting/receipts, sibling context/output/capabilities, defaults,
   aliases, and combo references except those pinned to the removed offering. Restart
   proves cleared blockers stay cleared and preserved state stays intact.
6. Removal of the final offering handles bare default, aliases, fallback chain, and
   combos according to these rules and disables empty combos.
7. Ambiguous ID-only `/models/delete` requests perform no write and return the stable
   ambiguity validation error; unambiguous not-found and protected records retain their
   distinct compatibility responses.
8. Partial persistence failures remain retryable, return the target in the batch
   `errors` collection rather than `removed`, and retain enough canonical identity to
   finish reference cleanup after restart or provider rename/deletion; a skipped target
   is not treated as completed while stale references remain.
9. Viewer and non-provider administrators cannot invoke `/models/delete`,
   `/models/delisted/remove`, or `/models/remove`; bad/missing CSRF and cross-origin
   requests also perform no model-domain mutation. Security denial audit events do not
   count as a model-domain mutation.
10. Browser tests cover instance selection, duplicate names, confirmation text,
    cancellation, partial failure/retry, focus restoration, and sibling preservation.
11. Repeating a completed provider-scoped removal is idempotent and does not prune newly
    valid sibling-backed references.
12. Source-aware automatic model-gone cleanup removes only the failing provider's
    delisted offering and preserves same-ID rows/defaults from every sibling source.
13. Catalog removal and invocation prohibition are tested separately across legacy,
    shadow and enforce dispatch modes, including all three ingress protocols, the actual
    contacted provider, a direct provider-qualified request after removal, and an
    explicit restoration workflow if restoration is approved. Built-in reservations
    remain append-only until that restoration contract is approved; add/toggle must not
    create a misleading hidden row.

## Proposed contract: canonical navigation and ownership

The following 13 hashes are the complete canonical sidebar destination set for this
phase. No additional canonical destination or compatibility alias is implied. The owner
column records primary and contract-critical shared workflows, not every helper function
that happens to render into a page.

Permissions below are the current server permissions. A page may combine dependencies
with different permissions; that is recorded rather than hidden. `admin.view` is
required for every authenticated administrator, but protected routes additionally
require every permission registered on that route. The matrix describes current page
loads, including composite failures, not the proposed partial-rendering behavior below.

| Visible label | Hash | DOM owner | JavaScript owner | Backend dependencies | Current permission | Primary mutable resource |
| --- | --- | --- | --- | --- | --- | --- |
| Overview | `#dashboard` | `tab-dashboard`, `pages/dashboard.html` | `15_dashboard.js`, `04_accounts.js` | `/stats`, `/dashboard/overview`, `/request-logs`; optional `/accounts` and `/connections` enrich account attention | Core view: `admin.view`; account/connection enrichment: `credential.write` | None; links to existing workflows |
| Providers | `#providers` | `tab-providers`, `pages/providers.html` | Primary page: `18_global.js`, `06_providers_custom.js`, `08_providers_detect.js`, `19_provider_setup.js`; shared/native workflows: `05_settings.js`, `07_providers_detail.js`, `09_providers_pickers.js`, `10_providers_import.js`, `11_connections.js`, `11_upstreams.js` | View projections and presets; secret-bearing `/providers`; provider-native configuration; `/connections` for login/binding | Projections/presets: `admin.view`; current full page load calls both `provider.write` and `credential.write` routes | Provider instances, model declarations, credential bindings, provider-native settings |
| Accounts & Credentials | `#credentials` | `tab-credentials`, `pages/accounts.html` | `04_accounts.js`, `11_connections.js`, `18_global.js` | `/accounts`, `/account-health`, `/connections`, `/credentials`, `/stats` | `credential.write`; export additionally `credential.export` | Cline accounts and provider connections; provider API credentials remain edited by their provider owner |
| Network & Egress | `#network` | `tab-network`, `pages/upstreams.html` | `11_upstreams.js`, `18_global.js` | `/egress-profiles` currently projects named global and Zen pools without provider binding; `/cline-proxy/config` and update | Projection: `admin.view`; current full page load also reads `/cline-proxy/config` with `admin.view`; update: `provider.write` | Shared default egress pool; custom-provider proxy/header overrides remain under Providers |
| Models & Offerings | `#models` | `tab-models`, `pages/models.html` | `13_models.js`, `13_model_sync.js`, `13_model_cleanup.js`, `13_model_provider_actions.js`, `18_global.js`; Zen sync uses `11_upstreams.js` | `/models`, global projections, provider identities/sync status; model mutations/imports | Base catalog/projections: `admin.view`; current `loadGlobal` handles credential detail as optional; mutation/test/sync/import: `provider.write` | Logical-model catalog and provider-scoped offerings |
| Routing Policy | `#routing` | `tab-routing`, `pages/routing.html` | `05_settings.js`, `13_models.js`, `20_routing_inspector.js` | Models, offerings, provider state, combos, logs; protected config and credential reads | Page requires `provider.write` for `/config`; inspector credential enrichment also requires `credential.write`; logs/projections use `admin.view` | Global routing policy, fallback chain, aliases, exclusions, retry/stream/affinity/error policies |
| Routing Combos | `#combos` | `tab-combos`, `pages/combos.html` | `12_combo_catalog.js`, `12_combos.js` | `/combos` and combo mutations/tests | Read: `admin.view`; mutation/test: `provider.write` | Named ordered combo definitions |
| Request Logs | `#logs` | `tab-logs`, `pages/logs.html` | `14_logs.js` | `/request-logs`; clear endpoint | Read: `admin.view`; clear: `security.write` + `dangerous.execute` | Request-log store only when clearing |
| Usage & Quotas | `#quota` | `tab-quota`, `pages/quota.html` | `17_quota.js` | `/providers`, `/stats`, cooldown reset | Current page load requires `provider.write` because `/providers` is fetched first; `/stats` alone uses `admin.view`; reset uses `provider.write` | Read-only provider budget/usage summaries plus provider cooldown reset; budget editing remains in the Providers editor |
| Provider Health | `#health` | `tab-health`, `pages/health.html` | `18_global.js` | `/provider-instances`, `/model-offerings`, `/provider-state`, `/egress-profiles`; credential projection when permitted | Base health: `admin.view`; `loadGlobal` omits credential projection without `credential.write` | None; read-only projections |
| API Keys & Security | `#security` | `tab-security`, `pages/security.html` | `03_shell.js`, `04_accounts.js`, `05_settings.js`, `06_security_users.js`; config hydration in `13_models.js` | Client keys, current password, users, sessions, SSO, listener, dangerous account/key actions; current listener/config read depends on `/config` | Current password/logout: `admin.view`; keys/accounts: `credential.write`; users/sessions/SSO and listener mutation: `security.write`; current config read also requires `provider.write`; Delete All Accounts and key deletion also need `dangerous.execute` | Client API keys and administrator security resources; account deletion is the Phase 2 account-only operation |
| Settings & Runtime | `#about` | `tab-about`, `pages/about.html` | `15_dashboard.js`, `05_settings.js`, `02_nav.js` | `/version-check`; `/stats` polling populates runtime values; `/config` may populate additional values only for `provider.write` | Version/runtime overview and stats: `admin.view`; config-derived values: `provider.write`; linked owners retain their own permissions | None; navigation and process information hub |
| Quick Start | `#quickstart` | `tab-quickstart`, `pages/quickstart.html` | `02_nav.js` | Static content and links | `admin.view` | None |

### Compatibility aliases

| Alias | Canonical destination | Required behavior |
| --- | --- | --- |
| `#global` | `#providers` | Rewrite to the canonical hash and show Providers |
| `#accounts` | `#credentials` | Rewrite and select connected accounts |
| `#import` | `#credentials` | Rewrite, select connected accounts, and open Cline setup/import |
| `#upstreams` | `#network` | Rewrite to Network & Egress |

Aliases and compatibility DOM anchors remain public compatibility interfaces. They
must not return as duplicate sidebar destinations.

## Proposed contract: UI behavioral requirements

These requirements use the current embedded architecture and existing controls. They
do not authorize new product capabilities.

### Role-specific visibility

- The server remains authoritative. Hidden or disabled controls are usability aids,
  never authorization controls.
- A screen remains visible when it has a useful permitted read-only state. Mutations
  the principal cannot perform are hidden, or disabled with a persistent accessible
  explanation that is available without pointer hover. Roles combine additively; they
  are not a privilege hierarchy.
- A page must not fail as a whole merely because one optional dependency requires a
  stronger role. It renders permitted sections and marks the others unavailable.
- Provider mutation controls require `provider.write`; account, connection, and
  client-key controls require `credential.write`; the account-export route requires
  `credential.export`; users, sessions, SSO, and listener controls require
  `security.write`; destructive controls also require `dangerous.execute` only where
  route policy says so. Delete All Accounts and key deletion require it, and clearing
  request logs therefore requires both
  `security.write` and `dangerous.execute`; `security_admin` alone is insufficient.
  Changing the current administrator's own local password and logging out remain
  `admin.view` self-service operations.
- The current browser role-to-permission map is a presentation copy only. Server route
  policy remains the source of authority. Current nonconformances include provider setup
  failing on credential reads, the routing inspector's all-or-nothing stronger reads,
  security-only listener hydration depending on `/config`, forbidden data rendered as
  zero/empty in some projections, and mutation controls outside Security remaining
  visible until the server rejects them.

### Loading, empty, success, error, unauthorized, and unavailable data

- **Loading** preserves the page structure, marks the affected region busy, and does
  not expose stale values as current. Repeated refresh and polling must not create
  overlapping mutations or duplicate announcements.
- **Empty** is a successful read with zero resources. It names what is absent and, when
  permitted, points to the existing owner workflow; it is never used for a failed or
  forbidden request.
- **Success** is announced once through the existing status/toast pattern and leaves
  controls and focus in a deterministic usable state. A mutation is not shown as
  successful before its required persistence completes.
- **Error** keeps recoverable input and successful sibling sections, gives a retry or
  corrective action when one exists, and does not render raw secrets or sensitive
  server details.
- **Unauthorized** means the signed-in principal lacks permission. Do not retry the
  request, show zero, or label it unknown. Mark the section unavailable for the
  current role without exposing server error details.
- **Unauthenticated/session expired** opens the existing login flow and does not render
  protected data. A `401` drives login; a `403` is an authenticated permission failure
  and must not create a login loop.
- **Unavailable** means a permitted dependency failed or is not ready. Preserve other
  successful sections, show retry where the existing component supports it, and do
  not convert failure to an empty inventory. Runtime-state enforcement health is shown
  separately from an empty availability-observation list; Provider Health must consume
  the endpoint's `runtimeState` rather than infer health from observation count.
- **Unknown** is a valid domain value, for example absent quota-reset evidence or
  unverified access. It must remain distinct from unauthorized, unavailable, zero,
  unsupported, and disabled.
- Redacted values must be labelled as redacted/configured; visual masking must not
  imply that the backend returned a secret. Redaction is endpoint-specific: the global
  credential projection is redacted, while current provider/config/OpenCode management
  endpoints and `/keys` can return raw secrets to their authorized roles.

### Dirty forms, navigation, and refresh

- Provider editor and Routing tuning drafts are the current dirty-form surfaces.
- Same-document navigation, language/theme changes, polling, and background reads must
  not silently overwrite unrelated dirty fields. Current ordinary Routing/Models
  navigation, refresh, and language switching call `loadConfig()` without draft
  preservation and are confirmed nonconformances to this proposed rule.
- The top refresh action refreshes read projections. If refreshing a mutable form
  would discard edits, use the existing confirmation pattern or preserve the draft.
- Subject to unresolved decision 8, server-authoritative cleanup prunes each safely
  identified removed reference from the confirmed baseline and current draft while
  preserving unrelated edits and their dirty status. If identity is ambiguous, block
  and require explicit reload/discard; never silently resurrect the removed reference.
- Leaving or reloading the document with remaining dirty state uses the existing
  unload warning.
- Cancel/discard restores the latest confirmed server baseline, not an arbitrary older
  snapshot. Bulk-header cancel must also remove the temporary editor, restore the normal
  actions, and remain idempotent across repeated open/cancel cycles.

### Confirmation and destructive actions

- Confirmation text names the resource type, scope, count when known, and irreversible
  effects. Generic "all data" wording is forbidden unless every named store is in
  scope.
- Cancel, overlay dismissal, and Escape perform no mutation and restore focus to the
  invoking control; only an explicit confirmation action commits.
- Duplicate submissions are suppressed while a mutation is pending.
- Partial batch failure identifies successful, skipped, and failed targets and leaves
  failed targets available for retry. The HTTP status and success envelope are not used
  as a substitute for inspecting the documented per-item result.
- Secret values, tokens, cookies, and raw sensitive server errors never appear in
  confirmations, toasts, screenshots, or logs. Shared diff/confirmation surfaces use
  stable field identity rather than translated labels to redact API keys, authorization
  headers, custom-header values, proxy userinfo/query values, passwords and tokens;
  browser artifacts use fixtures rather than production secrets.

### Focus and keyboard behavior

- Every dialog/drawer, including model metadata and preset/import pickers, has an
  accessible name and correct modal semantics. Background
  content is inert and excluded from the accessibility tree while a modal surface owns
  focus.
- Opening places focus on the heading, first required field, or primary task control.
- Tab and Shift+Tab remain within the topmost modal surface.
- Escape closes only the topmost dismissible surface. Destructive work is never
  accepted by Escape.
- Closing by Escape, Cancel, close button, or overlay restores focus to the connected,
  visible invoking control, with a stable fallback when it no longer exists.
- Nested surfaces keep only the topmost layer interactive; closing it returns focus to
  the invoking control in the still-open parent before the page fallback is considered.
- Tabs, sortable headers, cards that act as buttons, and row actions are keyboard
  operable and expose state through native elements or ARIA.
- Dropdowns, comboboxes, and overflow menus use native keyboard behavior or expose an
  equivalent documented pattern, close on Escape without committing, restore focus to
  their trigger, and stop dismissal from bubbling into a parent drawer/modal.
- Reduced-motion preference disables nonessential CSS transitions/animations and
  JavaScript smooth scrolling.

### Existing component and visual acceptance

- Reuse the current CSS tokens and component families; do not introduce a parallel
  design system. Spacing, radii, borders, control heights, typography, and status tones
  come from the existing token/cascade owners.
- Buttons preserve the existing primary, neutral, danger, and compact variants. A busy
  action is disabled, keeps an intelligible label, and cannot submit twice.
- Inputs, selects, and textareas keep persistent labels, programmatic error association,
  visible keyboard focus, and the user's value after validation or server failure.
- Tables keep real header relationships and keyboard-operable sort controls. Responsive
  card/compact projections expose the same resource identity, state, and actions.
- Cards, expandable model groups, sortable account headers, mobile log rows, and legacy
  import tabs that navigate, expand or mutate are native controls or equivalent keyboard-
  operable controls with exposed state. Static cards are not announced as interactive.
- Modals, drawers, and dropdowns follow the focus/keyboard rules above. Their loading,
  empty, success, and error states remain inside the owning labelled surface.
- Light and dark themes preserve visible focus and status meaning without color alone.
  Automated contrast checks must meet WCAG 2.2 AA for normal text, large text, non-text
  UI components, and focus appearance, with any exception recorded by selector and
  state.
- English/Chinese verification covers static text, generated rows, placeholders,
  accessible names, validation, confirmation, empty/error states, and text expansion.
  Substantive public claims change in both READMEs together. Subject to unresolved
  decision 19, internal/operator documents may remain English in Phase 1; that is not a
  reason to let public claims drift.

### Responsive acceptance

- Verify widths 1440, 1280, 1024, 768, 390, and 360 pixels in light and dark themes.
- Verify English and Chinese at desktop and mobile representative widths; no control,
  status, or destructive scope may disappear because text expands.
- No document-level horizontal overflow is allowed. Intentionally wide data tables may
  scroll inside their labelled container or transform to the existing compact/card
  representation.
- At widths up to 900px, navigation uses the existing off-canvas drawer, makes the
  page inert while open, closes after selection, and restores focus. Individual
  component table-to-card changes retain their existing breakpoints, currently up to
  760px for shared provider/credential management tables.
- Drawers fit the viewport and scroll internally. Topmost modal actions and required
  fields remain reachable at short viewport heights.
- Coarse-pointer interactive targets are at least 40 CSS pixels in each dimension;
  security and modal primary controls retain the existing 44-pixel target where used.
  Later component-specific selectors must not shrink those effective hit boxes.
- At 200% zoom, primary workflows reflow without clipped controls or two-dimensional
  document scrolling. Machine-readable values may wrap, truncate with keyboard-
  accessible full-value access, or scroll within their component.
- Browser assertions check `scrollWidth <= clientWidth` at the document level and that
  every required primary/destructive control has a non-zero rectangle inside its
  labelled surface at each target width/zoom.

## Proposed contract: browser verification

The required harness must bootstrap or log in explicitly, retain the administrator
cookies plus session-bound CSRF cookie, and create separate authenticated contexts for
each role. A redirect or visible login screen is a failure for a protected-route case,
not navigation coverage.

A browser result is current only when the record includes:

- the commit or exact worktree revision;
- a disposable data directory and non-production port;
- explicit administrator bootstrap/login and authenticated storage state;
- role under test;
- viewport, theme, and language matrix;
- fixture and interception scope;
- functional assertion counts separated from optional screenshots;
- console, page, HTTP, and request-failure diagnostics;
- deterministic assertions for loading, empty, success, error, focus return, duplicate-
  submission suppression, and dirty-draft preservation on each changed workflow;
- confirmation that the disposable process and data directory were cleaned up.

Required Phase 1 follow-up coverage:

1. All 13 destinations and four aliases under an authenticated break-glass session.
2. Viewer, each single administrator role, representative additive role combinations,
   and break-glass visibility/unavailable-section behavior; combinations include at
   least provider+credential and security+one dangerous role.
3. C1 and C2 browser regressions after their isolated backend fixes.
4. Dirty-form navigation/refresh/language behavior.
5. Focus containment/restoration and topmost-only Escape behavior for every modal,
   drawer, picker and overflow-menu family, including nested authentication surfaces.
6. Fullscreen logs, repeatable bulk-header cancellation without duplicate IDs, both
   existing file-import workflows (review-before-import and immediate Cline batch),
   mobile navigation, and responsive overflow.
7. Automated contrast and accessibility checks plus supported Chromium/Wails WebView
   smoke coverage.
8. Route assertions for final hash, sole `aria-current` active sidebar item, visible
   panel, key owner API calls, and absence of unexpected login/403/5xx responses. They
   also cover unknown-hash fallback, hash/session/subtab restoration, replace-state
   sidebar behavior, back/forward handling, and each alias's required subworkflow.
9. A non-loopback trusted-proxy smoke case covers canonical HTTPS origin, overwritten
   forwarding headers, secure cookies, HSTS when enabled, and rejection of direct or
   untrusted-proxy administrator access.

## Historical evidence

`review-artifacts/RouteNest-visual-design-review.md` is a 2026-11-06 baseline. Its
cards-only/static-routing and typography findings predate the current management tables,
routing inspector and later CSS layers; it is not current acceptance authority.
`docs/reports/global-provider-system-verification.md` is tied to its recorded branch and
head and contains conflicting statements about whether browser verification occurred.
Neither document may be presented as a current pass without a revision-specific rerun.

Reports and runbooks that record past passing counts retain their dates and baselines.
They must not be cited as a current pass after authentication, route, asset, or
component changes unless the documented harness is rerun. In particular, the visual
benchmark predates the current management tables and routing inspector, and the older
navigation/setup browser harnesses predate mandatory administrator authentication.
`tools/README-admin-provider-setup.md` also says the IBM payload was removed, while the
current stylesheet retains it inside an inactive comment; that runbook statement is
historical, not current font authority.
