# Model delisting notifications

Cline, OpenCode Zen, and custom-provider delistings share the model-sync popup.
Notices identify the source and model IDs, keep concurrent source changes
together, and remain dismissed while the same delisting flags are unchanged.
A model that returns upstream and is later delisted produces a new notice.

Provider rechecks and settings/catalogue loads check for delistings immediately.
While the admin is visible, a 60-second timer checks the local catalogue without
triggering upstream discovery or redrawing configuration forms. Returning to
the browser tab also checks it. Backend discovery intervals and model retention
rules stay the same; new custom-provider models remain pending import review.

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
