## 1. Final Conclusion

**Cline 4.1.15's Free Models come from:**
```text
GET https://api.cline.bot/api/v1/ai/cline/recommended-models
```

The JSON returned by this API contains three arrays: `recommended`, `free`, and `clinePass`. **Free models come directly from the server-returned `free` array**; the client performs no price judgment of its own.

---

## 2. API Information

| Item | Value |
|---|---|
| **HTTP Method** | `GET` |
| **URL** | `https://api.cline.bot/api/v1/ai/cline/recommended-models` |
| **Authentication** | **No login / no token required** |
| **Env var override** | `CLINE_API_BASE_URL` (production default `https://api.cline.bot`) |

---

## 3. Complete Request

```bash
curl 'https://api.cline.bot/api/v1/ai/cline/recommended-models'
```

- **Query params**: none
- **Body**: none
- **Headers**: only default browser/Node headers, **no custom Authorization**
- **Timeout**: 5000ms (`T_p=5e3`)
- **Cache**: 5 minutes (`fMh`)

---

## 4. Response Example

```json
{
  "recommended": [
    {"id": "anthropic/claude-opus-4.6", "name": "Claude Opus 4.6", "description": "...", "tags": ["BEST"]}
  ],
  "free": [
    {"id": "kwaipilot/kat-coder-pro", "name": "KwaiKAT Kat Coder Pro", "description": "...", "tags": ["FREE"]},
    {"id": "arcee-ai/trinity-large-preview:free", "name": "Arcee AI Trinity Large Preview", "description": "...", "tags": ["FREE"]}
  ],
  "clinePass": []
}
```

---

## 5. Free Detection Logic

**Free determination basis**: the server-returned `free` array

- The client does **not check** fields like `pricing.input === 0` or `model.free === true`
- The backend `bqd()` function **force-sets** `pricing: {input:0, output:0}` on free models, but this is a setting rather than a filter
- The Free Tab renders the `r.free` array: `we.free.map(e => h$(e, "FREE"))`

---

## 6. Model Fields

| Field | Type | Meaning |
|---|---|---|
| `id` | string | **Actual API Model ID** (e.g. `kwaipilot/kat-coder-pro`) |
| `name` | string | Display name |
| `description` | string | Description |
| `tags` | string[] | Tags (e.g. `["FREE"]`) |
| `contextWindow` | number | Context window |
| `maxInputTokens` | number | Max input tokens |
| `maxTokens` | number | Max output tokens |
| `capabilities` | string[] | Capability list |
| `pricing` | object | Pricing (set to 0 on the client) |
| `releaseDate` | string | Release date |
| `family` | string | Model family |
| `status` | string | Status (active/preview/deprecated/legacy) |
| `modalities` | object | Input/output modalities |
| `operation` | string | Operation type |

---

## 7. Cline Internal Call Chain

```text
Free Tab UI (Settings → API Configuration → ClinePass → Free)
  ↓
next/webview-ui/build/assets/index.js
  ↓ Ar.makeUnaryRequest("refreshClineRecommendedModelsRpc", en.create({}), en.toJSON, YF.fromJSON)
  ↓ window.postMessage({type:"grpc_request", method:"refreshClineRecommendedModelsRpc", ...})
  ↓ ProtoBuf decode → ModelsService handler → ywu(t, e) function
      ↓ $0r() [5-minute in-memory cache]
          ↓ mMh() [cache miss]
              ↓ r7o({}) → B_p(fetch, `${base}/api/v1/ai/cline/recommended-models`, 5000)
                  ↓ R_p() → use().apiBaseUrl → p8t().apiBaseUrl → "https://api.cline.bot"
              ↓ k_p(i) [validate structure] → P_p(s, obn, timeout) [enrichment]
          ↓ Cache: K0r = {data, timestamp}
      ↓ Nst.create({recommended, free, clinePass})
  ↓ webview: v(R.free ?? []) → Free Tab render
```

**Key files/functions**:
- `xqd(t=fetch)` — builds the URL: `${p8t().apiBaseUrl}/api/v1/ai/cline/recommended-models`
- `p8t()` — returns the config; `YOt.production.apiBaseUrl = "https://api.cline.bot"`
- `ywu(t,e)` — RPC handler, calls `$0r()` → `r.free.map(...)` and returns the `free` array
- `r7o(t)` — the actual HTTP request; wraps `fetch()` via `B_p()`, 5-second timeout
- `B_p(t,e,r)` — fetch wrapper with AbortController, **no auth headers**
- `NQt` — hardcoded fallback (4 recommended + 2 free models)
- `Pge` — webview hardcoded fallback (4 recommended + 2 free models)

