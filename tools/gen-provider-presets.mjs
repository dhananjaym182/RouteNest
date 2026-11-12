#!/usr/bin/env node
// Generate internal/providerreg/imported.json from an ExtremeRouter provider
// registry checkout.
//
// Usage:
//   node tools/gen-provider-presets.mjs <registry-dir> [imported.json] [overrides.json]
//
// <registry-dir> is required (or set EXTREMEROUTER_REGISTRY). The output is
// deterministic: the same inputs always produce byte-identical JSON, and entries
// are ordered by code unit, not by locale.
//
// Hand-written presets live in internal/providerreg/manual/ and are never
// generated. A generated entry whose base URL matches a manual preset is skipped
// so each upstream has one definition. Manual corrections to generated entries
// are recorded in tools/provider-presets.overrides.json and applied after mapping.
// If the registry checkout contains public/providers/<id>.<ext>, the generated
// preset also points at RouteNest's embedded copy under /admin/assets/providers/.
import fs from "fs";
import path from "path";
import { fileURLToPath, pathToFileURL } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.join(__dirname, "..");

export const SKIP_IDS = {
  qwen2api: "personal netlify deployment of ExtremeRouter, not a public provider",
  opencode: "zen source handled by dedicated zen transport, not generic preset",
  uncloseai: "key registration closed upstream (401 public_beta_closed), preset dead",
  mimocode: "broken upstream endpoint (404), preset dead",
};
export const EXCLUDE_KEYS = [/tts/i, /voice/i, /-web$/, /web-cookie/];

// Code-unit comparison: stable across locales and Node builds.
const byCodeUnit = (a, b) => (a < b ? -1 : a > b ? 1 : 0);

// normalizeLocalhost maps localhost to 127.0.0.1 for base-URL comparison only.
export function normalizeLocalhost(url) {
  return url.replace(/\/\/localhost\b/, "//127.0.0.1");
}

// manualBaseURLs returns the base URLs of hand-written presets, which generated
// entries must not duplicate.
export function manualBaseURLs(manualDir) {
  const out = new Set();
  if (!fs.existsSync(manualDir)) return out;
  for (const f of fs.readdirSync(manualDir).sort(byCodeUnit)) {
    if (!f.endsWith(".json")) continue;
    const p = JSON.parse(fs.readFileSync(path.join(manualDir, f), "utf8"));
    if (p.baseURL) out.add(normalizeLocalhost(p.baseURL));
  }
  return out;
}

// mapModule converts one ExtremeRouter registry module into a candidate record,
// or returns null when the module is outside the provider catalogue.
export function mapModule(mod, skipped, file) {
  if (!mod || typeof mod !== "object") return null;
  const category = mod.category;
  const transport = mod.transport || {};
  const format = transport.format || "openai";
  const baseUrl = transport.baseUrl || (Array.isArray(transport.baseUrls) ? transport.baseUrls[0] : "");
  const name = mod.display?.name || mod.id;
  const headers = {};
  for (const [k, v] of Object.entries(transport.headers || {})) {
    if (/^(accept|x-amz|user-agent|content-type)/i.test(k)) continue;
    headers[k] = String(v);
  }
  if (category !== "free" && category !== "freeTier" && !(category === "apikey" && (mod.hasFree || mod.freeModes))) {
    return null;
  }
  if (format !== "openai") {
    skipped.push([mod.id, `non-OpenAI transport ${format}; requires protocol adapter`]);
    return null;
  }
  const base = String(baseUrl).replace(/\/+(chat\/completions|responses)$/i, "");
  if (!base) {
    skipped.push([mod.id, "no chat/completions-capable baseURL"]);
    return null;
  }
  const fetcher = transport.modelsFetcher || mod.modelsFetcher || {};
  // No seeded model list: the catalogue is fetched live at configuration time
  // (see the provider editor's model picker), so presets carry no stale models.

  const tiered = category === "free" || category === "freeTier";
  const host = (() => { try { return new URL(base).hostname; } catch { return ""; } })();
  const isLocal = host === "localhost" || host === "127.0.0.1" || host === "::1";
  // kind sets the UI grouping: free (no key) > freeTier > hasFree > local > other.
  const kind = mod.noAuth === true ? "free" : tiered ? "freeTier" : isLocal ? "local" : "hasFree";
  return {
    key: mod.id,
    displayName: name,
    baseURL: base,
    kind,
    hasFree: tiered || mod.noAuth === true,
    headers: Object.keys(headers).length ? headers : undefined,
    modelsFetcher: fetcher?.url ? { url: fetcher.url, type: fetcher.type || "openai" } : undefined,
    notes: `${mod.id} (from extremerouter registry)`,
    file,
  };
}

