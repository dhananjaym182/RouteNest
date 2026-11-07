# RouteNest Admin UI — Visual Design Benchmark and Direction Review

Reviewed: 2026-11-06
Scope: current dirty worktree, live Go-embedded admin at `http://127.0.0.1:3457/admin/`
Method: read-only source inspection plus Playwright screenshots at 1440px and 390px in light and dark themes. No application source files were changed.

## Executive assessment

RouteNest already has a credible developer-infrastructure shell: the navigation is grouped well, the palette is restrained, both themes share a coherent token system, the Models page is inventory-oriented, and Request Logs has real filters, a dense desktop table, attempt traces, and a right-side inspection drawer.

The current UI falls short of the requested direction in four concrete areas:

1. Typography is materially too small on operational surfaces. Browser evidence measured 10px table headers and 12px table cells, while the target is approximately 14px. Desktop page headings render at 32px, above the requested 25–28px range.
2. IBM Plex Mono is absent. `--font-mono` points to Inter, and the final stylesheet explicitly forces technical identifiers back to the UI font.
3. Providers and API credentials remain card inventories. The provider cards have been compressed into row-like layouts, but they still scale vertically and lack the sorting and column scanability of a management table.
4. Routing explains the global pipeline but does not show an operational route detail: candidate providers/accounts/offerings, eligibility, rejection reasons, order, and the eventual fallback path.

These are functional presentation problems rather than requests for a new visual identity. The purple accent, sidebar width, border radii, and general neutral palette are subjective choices and do not need redesign.

## Benchmark synthesis

| Product | Useful reference pattern | RouteNest implication |
| --- | --- | --- |
| Vercel AI Gateway | Dense request table, composable filters, live tailing, resizable side inspector, one routing card per provider attempt with status and timing | Keep RouteNest's table and drawer; strengthen the attempt trace and selected-request context |
| Portkey | Compact per-request gateway state for cache/retry/fallback/load balance; raw request/response inspector and config linkage | Surface routing state at row-scan speed when the existing log payload supports it |
| Cloudflare AI Gateway | Narrow overview hierarchy: requests, tokens, costs, errors, cached responses with a time filter | Keep the RouteNest overview restrained; do not add more decorative KPI cards |
| LiteLLM Admin UI | Table-first keys and model inventories with search, filters, columns, status, spend/budget, and pagination | Use management tables for providers and API credentials; preserve the existing Models pattern |
| Helicone | A few meaningful charts plus hierarchical trace/timeline views | Use hierarchy for fallback and route inspection, not more summary cards |

Official evidence:

- Vercel: <https://vercel.com/docs/ai-gateway/observability-and-spend/logs> and <https://vercel.com/docs/ai-gateway/observability>
- Portkey: <https://portkey.ai/docs/product/observability/logs> and <https://portkey.ai/docs/product/ai-gateway/configs>
- Cloudflare: <https://developers.cloudflare.com/ai-gateway/observability/analytics/>
- LiteLLM: <https://docs.litellm.ai/docs/proxy/ui>, <https://docs.litellm.ai/docs/proxy/ai_hub>, and <https://docs.litellm.ai/docs/proxy/ui_logs>
- Helicone: <https://www.helicone.ai/> and <https://docs.helicone.ai/features/sessions.md>

## Browser evidence

The complete screenshot set is in [`review-artifacts/ui-benchmark`](./ui-benchmark/).

Representative desktop screenshots:

- [Overview — light, 1440px](./ui-benchmark/light-1440-dashboard.png)
- [Providers — light, 1440px](./ui-benchmark/light-1440-providers.png)
- [Models — light, 1440px](./ui-benchmark/light-1440-models.png)
- [Routing — light, 1440px](./ui-benchmark/light-1440-routing.png)
- [Request Logs — light, 1440px](./ui-benchmark/light-1440-logs.png)
- [Request inspector — light, 1440px](./ui-benchmark/light-1440-logs-detail.png)
- [Overview — dark, 1440px](./ui-benchmark/dark-1440-dashboard.png)
- [Request inspector — dark, 1440px](./ui-benchmark/dark-1440-logs-detail.png)

Representative mobile screenshots:

- [Overview — light, 390px](./ui-benchmark/light-390-dashboard.png)
- [Providers — light, 390px](./ui-benchmark/light-390-providers.png)
- [Accounts — light, 390px](./ui-benchmark/light-390-credentials.png)
- [Models — light, 390px](./ui-benchmark/light-390-models.png)
- [Routing — light, 390px](./ui-benchmark/light-390-routing.png)
- [Request Logs viewport — light, 390px](./ui-benchmark/light-390-logs-viewport.png)
- [Request inspector — light, 390px](./ui-benchmark/light-390-logs-detail.png)
- [Settings — light, 390px](./ui-benchmark/light-390-about.png)
- [Providers — dark, 390px](./ui-benchmark/dark-390-providers.png)

Measured browser values:

