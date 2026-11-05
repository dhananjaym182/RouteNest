// Literal gate: a user-visible Chinese string in admin JS must be a dictionary
// key. Comment lines are ignored. The dictionary asset itself is skipped.
//
// Loving the rule "every Chinese literal is a key" also catches `t('...')` calls
// with no entry: those never translate, so they are the same defect as a bare
// literal.
//
// Usage: node tools/i18n_literals.js [file ...]   (default: every admin JS asset)
const fs = require("fs");
const { parseDict, uiJSFiles, dictFile } = require("./i18n_lib");

const zh = /[\u4e00-\u9fff]/;
const { keys } = parseDict();
const dict = dictFile();
const files = process.argv.length > 2 ? process.argv.slice(2) : uiJSFiles();

let failed = false;
let findings = 0;
for (const f of files) {
  if (f === dict) continue;
  const lines = fs.readFileSync(f, "utf8").split("\n");
  lines.forEach((line, i) => {
    if (!zh.test(line)) return;
    const trimmed = line.trim();
    if (trimmed.startsWith("//") || trimmed.startsWith("*") || trimmed.startsWith("/*")) return;
    const code = line.replace(/\/\/[^'"]*$/, "");
    const lits = code.match(/'[^'\n]*'|"[^"\n]*"|`[^`\n]*`/g) || [];
    for (const lit of lits) {
      if (!zh.test(lit)) continue;
      const body = lit.slice(1, -1);
      if (keys.has(body)) continue;
      findings++;
      failed = true;
      console.error(f + ":" + (i + 1) + ": Chinese literal without a dictionary key: " + lit.slice(0, 120));
    }
  });
}

console.log("LITERALS WITHOUT KEYS: " + findings);
process.exit(failed ? 1 : 0);
