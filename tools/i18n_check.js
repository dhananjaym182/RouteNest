// Aggregate admin i18n gate. Runs the dictionary, gap, dead-key and literal
// checks in order and exits non-zero if any of them reports a finding.
//
// Usage: node tools/i18n_check.js
const { spawnSync } = require("child_process");

const checks = ["i18n_dict_gate.js", "i18n_gap.js", "i18n_deadkeys.js", "i18n_literals.js"];
let failed = false;
for (const script of checks) {
  const res = spawnSync(process.execPath, [require("path").join(__dirname, script)], {
    stdio: "inherit",
  });
  if (res.status !== 0) {
    failed = true;
    console.error("FAILED: " + script);
  }
}
process.exit(failed ? 1 : 0);
