// Shared I18N dictionary reader for the admin UI gates.
//
// The dictionary lives in one flat asset (internal/admin/ui/js/01_i18n.js) so the
// gates can parse it the same way the runtime object is written: single-quoted
// keys and values. Every gate reports findings and exits non-zero; there is no
// silent success.

const fs = require("fs");
const path = require("path");

const ROOT = path.resolve(__dirname, "..");
const UI = path.join(ROOT, "internal/admin/ui");

function uiJSFiles() {
  const dir = path.join(UI, "js");
  return fs
    .readdirSync(dir)
    .filter((f) => f.endsWith(".js"))
    .sort()
    .map((f) => path.join(dir, f));
}

function dictFile() {
  for (const f of uiJSFiles()) {
    if (/const I18N\s*=\s*\{/.test(fs.readFileSync(f, "utf8"))) return f;
  }
  throw new Error("I18N dictionary block not found under internal/admin/ui/js");
}

// parseDict returns the ordered entries, duplicate keys, unescaped apostrophes
// and exact English-value collisions.
function parseDict() {
  const file = dictFile();
  const src = fs.readFileSync(file, "utf8");
  const m = src.match(/const I18N\s*=\s*\{([\s\S]*?)\n\};/);
  if (!m) throw new Error("I18N object literal is malformed in " + file);

  const entries = [];
  const kre = /^\s*'((?:[^'\\]|\\.)*)'\s*:\s*'((?:[^'\\]|\\.)*)'/gm;
  let k;
  while ((k = kre.exec(m[1]))) {
    entries.push({
      key: k[1].replace(/\\'/g, "'").replace(/\\\\/g, "\\"),
      rawKey: k[1],
      value: k[2].replace(/\\'/g, "'").replace(/\\\\/g, "\\"),
    });
  }

  const seen = new Map();
  const dups = [];
  for (const e of entries) {
    if (seen.has(e.key)) dups.push(e.key);
    else seen.set(e.key, e);
  }

  const byValue = new Map();
  const collisions = [];
  for (const e of entries) {
    if (!byValue.has(e.value)) byValue.set(e.value, []);
    byValue.get(e.value).push(e.key);
  }
  for (const [value, keys] of byValue) {
    if (keys.length > 1) collisions.push({ value, keys });
  }

  // Check the raw source value: a correctly escaped apostrophe appears as \' in
  // the literal, while an unescaped one terminates the string early.
  const escaped = [];
  {
    const raw = /^\s*'((?:[^'\\]|\\.)*)'\s*:\s*'((?:[^'\\]|\\.)*)'/gm;
    let r;
    while ((r = raw.exec(m[1]))) {
      if (/(^|[^\\])'/.test(r[2])) escaped.push({ rawKey: r[1], value: r[2] });
    }
  }
  return { file, entries, keys: seen, dups, collisions, escaped };
}

function reportDuplicates(dups) {
  if (dups.length) {
    console.error("DUP KEYS: " + dups.length + " " + dups.join(" | "));
    return false;
  }
  console.log("DUP KEYS: 0");
  return true;
}

module.exports = { ROOT, UI, uiJSFiles, dictFile, parseDict, reportDuplicates };
