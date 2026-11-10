# Access Settings browser regression

`admin_access_verify.mjs` tests the Go-embedded Access Settings with real administrator APIs and cookies. It creates one disposable local user, resets that user's password, and rotates the isolated administrator's password. Cleanup deletes only that disposable user and restores the original test password. The script does not start, stop, or modify an existing process.

Use a separate server with an empty directory created by `mktemp -d` under the system temporary directory. Never use a deployed gateway or production credentials. The suite requires `ROUTENEST_ACCESS_VERIFY_MUTATIONS=1`, a loopback `BASE_URL`, and `ADMIN_TEST_PASSWORD`; it also checks the server's account-pool path is inside the system temporary directory before user or password mutations. A fresh server additionally needs `ADMIN_TEST_BOOTSTRAP_SECRET`, matching its `ROUTENEST_ADMIN_BOOTSTRAP_PASSWORD` environment variable. The bootstrap secret retains its separate 12-byte minimum; permanent passwords have an 8-character minimum.

Build a separate binary with `go build -o /tmp/routenest-access-verification .`. Start it in another terminal with a fresh `ROUTENEST_DATA_DIR`, `ROUTENEST_ADMIN_BOOTSTRAP_PASSWORD`, and `--host 127.0.0.1 --port 3467`. Supply private test credentials through environment variables; do not put them in screenshots or command logs.

Run from the repository with those test variables already exported:

```sh
ROUTENEST_ACCESS_VERIFY_MUTATIONS=1 \
BASE_URL=http://127.0.0.1:3467/admin/ \
node tools/admin_access_verify.mjs
```

The suite reuses an existing `playwright` or `playwright-core` installation, including `/tmp/routenest-playwright/node_modules/playwright-core`, and Chromium at `/usr/bin/chromium`. No dependencies are installed. Set `PLAYWRIGHT_MODULE` or `CHROMIUM_PATH` to an existing installation if needed. `FONTCONFIG_FILE` may point to an existing Fontconfig configuration required by the local Chromium installation.

Set `ARTIFACT_DIR` to save viewport screenshots of both protocol editors at every width and theme. The suite scrolls the editor into view without changing application layout styles. Screenshots use the isolated account and unsaved reference-only SSO fixtures. The suite never contacts an identity provider or saves an SSO fixture.

Coverage:

- Clear administrator-security sections, including Desktop Sign-In, at 1440, 1280, 1024, 768, 390, and 360 pixels in both themes; inputs stay within cards and the page has no horizontal overflow.
- English and Chinese 8-character help, labels, bootstrap placeholders, and minimum-length attributes.
- Input labels, accessible icon buttons, mobile touch targets, keyboard password-field order, role selection, and optional-field disclosure.
- New optional OIDC/SAML and group fields start collapsed; populated configuration opens the applicable fields automatically.
- Checkbox defaults preserve OIDC/SAML payloads, group JSON validation rejects invalid or reserved roles, and signing references remain paired.
- Federated accounts show the identity-provider password notice and viewer permissions hide security administration controls. This presentation check uses a synthetic principal only in the browser; authentication remains real.
- Real 8-character local-user creation, reset, and login; 7-character Unicode inputs fail UI validation.
- Real 8-character administrator-password rotation revokes both the original token and an independently logged-in session, while the initialized administrator identity remains mandatory.

The script prints safe operation labels to stderr and a compact JSON result to stdout. A nonzero exit means validation or cleanup failed. Raw credentials, cookies, tokens, page content, and unredacted browser/API errors are never printed. Stop the separate test server afterward and discard only its validated temporary data directory.
