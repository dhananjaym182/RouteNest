# Provider setup and account verification

Provider setup lives on Providers, including its OAuth setup tab and native Cline/OpenCode configuration drawers. Accounts & Credentials shows connected OAuth accounts and API credentials with links to the original provider editors. Network & Egress owns the existing shared default proxy pool.

The shared pool covers Cline, OAuth sign-in/refresh and custom providers without a proxy override. Native OpenCode keeps its independent pool; explicit custom overrides and direct/local connections keep their existing behavior. This change adds no global inheritance, API, credential store or provider adapter.

Inter is embedded as WOFF2 with system font fallbacks. Normal UI text and metrics use Inter; the unused IBM Plex Mono payload has been removed.

## Run

Use a separate temporary-data server, as described in [README-admin-navigation.md](README-admin-navigation.md). The tests must not run against a production instance.

```sh
go build -o /tmp/routenest-admin-verification .
ROUTENEST_DATA_DIR=/tmp/routenest-admin-verification-data /tmp/routenest-admin-verification --host 127.0.0.1 --port 3467
```

In another terminal:

```sh
BASE_URL=http://127.0.0.1:3467/admin/ node tools/admin_provider_setup_verify.mjs
SCREENSHOTS=1 ARTIFACT_DIR=/tmp/routenest-unified-final-verification node tools/admin_navigation_verify.mjs
node tools/i18n_check.js
go test ./internal/admin ./internal/adminapi
go test ./...
go build ./...
go vet ./...
git diff --check
```

Both browser suites use an existing Playwright installation and Chromium; no runtime frontend dependency is added. Set `PLAYWRIGHT_MODULE` and `CHROMIUM_PATH` when needed.

The setup suite covers both themes at 1440, 1280, 1024, 768, 390 and 360 pixels. It checks directory loading/search/retry, original form identity and restoration, page ownership, OAuth/API account links, explicit auth and save contracts, credential masking, stale async responses, fresh connection reads, model subviews, typography and overflow.

OAuth/catalogue fixtures and fake-credential provider writes are intercepted. Native OpenCode config is saved on the isolated backend and restored; proxy-credential preservation checks use intercepted reads/writes. These are UI/integration regressions, not live upstream authentication tests.

## Executed validation

- I18n: 1,215 keys; no gaps, duplicate/dead keys or unkeyed Chinese literals. Value-collision notices are advisory.
- JavaScript syntax: all 21 admin scripts and both browser scripts passed `node --check`.
- `go test ./internal/admin ./internal/adminapi`, `go test ./...`, `go build ./...`, `go vet ./...` and `git diff --check` passed.
- Navigation: 156 destination checks, 48 alias checks, 48 history checks, 24 localization checks and 72 embedded-font checks passed across both themes and all six widths; 72 screenshots captured. No console, page or request failures.
- Setup: 1,042 checks passed across all 12 theme/width combinations, including native save/restore, page/account links, auth/save payloads, async guards and masked-proxy preservation. No console, page or request failures. Sixteen representative setup screenshots captured.
- Final CSS verification: 315 read-only checks passed across both themes and all six widths after correcting mobile OAuth spacing. Native Cline/OpenCode drawers, text/action non-overlap, page/body bounds and original-node restoration passed; zero writes and zero browser diagnostics. Corrected screenshots replaced the earlier setup captures.

## Uncommitted file inventory

The following is the final workspace diff for this work. Pre-existing edits in `20_layout.css`, `45_pages_provider_system.css` and `95_control_plane.css` were preserved; `18_global.js` contains preserved edits plus integration changes. No backend business logic, endpoint, storage or adapter files were changed. Go changes register frontend assets and verify frontend contracts.

```text
DESIGN.md
THIRD_PARTY_NOTICES.md
internal/admin/ui/css/00_base.css
internal/admin/ui/css/01_fonts.css
internal/admin/ui/css/20_layout.css
internal/admin/ui/css/45_pages_provider_system.css
internal/admin/ui/css/95_control_plane.css
internal/admin/ui/css/96_workflows.css
internal/admin/ui/html/dialogs/provider_editor.html
internal/admin/ui/html/dialogs/provider_setup.html
internal/admin/ui/html/pages/accounts.html
internal/admin/ui/html/pages/dashboard.html
internal/admin/ui/html/pages/models.html
internal/admin/ui/html/pages/providers.html
internal/admin/ui/html/pages/upstreams.html
internal/admin/ui/js/01_i18n.js
internal/admin/ui/js/02_nav.js
internal/admin/ui/js/04_accounts.js
internal/admin/ui/js/08_providers_detect.js
internal/admin/ui/js/09_providers_pickers.js
internal/admin/ui/js/11_connections.js
internal/admin/ui/js/11_upstreams.js
internal/admin/ui/js/18_global.js
internal/admin/ui/js/19_provider_setup.js
internal/admin/ui_contract_test.go
internal/admin/ui_manifest.go
tools/README-admin-navigation.md
tools/README-admin-provider-setup.md
tools/admin_navigation_verify.mjs
tools/admin_provider_setup_verify.mjs
```

The implementation plan also exists at `docs/plans/2026-11-06-unified-provider-setup.md`; `docs/` is ignored by the repository's existing Git rules. Changes have not been committed or pushed in this task.
