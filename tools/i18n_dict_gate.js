// Dictionary gate: duplicate keys and unescaped apostrophes fail the build.
//
// Duplicate keys silently keep the last English value, so two call sites show
// different copy. An unescaped apostrophe breaks the object literal or truncates
// the value. English-value collisions are reported but are not failures: distinct
// Chinese keys may legitimately share one English phrase (账号管理/账户 -> Accounts).
//
// Usage: node tools/i18n_dict_gate.js [--check-collisions]
const { parseDict, reportDuplicates } = require("./i18n_lib");

const strictCollisions = process.argv.includes("--check-collisions");
const { entries, dups, collisions, escaped } = parseDict();

console.log("keys: " + entries.length);
let ok = reportDuplicates(dups);

if (escaped.length) {
  ok = false;
  console.error("UNESCAPED APOSTROPHES: " + escaped.length);
  for (const e of escaped) console.error("  " + e.rawKey + " => " + e.value);
} else {
  console.log("UNESCAPED APOSTROPHES: 0");
}

console.log("EN value collisions: " + collisions.length + (strictCollisions ? "" : " (advisory)"));
for (const c of collisions) console.log("  " + c.value + " <- [" + c.keys.join("] and [") + "]");

if (strictCollisions && collisions.length) {
  console.error("EN value collisions are not allowed with --check-collisions");
  ok = false;
}

process.exit(ok ? 0 : 1);
