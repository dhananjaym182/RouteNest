# Compact model catalog and provider management

The catalog previously used separate lines for the upstream model, badges,
the repeated "Provider model names" caption, and qualified names. Hundreds of
models produced unnecessary vertical space. Each catalog row now presents the
original ID first, its matching copyable qualified names next, and compact
metadata inline. Long names use ellipsis with full tooltips; copying retains
the complete literal model ID. Provider offerings retain their existing layout.
Provider prefixes stay visible when names shorten. On small screens, badges
share the context/output/action line while model names remain together.
When a shared model has more aliases than fit, its alias strip scrolls
horizontally within the row so every copy button remains reachable.

```text
AntSeed  99/100  [Enable all] [Disable all] [Edit Provider] [Remove models]
  [on] upstream-model  [as/upstream-model ⧉]  Free  ⓘ  AntSeed  200K  32K  …
```

## Provider actions

**Edit Provider** opens the existing provider editor or built-in settings.
**Remove models** opens a searchable list for that provider. Choose individual
models, remove a single row, or select all matching search results and remove
the selection. Confirmation names the provider and model count. Failed models
remain available for retry after partial errors. The dialog traps keyboard
focus, supports Escape/cancellation, and returns focus to the catalog.

Catalog rows still merge equal raw model IDs for display. Administration uses
stable instance IDs. A group containing duplicate provider display names opens
an explicit instance chooser; it cannot silently pick the first match.

```json
POST /admin/api/models/remove
{"models":[{"id":"vendor/model@revision","source":"provider","providerId":"antseed_primary"}]}
```

Sources are `remote` with `cline`, `zen` with `opencode`, or `provider` with a
custom stable ID. Each bounded request targets one provider. The response
returns `removed`, `skipped` and per-model `errors`. The UI batches large
selections and refreshes models, providers, offerings, combos and routing
settings after mutation while preserving unrelated unsaved edits.

Removal deletes the provider's model declaration and affected references.
Built-in removal reservations prevent sync or offline fallback from restoring
manually removed offerings. Equal raw IDs offered by other providers remain
independent. The existing delisted-only popup uses its original endpoint.

Read-only administrators can inspect and copy the same qualified names. Catalog
loading uses public provider identity and offering projections, skips protected
credential/configuration reads when permission is absent, and keeps provider
editing and removal controls restricted to provider administrators.

## File layout

| File or directory | Responsibility |
| --- | --- |
| `internal/admin/ui/js/13_models.js` | Raw ID / qualified-name / badge ordering and provider header actions. |
| `internal/admin/ui/js/13_model_provider_actions.js` | Stable instance chooser, searchable selection, bounded removal and error retry. |
| `internal/admin/ui/js/13_model_sync.js` | Clear completed provider notices and use public identities for read-only delisting observations. |
| `internal/admin/ui/html/dialogs/model_sync.html` | Accessible provider action dialog, confirmation and selection controls. |
| `internal/admin/ui/css/96_workflows.css` | Compact catalog rows and responsive removal dialog. |
| `internal/admin/ui/js/03_shell.js` | Keep drawer Escape handling behind the new modal. |
| `internal/admin/ui/js/05_settings.js`, `internal/admin/ui/js/18_global.js` | Respect credential read permissions while retaining public model alias metadata. |
| `internal/admin/ui/js/02_nav.js` | Invalidate delayed dialog/editor opens after navigation. |
| `internal/admin/ui/js/08_providers_detect.js` | Keep the provider editor's follow-up read in the same navigation context. |
| `internal/admin/ui/js/01_i18n.js`, `internal/admin/ui_manifest.go` | Translations and script assembly. |
| `internal/adminapi/model_removal.go`, `internal/admin/model_removal.go` | Bounded decoding, whole-request validation and authenticated administration. |
| `internal/providers/model_removal.go`, `internal/models/removal.go` | Strict provider-scoped removal and catalog reconciliation. |
| `internal/config/model_removal.go` | Prune canonical source and qualified combo/routing references while preserving automatic and sibling-provider references. |
| `internal/domain/account.go`, `internal/models/removal_policy.go`, `internal/models/sync.go` | Persistent built-in removal policy and sync filtering. |
| `internal/routing/catalog.go`, `internal/routing/model_offerings.go`, `internal/routing/snapshot.go` | Effective catalog/default/free-chain filtering and request-local fallback policy. |
| `internal/zen/zen_catalog.go`, `internal/app/wiring.go` | Scoped offline Zen resolution and composition for removal. |
| `internal/integration/route_inventory_test.go`, `internal/integration/testdata/` | Track the new authenticated route and additive saved-policy fields. |
| `tools/admin_model_provider_actions.test.mjs` | Controller regressions for scope, names, cancellation, batching and retry. |
| `tools/admin_model_sync.test.mjs` | Regressions for read-only catalog loading, scoped notice acknowledgements and protected reads. |
| `tools/admin_combo_isolation_verify.mjs` | Disposable-server browser checks for dense geometry, copy, provider edits and removal. |

Run the focused controller tests with:

```sh
node --test tools/admin_model_provider_actions.test.mjs tools/admin_model_sync.test.mjs tools/admin_combo_isolation.test.mjs
```

Browser checks use a disposable data directory and fixture providers. They do
not remove configured models or send inference to the live service.

## Verification

The full Go suite passed all 52 packages, `go vet ./...` passed, and all 110
JavaScript controller/generator tests passed. Translation checks reported no
missing translations, duplicate keys, unused keys or missing literal keys.
Canonical Cline and OpenCode removal regressions verify persisted cleanup while
retaining automatic raw references, live sibling pins, defaults and credentials.
The administrator package also passed a fresh full run after the final UI
permission guards were added.

The comprehensive browser matrix completed 92 functional cases and 2,727
checks across light/dark themes and 390/1440px viewports, including 52 real
clipboard readbacks, 20 scoped removals and four viewer mutation-denial probes.
That run recorded one intermittent connection reset without a URL; it had no
HTTP failures or unintended writes. The final focused viewer run passed all 58
checks with zero console, HTTP or transport errors, including mouse reachability
and exact copying of four aliases in the horizontally scrolling strip. Its
disposable fixture was stopped and cleaned.