// loadRegistry imports every registry module in sorted filename order.
export async function loadRegistry(dir, skipped) {
  const files = fs
    .readdirSync(dir)
    .filter((f) => f.endsWith(".js") && f !== "index.js" && f !== "README.js")
    .sort(byCodeUnit);
  const presets = [];
  for (const f of files) {
    let mod;
    try {
      mod = (await import(pathToFileURL(path.join(dir, f)).href)).default;
    } catch (e) {
      skipped.push([path.basename(f, ".js"), `import failed: ${e.message}`]);
      continue;
    }
    const rec = mapModule(mod, skipped, f);
    if (rec) presets.push(rec);
  }
  return presets;
}

function providerLogoPath(registryDir, id) {
  let current = path.resolve(registryDir);
  while (true) {
    const providersDir = path.join(current, "public", "providers");
    for (const ext of ["png", "jpg", "jpeg", "webp", "svg"]) {
      if (fs.existsSync(path.join(providersDir, `${id}.${ext}`))) {
        return `/admin/assets/providers/${id}.png`;
      }
    }
    const parent = path.dirname(current);
    if (parent === current) return "";
    current = parent;
  }
}

// generate returns the imported catalogue as a plain object keyed by id, plus
// notices for skipped entries. It does not touch the file system except to read
// inputs.
export async function generate({ registryDir, manualDir, overrides }) {
  const skipped = [];
  const presets = await loadRegistry(registryDir, skipped);
  const manual = manualBaseURLs(manualDir);
  const used = new Set();
  const notices = [];
  const imported = {};
  for (const p of presets) {
    if (used.has(p.key)) continue;
    used.add(p.key);
    if (SKIP_IDS[p.key]) {
      notices.push([p.key, SKIP_IDS[p.key] + ", skipped"]);
      continue;
    }
    if (manual.has(normalizeLocalhost(p.baseURL))) {
      notices.push([p.key, "duplicate of a manual preset, skipped"]);
      continue;
    }
    if (EXCLUDE_KEYS.some((re) => re.test(p.key))) {
      notices.push([p.key, "non chat/completions source (web/speech), skipped"]);
      continue;
    }
    // The override is applied while the preset is built, so a manual
    // correction lands in the same key order as a generated field.
    const fix = overrides[p.key];
    const modelsFetcher = fix && fix.modelsFetcher
      ? { url: fix.modelsFetcher.url, type: fix.modelsFetcher.type || "openai" }
      : (p.modelsFetcher ? { url: p.modelsFetcher.url, type: p.modelsFetcher.type } : undefined);
    const logo = fix && fix.logo !== undefined ? fix.logo : providerLogoPath(registryDir, p.key);
    const preset = {
      id: p.key,
      name: fix && fix.name !== undefined ? fix.name : p.displayName,
      baseURL: fix && fix.baseURL !== undefined ? fix.baseURL : p.baseURL,
      ...(p.headers ? { headers: p.headers } : {}),
      ...(p.notes ? { notes: p.notes } : {}),
      freeTier: p.hasFree,
      ...(modelsFetcher ? { modelsFetcher } : {}),
      kind: fix && fix.kind !== undefined ? fix.kind : p.kind,
      ...(logo ? { logo } : {}),
    };
    if (fix) {
      notices.push([p.key, "manual override applied"]);
    }
    imported[p.key] = preset;
  }
  for (const [id, why] of skipped) notices.push([id, why]);
  const sorted = Object.fromEntries(Object.keys(imported).sort(byCodeUnit).map((k) => [k, imported[k]]));
  return { imported: sorted, notices };
}

export function serialize(obj) {
  return JSON.stringify(obj, null, 2) + "\n";
}

async function main() {
  const registryDir = process.argv[2] || process.env.EXTREMEROUTER_REGISTRY;
  if (!registryDir) {
    process.stderr.write("usage: gen-provider-presets.mjs <registry-dir> [imported.json] [overrides.json]\n");
    process.exit(2);
  }
  const outPath = process.argv[3] || path.join(repoRoot, "internal", "providerreg", "imported.json");
  const overridesPath = process.argv[4] || path.join(__dirname, "provider-presets.overrides.json");
  const manualDir = process.argv[5] || path.join(repoRoot, "internal", "providerreg", "manual");
  const overrides = fs.existsSync(overridesPath) ? JSON.parse(fs.readFileSync(overridesPath, "utf8")) : {};

  const { imported, notices } = await generate({ registryDir, manualDir, overrides });
  fs.mkdirSync(path.dirname(outPath), { recursive: true });
  fs.writeFileSync(outPath, serialize(imported));
  process.stderr.write(`wrote ${Object.keys(imported).length} imported presets to ${outPath}\n`);
  process.stderr.write(`notices ${notices.length}:\n`);
  for (const [id, why] of notices.slice(0, 80)) process.stderr.write(`  ${id}: ${why}\n`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await main();
}
