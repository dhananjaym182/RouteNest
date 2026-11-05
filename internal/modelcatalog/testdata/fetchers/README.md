# Catalog fetcher fixtures

Offline response bodies used by `fetchers_test.go`.

| File | Fetcher type | Provenance | Status |
| --- | --- | --- | --- |
| `bynara-v1-models.json` | `bynara` | Captured `/v1/models` payload (2026-09-10) copied from the ExtremeRouter test `tests/unit/bynara-models-fetcher.test.js` | Generic OpenAI list shape verified; context and capability fields are not read by RouteNest |

Special fetcher types without a verified fixture keep the generic behaviour and
are marked in `strategy.go`. Their expected generic outcome is pinned in
`fetchers_test.go`.