---

## 8. Token Provenance

| Question | Answer |
|---|---|
| Where does the token come from | **No token needed** |
| Is a token required | **No** |
| Can it be fetched while logged out | **Yes** |
| Do Free Models require a Cline account | **No** |

`xqd()` → `B_p()` → `fetch(url, {signal})` — **a purely unauthenticated GET request** with no Authorization/Cookie/API-Key headers.

---

## 9. Can Third-Party Tools Call It Directly

**✅ Yes, it can be called directly** — no token, no special headers required.

---

## 10. Implementation Approach

```text
My tool
    ↓
GET https://api.cline.bot/api/v1/ai/cline/recommended-models
    ↓
Parse the JSON response
    ↓
Extract the response.free array
    ↓
Display the model list (id, name, description)
```

**curl example**:
```bash
curl 'https://api.cline.bot/api/v1/ai/cline/recommended-models'
```

**Node.js example**:
```js
const res = await fetch('https://api.cline.bot/api/v1/ai/cline/recommended-models');
const data = await res.json();
const freeModels = data.free ?? [];
```

---

## 11. Combo Named-Model Routing (this project's extension)

This project supports **Combo (named model groups)** on the client side: an admin configures several models, in order, into a combo ID (e.g. `claude-free-combo`) in the `/admin/` dashboard, and clients use that ID as if it were a regular model name.

**Applicable endpoints** — combos are located by model name; all three endpoints support them:

| Endpoint | Usage |
|---|---|
| `POST /v1/chat/completions` | `"model": "<combo-id>"` |
| `POST /v1/messages` (Anthropic) | `"model": "<combo-id>"` |
| `POST /v1/responses` | `"model": "<combo-id>"` |

**Fallback semantics** — entries are tried in the order the admin configured:

1. Configuration order is priority: **enabled entries earlier in the list are tried first**, and the first success serves the request.
2. If an entry fails and is judged **retryable** (rate limit 429 / insufficient balance 402 / transient 5xx / timeout / account unavailable, plus an upstream 400 "context length exceeded" that still fails after truncation retry), it automatically **moves on to the next entry**.
3. **Permanent errors** (401/403 and general 400) fail immediately without trying the next entry.
4. **Pinned source entries**: an entry can explicitly pin `cline` / `provider` / `zen`; only that upstream is used and failure does **not** fall back to other sources. If the pinned source is unavailable for that model, the entry is skipped and the next entry continues (the whole combo is not aborted).
5. Disabled entries / duplicate entries / models disabled in the dashboard are skipped (they count in the trace, but no request is made).
6. By default the global ModelChain or free-chain fallback is not triggered (emergency fallback must be explicitly enabled by an admin).

**Finding available combo IDs** — `GET /v1/models` (and `/models`) merges enabled combos into the model list, with combo entries identified by `owned_by: "combo"`:

```bash
curl -H "x-api-key: YOUR_KEY" http://127.0.0.1:8999/v1/models
```

```json
{
  "object": "list",
  "data": [
    {"id": "kwaipilot/kat-coder-pro", "object": "model", "created": 1, "owned_by": "cline"},
    {"id": "claude-free-combo",  "object": "model", "created": 1, "owned_by": "combo"}
  ]
}
```

> Combos are created / edited / deleted / enabled-or-disabled in the `/admin/` dashboard; `created` in the response is the current timestamp.

---

## Appendix: About models shown in the UI such as `ox-alpha`

The `ox-alpha`, `deepseek-v4-flash`, and `laguna-s-2.1` shown in the UI are **locally hardcoded** fallback models, not Free models returned by the remote API:

| UI shows | Local hardcoded Model ID | Remote API Free model |
|---|---|---|
| Ox Alpha | `stealth/ox-alpha` | (different model) |
| DeepSeek V4 Flash | `deepseek-ai/deepseek-v4-flash` | (different model) |
| Laguna S 2.1 | `poolside/laguna-s-2.1:free` | (different model) |

Free models returned by the remote API (4.1.15 build): `kwaipilot/kat-coder-pro`, `arcee-ai/trinity-large-preview:free`
(depends on the server side and may be updated)
