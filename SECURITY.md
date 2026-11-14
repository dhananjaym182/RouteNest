# Security policy

## Supported versions

RouteNest is a prerelease project. Until a stable-release support policy is
published, only the latest public preview on the [official releases page](https://github.com/dhananjaym182/RouteNest/releases)
receives security fixes. There is no stable-release support policy yet.

| Version | Security fixes |
| --- | --- |
| Latest public preview | Supported |
| Older public previews | Not supported; upgrade to the latest preview |
| Unreleased development builds | No separate security support |

Fixes are released in the latest preview; older previews do not receive backports.

## Report a vulnerability privately

Use [GitHub Private Vulnerability Reporting](https://github.com/dhananjaym182/RouteNest/security/advisories/new)
to report suspected RouteNest vulnerabilities privately to the maintainers. This
feature is enabled for this repository. Sign in to GitHub, open the repository's
**Security** tab, and select **Report a vulnerability**.

**Do not put secrets, persisted state, state exports, or logs in public issues,
pull requests, discussions, or comments.** This includes administrator passwords,
bootstrap or recovery codes, session cookies, CSRF tokens, trusted-device
credentials, client API keys, provider credentials, request/capture logs, and audit
logs. Use synthetic values and sanitized, minimal evidence even in private reports;
do not attach a production data directory or full state export. If a secret has
already been exposed, revoke or rotate it; deleting a post does not make it safe.

Include the following where available:

- The affected preview tag or exact commit, installation source, operating system
  and architecture, and deployment mode (CLI/server, Docker, or desktop).
- The affected component or endpoint, expected security boundary, and observed
  behavior, including whether it reproduces on the latest public preview.
- Reproduction steps and a minimal proof of concept using synthetic or redacted
  data. Describe prerequisites such as authentication, administrator role, local
  access, public exposure, reverse-proxy settings, or SSO configuration.
- The likely impact, attacker capabilities required, and users or data at risk.
- Any known mitigation, suggested fix, or related upstream advisory.
- Any existing disclosure plans and whether you would like reporter credit.

## Coordinated disclosure

We aim to review reports privately, assess their scope and impact, and work with
reporters on reproduction, mitigations, and a fix for the latest public preview.
Please keep vulnerability and exploit details private while this work is
coordinated, and discuss publication timing with the maintainers through the
private report. For confirmed issues, disclosure can include a public security
advisory, affected and fixed versions, mitigation guidance, and reporter credit
with the reporter's consent.

Timing depends on severity, reproducibility, maintainer availability, and any
upstream coordination. No response, remediation, or disclosure deadline or SLA is
guaranteed.

## Upstream services and provider accounts

RouteNest uses third-party dependencies and connects to external provider APIs,
local runtimes, and account services. Issues confined to those products or
services, including provider-account compromise unrelated to RouteNest, may belong
with the upstream maintainer's security team or the provider's private security or
support channel. RouteNest-specific credential exposure, authorization bypasses,
or unsafe handling of upstream data should be reported here. If ownership is
unclear, describe the suspected boundary in a private report so coordination can
be discussed.

## Secure operation

See [Administrator security and SSO](docs/admin-security.md) for administrator
setup, roles, hosted administration, secret handling, trusted desktop devices,
SSO, and recovery guidance.
