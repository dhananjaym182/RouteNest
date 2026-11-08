# Design

## Source of truth

Active, reconciled 2026-11-08. This document owns current UI design and interaction rules. [CONTRACTS.md](CONTRACTS.md) records the Phase 1 cross-cutting authority proposal, canonical navigation baseline, current permissions, destructive-operation proposals and model-mutation identity for review. [CONTEXT.md](CONTEXT.md) owns domain vocabulary. Accepted ADRs own hard-to-reverse architecture and persistence decisions. Until the reconciliation proposal is accepted, accepted ADRs and server-enforced security policy remain authoritative within their scopes; current code is implementation evidence, not automatic product intent.

## Brand

Approachable, clear developer application. Real configuration and honest status build trust. Avoid decorative heroes, tiny metadata, unnecessary jargon and dense capability dashboards.

## Product goals

One Connect provider entry point on Providers; original integrations and request contracts preserved. Generic provider OAuth creates a Provider Connection that is selected before explicit provider save. Native Cline OAuth directly creates a Cline Account; native OpenCode Zen uses its existing configuration flow. Primary Models catalog with secondary provider availability.

## Personas and jobs

Self-hosted gateway operators connect services, add accounts, choose models and diagnose issues. New users need clear choices; experienced operators retain advanced controls.

## Information architecture

Keep all 13 canonical sidebar routes and all four compatibility aliases recorded in `CONTRACTS.md`. Providers owns shared setup and connected inventory. Accounts & Credentials owns OAuth Connected Accounts, Cline accounts and the redacted API Credentials inventory; provider secrets remain edited through their provider owner. Providers has inventory and OAuth setup tabs; native service configuration, proxies and headers live with each provider. Network owns the shared default egress pool plus the current read-only named global/Zen egress projection; custom-provider override editing remains under Providers because no generic binding projection exists. Models has Catalog and Provider availability subviews. Compatibility aliases remain supported but are not duplicate sidebar destinations.

## Design principles

One primary action; progressive disclosure; reuse working forms and handlers. Enabled means configured, not tested healthy. Do not invent model access, auth flows or status.

## Visual language

Inter and the existing CJK/system fallbacks own normal app content and metrics. Machine-readable values use the shared monospace token where the existing component marks them as technical; setup-form controls retain the current UI-font exception. The current approved baseline does not rely on an active bundled monospace face; any reconsideration is unresolved in `CONTRACTS.md`. The IBM declarations sit inside an unclosed historical CSS comment, so documentation and verification must not claim that IBM Plex Mono is loaded until a separately approved font change activates matching `@font-face` rules. Preserve the intentional dark/light themes, 14px body, 26-28px desktop page headings, 25px mobile page headings, 17-19px section headings, and shared spacing and border tokens. Existing dense table metadata may remain 11-12.5px only when contrast, zoom and full-value access pass the acceptance matrix.

## Components

Reuse shared drawers, confirmations, original provider editor, login/import modals, buttons, inputs, selects, tables, cards and model controls. Preserve the existing primary, neutral, danger and compact variants, labelled form/error patterns, real table headers and responsive compact/card projections. Add a searchable setup directory; temporarily move original Cline/OpenCode nodes from Providers into its workspace and restore them without cloning IDs/listeners. Existing CSS tokens own spacing, sizing, radii, borders, typography, focus and state styling; do not create a parallel design system.

## Accessibility

Use semantic labelled dialogs and buttons, keyboard-operable tabs/sorts/rows/dropdowns, topmost-surface focus containment, deterministic focus restoration, Escape dismissal, meaningful state text and reduced motion. Preserve nested authentication dialogs and mobile navigation behavior. Visible focus and state meaning cannot rely on color alone; future browser gates use WCAG 2.2 AA contrast and focus criteria. Unauthorized, unavailable, unknown, unsupported, disabled and zero are distinct states; the proposed detailed role/state acceptance requirements are recorded in `CONTRACTS.md` for review.

## Responsive behavior

Verify 1440, 1280, 1024, 768, 390 and 360px in both themes, with representative English and Chinese checks at desktop and mobile widths. No document-level horizontal overflow is allowed. Intentionally wide tables scroll inside their component or use the existing compact/card form. Provider items and filters stack on mobile; drawers fit the viewport and scroll internally. Preserve the existing off-canvas navigation behavior up to 900px; proposed coarse-pointer, 200% zoom and detailed verification criteria are recorded in `CONTRACTS.md` for review.

## Interaction states

Loading, empty, retry, error and success states are explicit and remain inside their owning labelled component; a failed/forbidden read is never presented as zero or empty. No authentication or saves occur until deliberate action. Retain existing auth polling/errors, config autosave, explicit provider save and reviewed model import. Busy mutations suppress duplicate submission, preserve recoverable input on failure and announce completion once. Guard stale async auth results and dirty editor state. `CONTRACTS.md` records proposed navigation, refresh, draft-preservation and confirmation acceptance rules; destructive confirmations must identify exact resource scope and never expose secrets.

## Content voice

Short user actions: Connect provider, Sign in, API key, Models, Advanced settings. Explain next steps, not compatibility stores. Preserve English/Chinese translations.

## Implementation constraints

Go-embedded vanilla HTML/CSS/JS and existing manifests. UI refinement under this design adds no dependencies, new endpoints, storage migrations or backend/adapter rewrite. AuthFlow comes from backend metadata, never preset key inference. Generic provider OAuth creates a connection selected before explicit provider save; Cline OAuth creates a Cline Account directly. OpenCode has no supported OAuth/session import admin endpoint. Preserve existing dirty edits. C1 account deletion is implemented as the isolated Phase 2 safety fix recorded in `CONTRACTS.md`; C2 provider-scoped model removal remains a separate proposed backend fix, not a UI change authorized by this document.

## Open questions

Future global defaults are deferred at the user request. The existing shared egress pool covers Cline, OAuth and custom providers without overrides; native Zen retains its independent pool. OpenCode session-management expansion requires a separate backend/API task.
