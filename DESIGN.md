# Design

## Source of truth

Active, 2026-11-06. Provider setup, Providers, Models and shared typography. Evidence reviewed: CONTEXT.md; provider/account/model/network HTML; auth, preset, editor and global view JS; CSS tokens and browser baselines. Latest user instruction supersedes broad monospace styling.

## Brand

Approachable, clear developer application. Real configuration and honest status build trust. Avoid decorative heroes, tiny metadata, unnecessary jargon and dense capability dashboards.

## Product goals

One Connect provider entry point on Providers; original integrations and request contracts preserved. Carry OAuth from sign-in to account selection and explicit provider save. Primary Models catalog with secondary provider availability.

## Personas and jobs

Self-hosted gateway operators connect services, add accounts, choose models and diagnose issues. New users need clear choices; experienced operators retain advanced controls.

## Information architecture

Keep all 13 sidebar routes and legacy aliases. Providers owns shared setup and connected inventory. Accounts & Credentials has OAuth Connected Accounts and API Credentials. Cline and generic OAuth retain native account controls in the same destination. Providers has inventory and OAuth setup tabs; native service configuration, proxies and headers live with each provider. Network owns the existing shared default egress pool and a read-only inventory of provider overrides. Models has Catalog and Provider availability subviews.

## Design principles

One primary action; progressive disclosure; reuse working forms and handlers. Enabled means configured, not tested healthy. Do not invent model access, auth flows or status.

## Visual language

Inter and system fallbacks for normal app content and metrics. No bundled monospace font. Existing intentional dark/light themes; readable 13-14px labels, 14px body, modest headings, consistent spacing and borders.

## Components

Reuse shared drawers, confirmations, original provider editor, login/import modals and model controls. Add a searchable setup directory; temporarily move original Cline/OpenCode nodes from Providers into its workspace and restore them without cloning IDs/listeners. Existing CSS tokens own styling.

## Accessibility

Semantic labelled dialogs and buttons; keyboard setup, focus containment/restoration, Escape, meaningful state text and reduced motion. Preserve nested auth dialogs and mobile navigation behavior.

## Responsive behavior

Verify 1440,1280,1024,768,390,360px in both themes. Stack provider items and filters on mobile. Drawer fits viewport and scrolls internally.

## Interaction states

Loading/retry/no-match directory; no auth or saves until deliberate action. Retain existing auth polling/errors, config autosave, explicit provider save and reviewed model import. Guard stale async auth results and dirty editor state.

## Content voice

Short user actions: Connect provider, Sign in, API key, Models, Advanced settings. Explain next steps, not compatibility stores. Preserve English/Chinese translations.

## Implementation constraints

Go-embedded vanilla HTML/CSS/JS and existing manifests. No dependencies, new APIs, storage migrations or backend/adapter rewrite. AuthFlow comes from backend metadata, never preset key inference. OAuth creates a connection; select it before explicit save. OpenCode has no supported OAuth/session import admin API. Preserve existing dirty edits.

## Open questions

Future global defaults are deferred at the user request. The existing shared egress pool covers Cline, OAuth and custom providers without overrides; native Zen retains its independent pool. OpenCode session-management expansion requires a separate backend/API task.
