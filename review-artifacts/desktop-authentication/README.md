# Desktop authentication review artifacts

These artifacts are for independent review of the uncommitted authentication changes.
No release or packaged native-runtime certification is implied.

The later user-reported CLI help and localhost login fixes have separate current evidence
in [login-landing-followup/](login-landing-followup/README.md). Initial screenshots and
counts below are retained as dated review evidence, not the latest login appearance.

## Actual HTTP browser screenshots

Captured from the Go-embedded console on a disposable, password-authenticated IPv6
loopback server using Chromium and the real administrator service. There are no
mocked authentication APIs. Recovery values and password fields are masked before
capture and restored only inside the disposable browser.

- `browser-login-light-en.png`
- `browser-login-dark-zh.png`
- `browser-security-light-en.png`
- `browser-security-dark-en.png`
- `browser-recovery-form-dark-en.png`
- `browser-recovery-result-redacted.png`
- `browser-journeys.json`: redacted real HTTP/security/accessibility evidence.
- `browser-navigation.json`: all twelve authenticated navigation/history/theme matrices.
- `browser-access.json`: the older Access Settings harness result, including any recorded limitation.
- `file-inventory.json`: authentication-scoped changed/new file inventory.
- `verification-summary.json`: final test/build results and explicit runtime limitations.

## Desktop UI simulations

These use the shipped HTML, CSS, and JavaScript with an explicitly simulated bridge
and API. Displayed recovery codes and passwords are fictional fixture values.
They do not verify Wails IPC, a real OS vault, or a packaged desktop session.

- `simulation-setup-password-dark.png`
- `simulation-setup-recovery-dark.png`
- `simulation-login-mobile-zh.png`
- `simulation-security-mobile-dark.png`

The full simulation run produced 32 cases, 742 assertions, and 80 screenshots across
English/Chinese, light/dark, and 1440/390-pixel widths. Additional browser matrices
exercise 360 pixels and 200%/400% zoom-equivalent reflow. The targeted checks are not
an axe audit or a WCAG conformance certification. See
[the review report](../../docs/desktop-authentication-review.md) for exact commands,
current results, and platform limitations.
