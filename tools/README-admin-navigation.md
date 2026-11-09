# Admin navigation browser regression

`admin_navigation_verify.mjs` exercises the actual Go-embedded admin and its existing APIs. It does not mock responses or install project dependencies.

Use an existing Playwright or Playwright Core installation and Chromium. The script resolves local `playwright`, local `playwright-core`, or the previous audit installation at `/tmp/routenest-playwright/node_modules/playwright-core`. Override this with `PLAYWRIGHT_MODULE` and `CHROMIUM_PATH` when needed.

Build and run a separate instance with an empty temporary data directory; do not use production credentials for screenshots. The server and verifier must receive the same disposable bootstrap secret, and the verifier also needs a disposable permanent test password:

```sh
go build -o /tmp/routenest-admin-verification .
ROUTENEST_DATA_DIR=/tmp/routenest-admin-verification-data \
ROUTENEST_ADMIN_BOOTSTRAP_PASSWORD="$ADMIN_TEST_BOOTSTRAP_SECRET" \
ROUTENEST_INITIALIZE_EMPTY_ACCOUNT_STORE=true \
/tmp/routenest-admin-verification --host 127.0.0.1 --port 3467
```

In another terminal, run the suite from the repository:

```sh
ADMIN_TEST_BOOTSTRAP_SECRET="$ADMIN_TEST_BOOTSTRAP_SECRET" \
ADMIN_TEST_PASSWORD="$ADMIN_TEST_PASSWORD" \
node tools/admin_navigation_verify.mjs
```

Optional configuration:

```sh
BASE_URL=http://127.0.0.1:3467/admin/ \
ADMIN_TEST_BOOTSTRAP_SECRET="$ADMIN_TEST_BOOTSTRAP_SECRET" \
ADMIN_TEST_PASSWORD="$ADMIN_TEST_PASSWORD" \
PLAYWRIGHT_MODULE=/path/to/node_modules/playwright-core \
CHROMIUM_PATH=/usr/bin/chromium \
SCREENSHOTS=1 \
ARTIFACT_DIR=/tmp/routenest-navigation-verification \
node tools/admin_navigation_verify.mjs
```

`FONTCONFIG_FILE` may be set to an existing Fontconfig configuration when required by the system Chromium installation. The script itself does not depend on client-installed Inter: it verifies embedded WOFF2 declarations and successfully loaded font faces at every declared UI weight.

Coverage:

- All 13 sidebar destinations through real clicks, at 1440, 1280, 1024, 768, 390, and 360 pixels, in both themes: 156 navigation cases.
- Only the intended page panel is visible; only the intended sidebar button is `active` and has `aria-current="page"`; all other sidebar entries are inactive.
- Canonical URL hashes, matching shell/sidebar titles, and page-level horizontal overflow.
- Mobile drawer opening, background `inert`, navigation closure, and restored background interaction.
- All four legacy hashes (`global`, `accounts`, `import`, `upstreams`), including the shared OAuth account view and the native Cline import drawer: 48 alias cases.
- Back and forward through real same-document hash history: 48 traversals. Sidebar clicks intentionally keep the existing `replaceState` behavior and do not add history entries.
- English navigation titles and Chinese legacy/history navigation.
- Exact multi-provider gateway dashboard subtitle, focused Provider Health & Diagnostics title, retained health projections, and absence of redundant health shortcuts.
- Actual theme control switching and pressed states in desktop and mobile navigation.
- Embedded Inter at 400–800, with no font runtime URL requests.
- Browser console errors, uncaught errors, and failed requests.

The script emits progress to stderr and a JSON result to stdout. Exit code 0 means all checks passed, 1 means assertions failed, and 2 means browser execution stopped. Diagnostics identify the safe operation label while avoiding page content, credentials, URL queries, and raw browser errors. Optional screenshots cover Overview, Providers, Accounts & Credentials, Network & Egress, Models and Provider Health in each theme and width; the browser closes in a `finally` block.

See [README-admin-provider-setup.md](README-admin-provider-setup.md) for native setup, account links and async workflow regression coverage.

Stop the separate verification server with Ctrl-C afterward. The test does not manage or alter an existing server process.
