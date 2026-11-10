# Rebuilt CLI and login landing follow-up

Evidence for the user-reported missing password help and localhost login landing,
collected from the uncommitted worktree based on `4099b30`.

- `browser-journeys.json`: real HTTP/Chromium password, recovery, session, and targeted
  accessibility verification. No authentication API mocks or native-vault certification.
- `browser-login-light-en.png`: final standalone browser sign-in, 1440 pixels.
- `browser-login-dark-zh.png`: final standalone browser sign-in, 1440 pixels.
- `browser-login-mobile-dark-en.png`: final standalone browser sign-in, 390 pixels.
- `desktop-simulation.json`: shipped native UI with simulated bridge/API, 32 cases and
  750 assertions. This is not packaged Wails or OS credential-store runtime evidence.

Real `localhost` navigation was separately exercised against a fresh IPv4 loopback
server inside an isolated user/network namespace. Both `/admin` and `/admin/` reached
the canonical first-run password-creation page. GET/HEAD redirects created no cookies;
API requests at alternate hosts remained rejected. No production state was used.

Screenshot fields were empty or masked; no test password, bootstrap secret, session,
CSRF token, persistent device secret, or recovery code is retained. Full initial
verification and remaining target-OS/older Access Settings limitations remain in
`docs/desktop-authentication-review.md`.