| Element | 1440px | 390px | Assessment |
| --- | ---: | ---: | --- |
| Page title | 32px | 25px | Mobile is on target; desktop is oversized |
| Body/subtitle | 14px | 14px | Readable and on target |
| Table header | 10px | table replaced by cards | Too small |
| Table cell | 12px | table replaced by cards | Too small |
| Log detail button | 11px | card action | Too small on desktop |
| Section metric heading | 11px | 11px | Weak hierarchy, especially uppercase |
| Settings shortcut description | 11px | 11px | Too small for ordinary explanatory text |

No horizontal document overflow was detected at either width. Mobile pages intentionally replace account and log tables with cards.

## Prioritized findings

### P1 — Restore the typography contract

This is the clearest mismatch with the requested specification.

- Inter is correctly embedded and used for the interface: [`00_base.css`](../internal/admin/ui/css/00_base.css) lines 21 and 59.
- `--font-mono` currently aliases the UI font: [`00_base.css`](../internal/admin/ui/css/00_base.css) line 22.
- The final cascade layer forces model IDs, JSON, routes, metrics, keys, and other technical values to Inter: [`96_workflows.css`](../internal/admin/ui/css/96_workflows.css) lines 3–6. It wins because it loads last in [`ui_manifest.go`](../internal/admin/ui_manifest.go) lines 62–83.
- Operational table cells and headers are set to 12px and 10px: [`95_control_plane.css`](../internal/admin/ui/css/95_control_plane.css) lines 82–85.
- Desktop headings can reach 32px: [`95_control_plane.css`](../internal/admin/ui/css/95_control_plane.css) line 54.

Recommended change:

- Embed IBM Plex Mono and assign it only to model IDs, request IDs, API paths, endpoints, keys, token counts, JSON/code, and selected machine-readable values.
- Remove the blanket technical-selector override in `96_workflows.css`.
- Set desktop page headings to 26–28px, section headings to 17–19px, navigation and table content to 14px, form labels to 14px, and secondary text to 12–13px.
- Keep ordinary numeric KPIs in Inter unless they are acting as machine-readable technical values.

### P1 — Convert Providers and API Credentials to management tables on desktop

Providers are built as one `<article>` per service with metadata, capability disclosure, primary actions, and four related-view links: [`18_global.js`](../internal/admin/ui/js/18_global.js) lines 168–209. API credentials use a card grid: the same file, lines 212–236.

The desktop styling makes provider cards look like wide rows, which is better than a tile grid, but they remain vertically expensive and cannot provide mature table behavior. At 390px, five providers produce a 3445px page.

Recommended Provider columns:

| Provider | Status | Type | Auth | Credentials | Offerings | Base URL | Actions |
| --- | --- | --- | --- | ---: | ---: | --- | --- |

Recommended Credential columns:

| Credential | Provider | Auth type | Status | Expiry/cooldown | Requests | Tokens | Actions |
| --- | --- | --- | --- | --- | ---: | ---: | --- |

Keep capability details and full connection settings in a drawer. Preserve mobile cards, but reduce them to a compact summary plus one disclosure/action row.

### P1 — Add an operational route-detail view using existing data

The five-step routing flow is clear and worth preserving: [`routing.html`](../internal/admin/ui/html/pages/routing.html) lines 14–24. It explains logical model resolution, candidate selection, eligibility, dispatch, and fallback.

The gap is that it is a static global explanation. It does not answer:

- Which providers, credentials, and model offerings are candidates for a selected logical model?
- Which candidates are eligible, cooling down, unavailable, disabled, or unverified?
- What is the effective order?
- Why would a candidate be skipped?
- Which fallback is next?

Recommended change: add a selected-model route detail below the existing flow using the already loaded provider-instance, credential, model-offering, and provider-state projections. Show only facts available from current data; use `Unknown` or `Unverified` rather than inferred health or entitlement.

On mobile, the current flow becomes a horizontally scrolling strip: [`95_control_plane.css`](../internal/admin/ui/css/95_control_plane.css) lines 184–190. Add a visible continuation cue or convert it to a vertical numbered path at narrow widths.

### P1 — Increase Request Logs readability while preserving its strong interaction model

Request Logs is the strongest operational page in the current implementation.

Strengths:

- Time, provider, model, protocol, text search, quick status chips, live following, load more, download, error download, and fullscreen: [`logs.html`](../internal/admin/ui/html/pages/logs.html) lines 8–49.
- A 13-column desktop table and dedicated mobile cards: [`14_logs.js`](../internal/admin/ui/js/14_logs.js) lines 211–278.
- The inspector is already styled as a full-height right-side drawer: [`95_control_plane.css`](../internal/admin/ui/css/95_control_plane.css) lines 169–171.
- The drawer shows summary values, attempt trace, errors, and raw JSON: [`14_logs.js`](../internal/admin/ui/js/14_logs.js) lines 411–441.

Problems:

