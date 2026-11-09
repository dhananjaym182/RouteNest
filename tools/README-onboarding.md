# Onboarding and recovery browser verification

These scripts use an existing Playwright Core installation and system Chromium; they do
not add project dependencies.

## Desktop setup asset

Serve the embedded asset directory from a disposable loopback server, then run the
trusted-binding simulation:

```sh
python3 -m http.server 8765 --bind 127.0.0.1 --directory desktop/setup
PLAYWRIGHT_MODULE=/path/to/playwright-core \
CHROMIUM_PATH=/usr/bin/chromium \
DESKTOP_SETUP_URL=http://127.0.0.1:8765/ \
ARTIFACT_DIR=/tmp/routenest-desktop-setup \
node tools/desktop_setup_verify.mjs
```

It covers normal and restart-required completion at 1440, 1280, 1024, 768, 390 and
360px, English and Chinese locales, light and dark system themes, recovery-code
acknowledgement, overflow, session handoff navigation, and native quit behavior. This is
asset/runtime-contract verification, not a real Windows/macOS/Linux Wails runtime test.

## Authenticated Welcome and recovery

Run a separate RouteNest instance with a temporary data directory, then:

```sh
ROUTENEST_ONBOARDING_VERIFY_MUTATIONS=1 \
BASE_URL=http://127.0.0.1:3467/admin/ \
ADMIN_TEST_BOOTSTRAP_SECRET="$ADMIN_TEST_BOOTSTRAP_SECRET" \
ADMIN_TEST_PASSWORD="$ADMIN_TEST_PASSWORD" \
ADMIN_TEST_RECOVERED_PASSWORD="$ADMIN_TEST_RECOVERED_PASSWORD" \
PLAYWRIGHT_MODULE=/path/to/playwright-core \
CHROMIUM_PATH=/usr/bin/chromium \
ARTIFACT_DIR=/tmp/routenest-onboarding \
node tools/admin_onboarding_verify.mjs
```

The mutation guard is mandatory because the verifier issues and consumes real recovery
credentials and changes the disposable administrator password. It checks 24 Welcome
layouts and 24 recovery-dialog layouts, recovery issuance, password reset, replacement
code rotation, session revocation, used-code rejection, re-login, browser diagnostics,
and screenshots without printing credentials.
