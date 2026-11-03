// Dead-key gate: a dictionary key whose Chinese text appears nowhere outside the
// dictionary is unused copy. It either belongs to a removed control or was
// superseded by different wording; both are deleted, not allowlisted.
//
// A key also counts as used when another script calls t('...') with it, including
// dynamic label tables that are translated later.
//
// Usage: node tools/i18n_deadkeys.js
const fs = require("fs");
const { parseDict, uiJSFiles, dictFile, ROOT, reportDuplicates } = require("./i18n_lib");

const body = require("./admin_ui_body").adminBody(ROOT);
const dict = dictFile();
const others = [body];
for (const f of uiJSFiles()) {
  if (f !== dict) others.push(fs.readFileSync(f, "utf8"));
}

const { entries, dups } = parseDict();
const dead = [];
for (const e of entries) {
  // A key is used when its text appears anywhere outside the dictionary: as a
  // t('...') argument, in a dynamic label table, or as static markup text.
  const used =
    body.includes(e.key) || others.some((src) => src.includes(e.key));
  if (!used) dead.push(e.key);
}

let ok = reportDuplicates(dups);
console.log("DEAD KEYS: " + dead.length);
for (const d of dead) console.error("  " + JSON.stringify(d));
if (dead.length) ok = false;

process.exit(ok ? 0 : 1);
