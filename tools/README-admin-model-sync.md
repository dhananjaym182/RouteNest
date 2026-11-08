# Model delisting notifications

Cline, OpenCode Zen, and custom-provider delistings share the model-sync popup.
Notices identify the source and model IDs, keep concurrent source changes
together, and remain dismissed while the same delisting flags are unchanged.
A model that returns upstream and is later delisted produces a new notice.
Acknowledgements persist in this browser across full-page reloads. Provider
identity is checked against current local sync status before catalogue notices.

Each newly delisted offering has a **Remove** button; **Remove all delisted**
cleans the offerings shown in the popup in bounded batches. Cleanup removes
provider declarations, offering state, pending import entries, cooldowns, and
affected routing references. An active offering at another provider survives.
Failed items remain in the popup for retry. These are explicit one-click actions.

Provider rechecks and settings/catalogue loads check for delistings immediately.
While the admin is visible, a 60-second timer checks the local catalogue without
triggering upstream discovery or redrawing configuration forms. Returning to
the browser tab also checks it. New custom-provider models remain pending import review.

Implementation layout and bounded extraction plan:

| File | Responsibility |
| --- | --- |
| `internal/admin/ui/js/13_models.js` | Catalogue table and configuration loading. |
| `internal/admin/ui/js/13_model_sync.js` | Notification identity, persistent acknowledgements, and bounded cleanup actions. |
| `internal/admin/ui/js/13_model_cleanup.js` | Reconcile removed references in open drafts while preserving other edits. |
| `internal/adminapi/model_removal.go` | Validate and report individual and batch removal requests. |
| `internal/providers/model_removal.go` | Coordinate strict cleanup across existing compatibility stores. |

Keep the existing popup IDs, controller names and API contracts while extracting
notification code from the table controller. The current notification regressions
lock that behavior. Add reload-after-rename, large-batch and stale-draft save
regressions before the corresponding corrections, then run them against the
assembled page and browser flows.

Run the controller regressions (also included in `go test ./internal/admin`):

```sh
node --test tools/admin_model_sync.test.mjs
```

Run the browser checks using an existing Playwright installation and Chromium:

```sh
go build -o /tmp/routenest-model-sync-verification .
node tools/admin_model_sync_verify.mjs
```

The browser script starts and stops its own server with a fresh temporary data
directory. Sync/recheck requests use intercepted fixtures; unexpected writes
fail verification. It covers desktop/mobile, both themes, native settings and
provider drawers, manual sync, background checks, dismissal, and unsaved edits.
Screenshots are written to the temporary artifact directory printed by the script.