- 10px headers and 12px cells reduce scanability.
- The drawer is a single vertical document. It should visually separate Summary, Routing attempts, Error/response, Usage, and Raw JSON where those fields already exist.
- Mobile renders every loaded log as a full card. With 50 records, the captured page was about 20,638px tall.

Recommended change:

- Raise table content to 14px and headers to 12–13px.
- Preserve the drawer and table context.
- Present each attempt as a compact ordered route step with outcome, provider/account, duration/TTFT, and error reason when present.
- On mobile, use a compact two-line request row opening the same full-screen drawer, rather than a full metric card for every record.
- Keep the existing disclosure that filters apply to loaded records unless the backend contract is separately expanded.

### P2 — Keep Overview restrained, but fix hierarchy scale

The overview uses one main status/traffic region, four compact KPIs, charts/lists, recent requests, attention items, and a final quick-actions section: [`dashboard.html`](../internal/admin/ui/html/pages/dashboard.html) lines 14–90. This is appropriate and already closer to Cloudflare's metric hierarchy than to a decorative dashboard.

Recommended change:

- Keep the card count and current metric scope.
- Reduce the desktop page title from 32px to 26–28px.
- Raise 11px uppercase section labels to 12–13px or use 17–18px sentence-case section headings.
- Do not add new metrics unless the backend already provides trustworthy values. Cost and cache-rate metrics should remain absent when unavailable.

### P2 — Preserve Models; adjust typography and row expansion

Models is already close to the requested target. It has a catalog/provider-availability split, search, provider/billing/status filters, bulk controls, grouped inventory, and an offerings table: [`models.html`](../internal/admin/ui/html/pages/models.html) lines 15–98.

Recommended change:

- Keep the existing inventory model.
- Render model IDs in IBM Plex Mono at 12–13px while keeping row text and controls at approximately 14px.
- When a provider group is expanded, preserve a stable column header and avoid turning rows into card-like blocks.

### P2 — Accounts is structurally sound; unify the credential subview

The Cline account pool uses a desktop table with search, bulk actions, paging, and mobile cards: [`accounts.html`](../internal/admin/ui/html/pages/accounts.html) lines 27–60. This is the right pattern.

Recommended change:

- Increase table typography.
- Keep the mobile card transformation for accounts.
- Convert the API Credentials subview from cards to the credential table described above.
- Keep diagnostics collapsed by default; they are secondary to account operations.

### P3 — Settings needs refinement, not restructuring

The actual settings pages use straightforward grouped forms. Access settings are clear in [`security.html`](../internal/admin/ui/html/pages/security.html) lines 32–73, and Network uses a direct form in [`upstreams.html`](../internal/admin/ui/html/pages/upstreams.html) lines 18–44.

The four cards on the Settings & Runtime landing page are navigation shortcuts, not nested content containers: [`about.html`](../internal/admin/ui/html/pages/about.html) lines 10–15. Their use is acceptable.

Recommended change:

- Raise shortcut descriptions from 11px to 13px.
- Consider separating product/about information from frequently changed settings in a later information-architecture pass. This is a preference-level improvement, not a blocking design problem.

## Theme assessment

Both themes look professionally designed at the foundation level.

- Light and dark tokens cover backgrounds, surfaces, borders, primary/secondary text, semantic states, shadows, overlays, and native `color-scheme`: [`00_base.css`](../internal/admin/ui/css/00_base.css) lines 2–53.
- The dark theme preserves hierarchy and does not merely invert colors.
- Borders and shadows are restrained in both themes.
- The main weakness in both themes is the same small typography. Tiny secondary labels are especially easy to lose in the dark theme and low-contrast light surfaces.

No obvious theme-only structural failure was observed. A formal WCAG contrast audit was outside this review; the recommendation is to test the final enlarged typography and technical font rather than tune colors first.

## Genuine problems vs subjective preferences

Genuine design problems:

- Missing IBM Plex Mono technical typeface.
- 10–12px operational tables.
- 32px desktop page headings outside the requested scale.
- Provider and API-credential card inventories instead of scalable tables.
- Static routing explanation without route-detail inspection.
- Extremely long mobile log/provider pages.
- Ordinary settings descriptions rendered at 11px.

Subjective preferences that do not justify redesign:

- Purple accent color.
- Current sidebar width and grouped navigation.
- Rounded 9–14px corners.
- Settings shortcut cards.
- The overview's single status hero.
- The neutral blue-gray dark theme.

## Recommended implementation order

1. Typography tokens and selective IBM Plex Mono.
2. Table sizing across Accounts, Models offerings, Logs, and health/inventory tables.
3. Providers and API Credentials desktop tables, retaining compact mobile rows/cards.
4. Route-detail visualization using existing frontend data.
5. Request drawer information grouping and compact mobile log rows.
6. Minor overview and settings hierarchy adjustments.

Stop after these changes are verified at 1440px and 390px in both themes. A second broad visual redesign is not supported by the evidence.
