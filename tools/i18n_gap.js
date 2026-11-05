// Gap gate: every Chinese text node or title/placeholder/aria-label in the
// assembled body must have a dictionary entry, or the English UI keeps Chinese.
//
// Usage: node tools/i18n_gap.js
const { parseDict, ROOT } = require("./i18n_lib");

const body = require("./admin_ui_body").adminBody(ROOT);
const { keys } = parseDict();

const zh = /[\u4e00-\u9fff]/;
const gaps = [];
const seen = new Set();

function decode(s) {
  return s.replace(/&(gt|lt|amp|quot|nbsp|#x?[0-9a-fA-F]+);/g, (_, e) => {
    if (e === "gt") return ">";
    if (e === "lt") return "<";
    if (e === "amp") return "&";
    if (e === "quot") return '"';
    if (e === "nbsp") return " ";
    if (e[0] === "#") return String.fromCodePoint(e[1] === "x" ? parseInt(e.slice(2), 16) : parseInt(e.slice(1), 10));
    return _;
  });
}

function check(t, ctx) {
  t = decode(t.trim());
  if (!t || !zh.test(t)) return;
  if (keys.has(t)) return;
  const id = ctx + " | " + t.slice(0, 160);
  if (seen.has(id)) return;
  seen.add(id);
  gaps.push(id);
}

for (const p of body.split(/<[^>]*>/)) check(p, "text");
const attrRe = /(title|placeholder|aria-label)="([^"]*)"/g;
let a;
while ((a = attrRe.exec(body))) check(a[2], a[1]);

console.log("GAPS: " + gaps.length);
for (const g of gaps) console.log(g);
process.exit(gaps.length ? 1 : 0);
